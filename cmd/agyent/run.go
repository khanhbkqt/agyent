package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"agyent/builtin"
	"agyent/internal/adapters/channels/composite"
	"agyent/internal/adapters/channels/telegram"
	"agyent/internal/adapters/channels/zalo"
	contextAdapter "agyent/internal/adapters/context"
	evolutionAdapter "agyent/internal/adapters/evolution"
	"agyent/internal/adapters/harness/agy"
	"agyent/internal/adapters/mcp"
	pluginAdapter "agyent/internal/adapters/plugin"
	securityAdapter "agyent/internal/adapters/security"
	"agyent/internal/adapters/security/ipc"
	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/adapters/subagent"
	workspaceAdapter "agyent/internal/adapters/workspace"
	"agyent/internal/config"
	"agyent/internal/core/auth"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"
	"agyent/internal/core/engine"
	"agyent/internal/core/eventbus"
	"agyent/internal/core/execution"
	"agyent/internal/core/scheduler"
	"agyent/internal/logger"

	"github.com/spf13/cobra"
)

const banner = `
    _     ____ __   __ _____ _   _ _____ 
   / \   / ___|\ \ / /| ____| \ | |_   _|
  / _ \ | |  _  \ V / |  _| |  \| | | |  
 / ___ \| |_| |  | |  | |___| |\  | | |  
/_/   \_\\____|  |_|  |_____|_| \_| |_|  
🚀 High-Performance Go Gateway Daemon for Antigravity (AGY)
`

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the agyent gateway daemon",
	Long: `Starts the agyent daemon, connects to Telegram and Zalo bot gateways,
and begins processing inbound turns through the local Antigravity (AGY) harness.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Load and validate configuration
		cfg, err := config.Load(cfgFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading configuration from %s: %v\n", cfgFile, err)
			os.Exit(1)
		}

		if err := cfg.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "Configuration validation failed: %v\n", err)
			fmt.Fprintf(os.Stderr, "Please run 'agyent init' to setup your configuration.\n")
			os.Exit(1)
		}

		logLevel := cfg.Logging.Level
		if verbose {
			logLevel = "debug"
		}
		logFormat := cfg.Logging.Format
		mainLogger := logger.Init(logger.Options{
			Level:     logLevel,
			Format:    logFormat,
			AddSource: verbose || logLevel == "debug",
		})

		fmt.Print(banner)
		fmt.Println("------------------------------------------------------------")
		fmt.Printf("📦 Version:       %s (commit: %s, built: %s)\n", Version, GitCommit, BuildDate)
		fmt.Printf("📄 Config File:   %s\n", cfgFile)
		if len(cfg.Telegram.GetNormalizedBots()) > 0 {
			fmt.Printf("🌐 Telegram Mode: %s\n", cfg.Telegram.Mode)
		}
		if len(cfg.Zalo.GetNormalizedBots()) > 0 {
			fmt.Printf("🌐 Zalo Mode:     %s (API: %s)\n", cfg.Zalo.Mode, cfg.Zalo.APIURL)
		}
		fmt.Printf("🤖 AGY Binary:    %s (Streaming: %t)\n", cfg.AGY.BinaryPath, cfg.AGY.StreamingEnabled)
		fmt.Printf("🛡️ Security:      Preset '%s' (HITL Timeout: %ds)\n", cfg.Security.Preset, cfg.Security.ApprovalTimeoutSeconds)
		if cfg.Recovery.Enabled {
			fmt.Printf("🔄 Auto-Recovery: Mode '%s' (Max Retries: %d, Concurrency: %d)\n", cfg.Recovery.Mode, cfg.Recovery.MaxRetries, cfg.Recovery.MaxConcurrentRecoveries)
		}
		fmt.Printf("📁 Agents Dir:    %s\n", cfg.Storage.AgentsDir)
		fmt.Printf("💾 SQLite DB:     %s (WAL Mode)\n", cfg.Storage.DBPath)
		fmt.Printf("📝 Log Level:     %s (Format: %s)\n", logLevel, logFormat)
		fmt.Println("------------------------------------------------------------")

		mainLogger.Debug("Initializing agyent components...")

		// 1. Initialize Storage
		store, err := sqlite.Open(cfg.Storage.DBPath)
		if err != nil {
			mainLogger.Error("Failed to connect to SQLite storage", "db_path", cfg.Storage.DBPath, "error", err)
			fmt.Fprintf(os.Stderr, "❌ Failed to connect to SQLite storage: %v\n", err)
			os.Exit(1)
		}
		defer store.Close()
		type agyPreflightTarget struct {
			agentName    string
			projectID    string
			workspaceDir string
		}
		preflightTargets := make(map[string]agyPreflightTarget)

		// Sync configured agents from config.yaml into SQLite
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer syncCancel()
		for name, prof := range cfg.Agents {
			ws := prof.WorkspacePath
			if ws == "" {
				ws = config.ResolveAgentWorkspace(cfg.Storage.AgentsDir, name)
			}
			_ = os.MkdirAll(ws, 0755)
			agentRecord, getErr := store.GetAgent(syncCtx, name)
			if getErr != nil || agentRecord == nil {
				preset := prof.SecurityPreset
				if preset == "" {
					preset = "balanced"
				}
				agentRecord = &domain.Agent{
					Name:           name,
					Description:    prof.Description,
					Status:         domain.StatusInitialized,
					WorkspacePath:  ws,
					SecurityPreset: domain.SecurityPreset(preset),
					IsPublic:       prof.IsPublic,
					OwnerID:        prof.OwnerID,
					AllowedPaths:   prof.AllowedPaths,
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}
			} else {
				if prof.Description != "" {
					agentRecord.Description = prof.Description
				}
				if prof.WorkspacePath != "" {
					agentRecord.WorkspacePath = ws
				}
				if agentRecord.SecurityPreset == "" {
					if prof.SecurityPreset != "" {
						agentRecord.SecurityPreset = domain.SecurityPreset(prof.SecurityPreset)
					} else {
						agentRecord.SecurityPreset = domain.PresetBalanced
					}
				}
				if prof.OwnerID != "" {
					agentRecord.OwnerID = prof.OwnerID
				}
				if len(prof.AllowedPaths) > 0 {
					agentRecord.AllowedPaths = prof.AllowedPaths
				}
				agentRecord.IsPublic = prof.IsPublic
				agentRecord.UpdatedAt = time.Now()
			}
			if saveErr := store.SaveAgent(syncCtx, agentRecord); saveErr != nil {
				mainLogger.Warn("Failed to sync agent profile to SQLite", "agent", name, "error", saveErr)
			} else {
				mainLogger.Debug("Synced agent profile to SQLite", "agent", name, "is_public", agentRecord.IsPublic, "preset", agentRecord.SecurityPreset)
			}

			// Provision Workspace Hooks, Settings & AGY Project Grants for this agent
			if ws != "" {
				if _, err := securityAdapter.EnsureWorkspaceHooksProvisioned(ws, "", mainLogger); err != nil {
					fmt.Fprintf(os.Stderr, "❌ Failed to provision security hooks for agent %q: %v\n", name, err)
					os.Exit(1)
				}
				if err := securityAdapter.EnsureWorkspaceSettingsProvisioned(ws, mainLogger); err != nil {
					fmt.Fprintf(os.Stderr, "❌ Failed to provision workspace settings for agent %q: %v\n", name, err)
					os.Exit(1)
				}
			}
			if err := securityAdapter.EnsureAGYProjectProvisioned("agy-proj-"+name, name, ws, mainLogger); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to provision AGY project for agent %q: %v\n", name, err)
				os.Exit(1)
			}
			preflightTargets["agy-proj-"+name] = agyPreflightTarget{
				agentName: name, projectID: "agy-proj-" + name, workspaceDir: ws,
			}
		}
		defaultWs := filepath.Join(cfg.Storage.AgentsDir, "workspace")
		if _, err := securityAdapter.EnsureWorkspaceHooksProvisioned(defaultWs, "", mainLogger); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to provision security hooks for default workspace: %v\n", err)
			os.Exit(1)
		}
		if err := securityAdapter.EnsureWorkspaceSettingsProvisioned(defaultWs, mainLogger); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to provision workspace settings for default workspace: %v\n", err)
			os.Exit(1)
		}
		if err := securityAdapter.EnsureAGYProjectProvisioned("agy-proj-agyent", "agyent", defaultWs, mainLogger); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to provision AGY project for default agent: %v\n", err)
			os.Exit(1)
		}
		_ = securityAdapter.EnsureAGYProjectProvisioned("default-cli-project", "default-cli-project", defaultWs, mainLogger)
		preflightTargets["agy-proj-agyent"] = agyPreflightTarget{
			agentName: "agyent", projectID: "agy-proj-agyent", workspaceDir: defaultWs,
		}

		// Probe required AGY security capabilities and verify every provisioned
		// project grant reaches the fail-closed workspace hook before serving turns.
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		caps, probeErr := agy.ProbeCapabilities(probeCtx, cfg.AGY.BinaryPath)
		probeCancel()
		if probeErr != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to verify AGY CLI security capabilities: %v\n", probeErr)
			os.Exit(1)
		}
		if !caps.SupportsSandbox || !caps.SupportsProjectScopedGrants || !caps.SupportsStreamJSON {
			fmt.Fprintf(os.Stderr, "❌ AGY CLI lacks required security capabilities (sandbox=%t project_grants=%t stream_json=%t)\n",
				caps.SupportsSandbox, caps.SupportsProjectScopedGrants, caps.SupportsStreamJSON)
			os.Exit(1)
		}
		mainLogger.Info("Probed AGY CLI capabilities",
			"version", caps.Version,
			"supports_sandbox", caps.SupportsSandbox,
			"supports_project_grants", caps.SupportsProjectScopedGrants,
			"supports_stream_json", caps.SupportsStreamJSON,
			"platform", caps.Platform,
		)
		preflightProjectIDs := make([]string, 0, len(preflightTargets))
		for projectID := range preflightTargets {
			preflightProjectIDs = append(preflightProjectIDs, projectID)
		}
		sort.Strings(preflightProjectIDs)
		for _, projectID := range preflightProjectIDs {
			target := preflightTargets[projectID]
			canaryCtx, canaryCancel := context.WithTimeout(context.Background(), 60*time.Second)
			canaryErr := agy.PreflightCanaryCheck(canaryCtx, cfg.AGY.BinaryPath, target.projectID, target.workspaceDir)
			canaryCancel()
			if canaryErr != nil {
				fmt.Fprintf(os.Stderr, "❌ AGY project security preflight failed for agent %q: %v\n", target.agentName, canaryErr)
				os.Exit(1)
			}
			mainLogger.Info("Verified AGY project grant reaches workspace security hook",
				"agent_name", target.agentName,
				"project_id", target.projectID,
				"workspace", target.workspaceDir,
			)
		}

		// 2. Initialize Central EventBus
		bus := eventbus.NewEventBus(1024, 4)
		defer bus.Close()

		// 3. Initialize LockManager
		lockMgr := concurrency.NewSessionLockManager()

		// 4. Initialize AGY Subprocess Harness
		runner := agy.NewHarness(cfg.AGY, bus)

		// 5. Initialize Channel Multiplexer & Channel Adapters
		channelMux := composite.NewChannelMux()

		// Register Telegram adapter if configured
		if len(cfg.Telegram.GetNormalizedBots()) > 0 && strings.TrimSpace(cfg.Telegram.GetNormalizedBots()[0].BotToken) != "" {
			tgAdapter := telegram.NewAdapter(cfg, bus)
			channelMux.RegisterAdapter(tgAdapter)
			mainLogger.Info("Registered Telegram channel adapter")
		}

		// Register Zalo adapter if configured
		if len(cfg.Zalo.GetNormalizedBots()) > 0 && strings.TrimSpace(cfg.Zalo.GetNormalizedBots()[0].BotToken) != "" {
			zaloAdapter, err := zalo.NewAdapter(cfg, bus)
			if err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to initialize configured Zalo channel adapter: %v\n", err)
				os.Exit(1)
			}
			channelMux.RegisterAdapter(zaloAdapter)
			mainLogger.Info("Registered Zalo channel adapter", "api_url", cfg.Zalo.APIURL)
		}

		channel := channelMux

		// 6. Initialize Universal Security Gateway & IPC Host
		_ = securityAdapter.RemoveGlobalHooks(mainLogger)
		secMgr := securityAdapter.NewManager(cfg.Security, channelMux, mainLogger)
		secMgr.SetEventBus(bus)
		channelMux.SetURLSafetyEvaluator(secMgr)

		ipcTokenBytes := make([]byte, 32)
		if _, err := rand.Read(ipcTokenBytes); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to generate crypto random token for IPC: %v\n", err)
			os.Exit(1)
		}
		ipcAuthToken := hex.EncodeToString(ipcTokenBytes)
		secMgr.SetIPCAuthToken(ipcAuthToken)
		ipcServer := ipc.NewServer(secMgr, "", mainLogger)
		ipcServer.SetAuthToken(ipcAuthToken)

		_ = config.MigrateLegacyWorkspace(cfg.Storage.AgentsDir)
		starterWS := config.ResolveAgentWorkspace(cfg.Storage.AgentsDir, "")
		if _, err := securityAdapter.EnsureWorkspaceHooksProvisioned(starterWS, "", mainLogger); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to provision mandatory security hooks for starter workspace: %v\n", err)
			os.Exit(1)
		}
		if err := securityAdapter.EnsureWorkspaceSettingsProvisioned(starterWS, mainLogger); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to provision mandatory workspace settings for starter workspace: %v\n", err)
			os.Exit(1)
		}

		// 7. Initialize Context Resolver, MCP Syncer & Plugin Manager
		contextResolver := contextAdapter.NewContextResolver()
		mcpSyncer, err := mcp.NewMCPSyncer("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to initialize the mandatory MCP isolation registry: %v\n", err)
			os.Exit(1)
		}
		pluginMgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)
		if syncResults, syncErr := pluginMgr.SyncPlugins(context.Background(), "", false); syncErr == nil {
			for _, r := range syncResults {
				if r.Updated {
					mainLogger.Info("Auto-synced embedded plugin", "name", r.Name, "version", r.EmbeddedVersion)
				}
			}
		}

		// 8. Initialize Debouncer & Core Engine
		var eng *engine.Engine

		debHandler := func(ctx context.Context, msg domain.CanonicalMessage) error {
			return eng.HandleDebouncedMessage(ctx, msg)
		}

		debounceSeconds := cfg.Storage.DebounceSeconds
		if debounceSeconds <= 0 {
			debounceSeconds = 2.0
		}

		deb := debouncer.NewDebouncer(debouncer.Config{
			WindowDuration:    time.Duration(debounceSeconds * float64(time.Second)),
			MaxWaitDuration:   10 * time.Second,
			MaxMessageCount:   20,
			MaxActiveSessions: 5000,
		}, debHandler)
		defer deb.Close(context.Background())

		eng = engine.NewEngine(cfg, store, runner, channel, bus, deb, lockMgr, contextResolver, mcpSyncer, pluginMgr)
		eng.SetTemporalContext(contextAdapter.NewTemporalContext())
		eng.SetSecurityManager(secMgr)
		eng.SetAttachmentFetcher(channelMux)
		wsMgr := workspaceAdapter.NewManager(mainLogger)
		eng.SetWorkspaceManager(wsMgr)

		// Centralized Authorization Policy & Execution Chokepoint
		policyEngine := auth.NewEngine(store, cfg)
		execSvc := execution.NewService(runner, policyEngine, secMgr, store, cfg, mainLogger)
		eng.SetPolicyEngine(policyEngine)
		eng.SetExecutionService(execSvc)
		ipcServer.SetPolicyEngine(policyEngine)
		ipcServer.SetScheduleStore(store)
		channelMux.SetInboundAuthorizer(eng)

		// Initialize Scheduler (Heartbeat, Cron, One-off Schedules)
		sched := scheduler.NewScheduler(cfg, store, wsMgr, runner, bus, mainLogger)
		sched.SetLocation(contextAdapter.DetectUserLocation(starterWS))
		sched.SetExecutionService(execSvc)
		sched.SetSecurityManager(secMgr)
		sched.SetPluginManager(pluginMgr)
		sched.SetMCPRegistry(mcpSyncer)
		eng.SetScheduler(sched)
		ipcServer.SetScheduler(sched)

		subDispatcher := subagent.NewDispatcher(cfg.Subagent, cfg.AGY.BinaryPath, store, bus)
		subDispatcher.SetSecurityManager(secMgr)
		subDispatcher.SetPolicyEngine(policyEngine)
		subDispatcher.SetStoragePort(store)
		subDispatcher.SetExecutionService(execSvc)
		eng.SetSubagentDispatcher(subDispatcher)
		ipcServer.SetSubagents(subDispatcher)

		if cfg.Evolution.Enabled {
			evoOrch := evolutionAdapter.NewEvolutionOrchestrator(cfg, store, runner)
			evoOrch.SetExecutionService(execSvc)
			eng.SetEvolutionOrchestrator(evoOrch)
		}

		// 9. Start Engine and Inbound Listeners
		daemonCtx, cancelDaemon := context.WithCancel(context.Background())
		defer cancelDaemon()

		if err := ipcServer.Start(daemonCtx); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to start mandatory security IPC server: %v\n", err)
			os.Exit(1)
		}

		if err := eng.Start(daemonCtx); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to start agyent engine: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("🟢 agyent gateway daemon is active and listening for messages.")
		fmt.Println("   Press Ctrl+C to stop.")
		fmt.Println()

		// 9. Trap OS Signals for Graceful Shutdown (< 3.0s)
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

		sig := <-sigChan
		fmt.Printf("\n🛑 Received signal %v. Initiating graceful shutdown (< 3.0s)...\n", sig)

		// ARCH-03: Immediately halt channel inbound ingestion (polling / webhooks)
		cancelDaemon()

		shutdownStart := time.Now()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2800*time.Millisecond)
		defer cancelShutdown()

		// Step 1: Stop Security IPC Server (prevents incoming IPC requests panicking on closed SQLite store)
		fmt.Print("   [1/6] Stopping Security IPC Server... ")
		if err := ipcServer.Stop(); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 2: Flush In-flight Debouncer buffers into the active engine
		fmt.Print("   [2/6] Flushing Message Debouncer buffers... ")
		if err := deb.Close(shutdownCtx); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 3: Stop Engine & Cancel/Drain Active Turns and Subagents
		fmt.Print("   [3/6] Stopping Orchestration Engine & Active Turns... ")
		if err := eng.Stop(shutdownCtx); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 4: Drain and Stop Channel Adapters & Delivery Throttlers (allows buffered terminal messages to send)
		fmt.Print("   [4/6] Stopping Channel Adapters & Delivery Throttlers... ")
		if err := channel.Stop(); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 5: Drain EventBus Async Queue
		fmt.Print("   [5/6] Draining EventBus queues... ")
		if err := bus.CloseWithTimeout(shutdownCtx); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 6: Close SQLite Database Connection
		fmt.Print("   [6/6] Closing SQLite Database... ")
		if err := store.Close(); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		elapsed := time.Since(shutdownStart)
		fmt.Printf("🎉 Graceful shutdown completed cleanly in %v.\n", elapsed)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
