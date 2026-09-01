package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
	workspaceAdapter "agyent/internal/adapters/workspace"
	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/debouncer"
	"agyent/internal/core/domain"
	"agyent/internal/core/engine"
	"agyent/internal/core/eventbus"
	"agyent/internal/core/subagent"
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
			if zaloAdapter, err := zalo.NewAdapter(cfg, bus); err == nil {
				channelMux.RegisterAdapter(zaloAdapter)
				mainLogger.Info("Registered Zalo channel adapter", "api_url", cfg.Zalo.APIURL)
			} else {
				mainLogger.Warn("Failed to initialize Zalo channel adapter", "error", err)
			}
		}

		channel := channelMux

		// 6. Initialize Universal Security Gateway & IPC Host
		_ = securityAdapter.RemoveGlobalHooks(mainLogger)
		secMgr := securityAdapter.NewManager(cfg.Security, channelMux, mainLogger)
		secMgr.SetEventBus(bus)
		ipcServer := ipc.NewServer(secMgr, "", mainLogger)

		_ = config.MigrateLegacyWorkspace(cfg.Storage.AgentsDir)
		starterWS := config.ResolveAgentWorkspace(cfg.Storage.AgentsDir, "")
		if _, err := securityAdapter.EnsureWorkspaceHooksProvisioned(starterWS, "", mainLogger); err != nil {
			mainLogger.Debug("Provisioned starter workspace hooks", "workspace", starterWS, "error", err)
		}

		// 7. Initialize Context Resolver, MCP Syncer & Plugin Manager
		contextResolver := contextAdapter.NewContextResolver()
		mcpSyncer, err := mcp.NewMCPSyncer("")
		if err != nil {
			mainLogger.Warn("Failed to init MCP syncer", "error", err)
			fmt.Fprintf(os.Stderr, "⚠️ Warning: Failed to init MCP syncer: %v\n", err)
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
		eng.SetWorkspaceManager(workspaceAdapter.NewManager(mainLogger))

		subDispatcher := subagent.NewDispatcher(cfg.Subagent, cfg.AGY.BinaryPath, store, bus)
		eng.SetSubagentDispatcher(subDispatcher)

		if cfg.Evolution.Enabled {
			evoOrch := evolutionAdapter.NewEvolutionOrchestrator(cfg, store, runner)
			eng.SetEvolutionOrchestrator(evoOrch)
		}

		// 9. Start Engine and Inbound Listeners
		daemonCtx, cancelDaemon := context.WithCancel(context.Background())
		defer cancelDaemon()

		if err := ipcServer.Start(daemonCtx); err != nil {
			mainLogger.Warn("Failed to start Security IPC server", "error", err)
		}
		defer ipcServer.Stop()

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

		shutdownStart := time.Now()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 2800*time.Millisecond)
		defer cancelShutdown()

		// Step 1: Halt Channel Inbound & Drain Throttlers
		fmt.Print("   [1/5] Stopping Telegram Channel Adapter... ")
		if err := channel.Stop(); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 2: Flush In-flight Debouncer buffers
		fmt.Print("   [2/5] Flushing Message Debouncer buffers... ")
		if err := deb.Close(shutdownCtx); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 3: Stop Engine & Cancel Subprocesses
		fmt.Print("   [3/5] Stopping Orchestration Engine... ")
		if err := eng.Stop(shutdownCtx); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 4: Drain EventBus Async Queue
		fmt.Print("   [4/5] Draining EventBus queues... ")
		if err := bus.CloseWithTimeout(shutdownCtx); err != nil {
			fmt.Printf("⚠️ %v\n", err)
		} else {
			fmt.Println("✅ done.")
		}

		// Step 5: Close SQLite Database Connection
		fmt.Print("   [5/5] Closing SQLite Database... ")
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
