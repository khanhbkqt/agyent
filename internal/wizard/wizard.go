package wizard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agyent/builtin"
	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginBottom(1)

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	warningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500"))

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF4040"))

	labelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#43BF6D"))

	telegramTokenRegex = regexp.MustCompile(`^\d{6,15}:[A-Za-z0-9_-]{30,50}$`)
)

// WizardOptions contains options and presets for running the setup wizard.
type WizardOptions struct {
	NonInteractive     bool
	ConfigPath         string
	DBPath             string
	BotToken           string
	AdminUserIDs       []int64
	ZaloBotToken       string
	ZaloGroupID        string
	ZaloAdminUserIDs   []string
	AgentsDir          string
	AGYPath            string
	DebounceSeconds    float64
	CreateDefaultAgent bool
	TelegramAPIBaseURL string   // Defaults to https://api.telegram.org if empty
	SecurityPreset     string   // "unrestricted", "developer", "balanced", "strict", "read_only"
	ApprovalTimeout    int      // HITL approval timeout in seconds (default: 60)
	Plugins            []string // Builtin plugins to install/enable
	EnableAllPlugins   bool     // Install & enable all builtin capability plugins
	SkipPlugins        bool     // Skip plugin installation
}

// TelegramGetMeResponse models the response from Telegram getMe endpoint.
type TelegramGetMeResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
	Result      struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"result"`
}

// ValidateTokenFormat checks if the string matches typical Telegram Bot token format.
func ValidateTokenFormat(token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("bot token cannot be empty")
	}
	if !telegramTokenRegex.MatchString(token) {
		return errors.New("invalid Telegram bot token format (expected format: 123456789:ABCdefGHIjklMNOpqrSTUvwxYZ)")
	}
	return nil
}

// VerifyTelegramToken sends a getMe HTTP request with timeout to verify the bot token.
func VerifyTelegramToken(ctx context.Context, apiBaseURL, token string) (*TelegramGetMeResponse, error) {
	if apiBaseURL == "" {
		apiBaseURL = "https://api.telegram.org"
	}
	apiBaseURL = strings.TrimRight(apiBaseURL, "/")

	reqURL := fmt.Sprintf("%s/bot%s/getMe", apiBaseURL, token)

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("request to Telegram API timed out after 5s")
		}
		return nil, fmt.Errorf("network error verifying token: %w", err)
	}
	defer resp.Body.Close()

	var result TelegramGetMeResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("telegram API error: %s", result.Description)
	}

	return &result, nil
}

// ParseAdminIDs converts comma-separated or whitespace-separated string to int64 slice.
func ParseAdminIDs(input string) ([]int64, error) {
	parts := strings.FieldsFunc(input, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})

	if len(parts) == 0 {
		return nil, errors.New("at least one Telegram Admin User ID is required")
	}

	var ids []int64
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid user ID %q: must be a positive integer", p)
		}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		return nil, errors.New("at least one valid Telegram Admin User ID is required")
	}

	return ids, nil
}

// RunWizard orchestrates the interactive or non-interactive initial configuration of agyent.
func RunWizard(ctx context.Context, opts WizardOptions) (*config.Config, error) {
	cfg := config.DefaultConfig()

	if opts.ConfigPath == "" {
		opts.ConfigPath = "~/.agyent/config.yaml"
	}

	if opts.NonInteractive {
		return runNonInteractive(ctx, cfg, opts)
	}

	return runInteractive(ctx, cfg, opts)
}

func runNonInteractive(ctx context.Context, cfg *config.Config, opts WizardOptions) (*config.Config, error) {
	if opts.BotToken != "" {
		cfg.Telegram.BotToken = opts.BotToken
	}
	if len(opts.AdminUserIDs) > 0 {
		cfg.Telegram.AdminUserIDs = opts.AdminUserIDs
	}
	if opts.ZaloBotToken != "" {
		cfg.Zalo.BotToken = opts.ZaloBotToken
	}
	if opts.ZaloGroupID != "" {
		cfg.Zalo.GroupID = opts.ZaloGroupID
	}
	if len(opts.ZaloAdminUserIDs) > 0 {
		cfg.Zalo.AdminUserIDs = opts.ZaloAdminUserIDs
	}
	if opts.AgentsDir != "" {
		cfg.Storage.AgentsDir = opts.AgentsDir
	}
	if opts.DBPath != "" {
		cfg.Storage.DBPath = opts.DBPath
	}
	if opts.AGYPath != "" {
		cfg.AGY.BinaryPath = opts.AGYPath
	}
	if opts.DebounceSeconds > 0 {
		cfg.Storage.DebounceSeconds = opts.DebounceSeconds
	}

	// Security Preset Configuration
	secPreset := "balanced"
	if opts.SecurityPreset != "" {
		secPreset = strings.ToLower(strings.TrimSpace(opts.SecurityPreset))
	}
	cfg.Security = config.GetEffectiveSecurityPreset(secPreset)
	if opts.ApprovalTimeout > 0 {
		cfg.Security.ApprovalTimeoutSeconds = opts.ApprovalTimeout
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]config.AgentProfileConfig)
	}
	cfg.Agents["agyent"] = config.AgentProfileConfig{
		SecurityPreset: secPreset,
	}

	// Expand paths
	if err := cfg.ExpandPaths(); err != nil {
		return nil, fmt.Errorf("failed to expand paths: %w", err)
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Save configuration
	if err := config.Save(opts.ConfigPath, cfg); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	// Setup plugins if requested
	if !opts.SkipPlugins {
		pluginsToInstall := opts.Plugins
		if opts.EnableAllPlugins || (len(pluginsToInstall) == 0 && !opts.SkipPlugins) {
			pluginsToInstall = []string{"browser-camoufox", "database-sqlite", "subagent-dispatcher", "system-diagnostics"}
		}
		home, _ := os.UserHomeDir()
		globalPluginsDir := filepath.Join(home, ".agyent", "plugins")
		_, _ = SetupPlugins(ctx, pluginsToInstall, globalPluginsDir)
	}

	// Create directories & default agent if requested
	if err := setupDirectoriesAndAgent(ctx, cfg, opts.CreateDefaultAgent, secPreset); err != nil {
		return nil, err
	}

	return cfg, nil
}

func runInteractive(ctx context.Context, cfg *config.Config, opts WizardOptions) (*config.Config, error) {
	fmt.Println(titleStyle.Render("🚀 Welcome to the agyent Setup Wizard!"))
	fmt.Println("This wizard will guide you through setting up your Telegram bot gateway, security posture, and capability plugins.")
	fmt.Println()

	var (
		botTokenInput        = opts.BotToken
		adminIDsInput        = ""
		agentsDirInput       = cfg.Storage.AgentsDir
		agyBinaryInput       = cfg.AGY.BinaryPath
		debounceInput        = fmt.Sprintf("%.1f", cfg.Storage.DebounceSeconds)
		securityPresetInput  = "balanced"
		approvalTimeoutInput = "60"
		selectedPlugins      = []string{"browser-camoufox", "database-sqlite", "subagent-dispatcher", "system-diagnostics"}
		createDefaultAgent   = true
	)

	if opts.SecurityPreset != "" {
		securityPresetInput = strings.ToLower(strings.TrimSpace(opts.SecurityPreset))
	}
	if opts.ApprovalTimeout > 0 {
		approvalTimeoutInput = strconv.Itoa(opts.ApprovalTimeout)
	}
	if len(opts.Plugins) > 0 {
		selectedPlugins = opts.Plugins
	}

	if len(opts.AdminUserIDs) > 0 {
		var parts []string
		for _, id := range opts.AdminUserIDs {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		adminIDsInput = strings.Join(parts, ", ")
	}

	form := huh.NewForm(
		// Step 1: Telegram Gateway Setup
		huh.NewGroup(
			huh.NewInput().
				Title("Telegram Bot Token").
				Description("Enter the bot token provided by @BotFather").
				EchoMode(huh.EchoModePassword).
				Value(&botTokenInput).
				Validate(func(s string) error {
					return ValidateTokenFormat(s)
				}),

			huh.NewInput().
				Title("Telegram Admin User ID(s)").
				Description("Enter your Telegram numeric user ID(s) (comma-separated)").
				Value(&adminIDsInput).
				Validate(func(s string) error {
					_, err := ParseAdminIDs(s)
					return err
				}),
		).Title("Step 1: Telegram Gateway Setup"),

		// Step 2: Storage & AGY CLI Settings
		huh.NewGroup(
			huh.NewInput().
				Title("Agent Workspaces Directory").
				Description("Directory where agent profiles and workspaces will be stored").
				Value(&agentsDirInput),

			huh.NewInput().
				Title("AGY CLI Binary Path").
				Description("Command or path to Antigravity CLI binary").
				Value(&agyBinaryInput),

			huh.NewInput().
				Title("Debounce Duration (seconds)").
				Description("Time window to buffer consecutive messages").
				Value(&debounceInput).
				Validate(func(s string) error {
					v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
					if err != nil || v < 0 {
						return errors.New("must be a non-negative number")
					}
					return nil
				}),
		).Title("Step 2: Workspace & CLI Integration"),

		// Step 3: Security Posture & Guardrails
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Default Security Preset").
				Description("Select default autonomous security posture for your agent(s)").
				Options(
					huh.NewOption("Balanced (Recommended: Standard workspace jail, HITL for shell & outer files)", "balanced"),
					huh.NewOption("Developer (High Autonomy: Full dev access inside workspace, HITL for critical system changes)", "developer"),
					huh.NewOption("Unrestricted (Full Autonomy: Zero restrictions, no HITL - for private workstations)", "unrestricted"),
					huh.NewOption("Strict (High Security: Whitelist-only command execution, enforced jail)", "strict"),
					huh.NewOption("Read Only (Exploration only: Zero file modification or shell execution)", "read_only"),
				).
				Value(&securityPresetInput),

			huh.NewInput().
				Title("HITL Approval Timeout (seconds)").
				Description("Time to wait for Telegram approval before auto-rejecting sensitive actions").
				Value(&approvalTimeoutInput).
				Validate(func(s string) error {
					v, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || v < 5 {
						return errors.New("must be a positive integer >= 5 seconds")
					}
					return nil
				}),
		).Title("Step 3: Security Posture & Guardrails"),

		// Step 4: Capability Plugins & Default Agent
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Built-in Capability Plugins").
				Description("Select plugins to automatically install and enable in ~/.agyent/plugins/").
				Options(
					huh.NewOption("browser-camoufox (Anti-detect web browsing & DOM/visual extraction)", "browser-camoufox"),
					huh.NewOption("database-sqlite (SQLite database schema inspection & query execution)", "database-sqlite"),
					huh.NewOption("subagent-dispatcher (Asynchronous background subagent task dispatcher)", "subagent-dispatcher"),
					huh.NewOption("system-diagnostics (System resource metrics, process inspection & disk I/O)", "system-diagnostics"),
				).
				Value(&selectedPlugins),

			huh.NewConfirm().
				Title("Create default agent ('agyent')?").
				Description("Initializes a starter personal AI assistant profile with your chosen security preset").
				Value(&createDefaultAgent),
		).Title("Step 4: Capability Plugins & Starter Agent"),
	)

	err := form.Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Println(warningStyle.Render("\nSetup wizard was aborted by user."))
			return nil, err
		}
		return nil, fmt.Errorf("form error: %w", err)
	}

	adminIDs, _ := ParseAdminIDs(adminIDsInput)
	debounceVal, _ := strconv.ParseFloat(strings.TrimSpace(debounceInput), 64)
	timeoutVal, _ := strconv.Atoi(strings.TrimSpace(approvalTimeoutInput))

	cfg.Telegram.BotToken = strings.TrimSpace(botTokenInput)
	cfg.Telegram.AdminUserIDs = adminIDs
	cfg.Storage.AgentsDir = strings.TrimSpace(agentsDirInput)
	cfg.AGY.BinaryPath = strings.TrimSpace(agyBinaryInput)
	cfg.Storage.DebounceSeconds = debounceVal

	// Configure security preset and effective sub-configurations
	cfg.Security = config.GetEffectiveSecurityPreset(securityPresetInput)
	if timeoutVal > 0 {
		cfg.Security.ApprovalTimeoutSeconds = timeoutVal
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]config.AgentProfileConfig)
	}
	cfg.Agents["agyent"] = config.AgentProfileConfig{
		SecurityPreset: securityPresetInput,
	}

	fmt.Println()

	// 1. Verify Telegram Token
	fmt.Print("Verifying Telegram Bot Token... ")
	botInfo, err := VerifyTelegramToken(ctx, opts.TelegramAPIBaseURL, cfg.Telegram.BotToken)
	if err != nil {
		fmt.Println(warningStyle.Render(fmt.Sprintf("⚠️  Warning: could not verify bot token: %v", err)))
	} else {
		fmt.Println(successStyle.Render(fmt.Sprintf("✅ Valid bot token! (@%s)", botInfo.Result.Username)))
	}

	// 2. Check AGY Binary
	fmt.Print("Checking AGY CLI binary... ")
	if resolvedPath, err := exec.LookPath(cfg.AGY.BinaryPath); err == nil {
		fmt.Println(successStyle.Render(fmt.Sprintf("✅ Found at %s", resolvedPath)))
	} else {
		fmt.Println(warningStyle.Render(fmt.Sprintf("⚠️  Warning: %q not found in PATH. Make sure AGY CLI is installed before running.", cfg.AGY.BinaryPath)))
	}

	// 3. Expand and Save Config
	if err := cfg.ExpandPaths(); err != nil {
		return nil, fmt.Errorf("failed to expand paths: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	if err := config.Save(opts.ConfigPath, cfg); err != nil {
		return nil, fmt.Errorf("failed to save config to %s: %w", opts.ConfigPath, err)
	}
	fmt.Println(successStyle.Render(fmt.Sprintf("✅ Configuration saved to: %s", opts.ConfigPath)))

	// 4. Install Selected Capability Plugins
	if len(selectedPlugins) > 0 {
		home, _ := os.UserHomeDir()
		globalPluginsDir := filepath.Join(home, ".agyent", "plugins")
		installedPlugins, pluginErr := SetupPlugins(ctx, selectedPlugins, globalPluginsDir)
		if pluginErr == nil && len(installedPlugins) > 0 {
			fmt.Println(successStyle.Render(fmt.Sprintf("✅ Enabled plugins: %s", strings.Join(installedPlugins, ", "))))
		}
	}

	// 5. Setup directories and agent
	if err := setupDirectoriesAndAgent(ctx, cfg, createDefaultAgent, securityPresetInput); err != nil {
		return nil, err
	}

	fmt.Println()
	fmt.Println(titleStyle.Render("🎉 agyent initialization complete!"))
	fmt.Println("Setup Summary:")
	fmt.Printf("  %s %s\n", labelStyle.Render("Security Preset :"), securityPresetInput)
	fmt.Printf("  %s %d seconds\n", labelStyle.Render("HITL Timeout    :"), cfg.Security.ApprovalTimeoutSeconds)
	fmt.Printf("  %s %s\n", labelStyle.Render("Config Path     :"), opts.ConfigPath)
	fmt.Printf("  %s %s\n", labelStyle.Render("Database Path   :"), cfg.Storage.DBPath)
	fmt.Println("\nYou can now start the gateway daemon with:")
	fmt.Println("  agyent run")
	fmt.Println()

	return cfg, nil
}

// SetupPlugins extracts and enables the specified builtin plugins into the target plugins directory.
func SetupPlugins(ctx context.Context, plugins []string, globalPluginsDir string) ([]string, error) {
	if len(plugins) == 0 {
		return nil, nil
	}

	if globalPluginsDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		globalPluginsDir = filepath.Join(home, ".agyent", "plugins")
	}

	if err := os.MkdirAll(globalPluginsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create plugins directory %s: %w", globalPluginsDir, err)
	}

	mgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)
	var installed []string

	for _, pName := range plugins {
		pName = strings.TrimSpace(pName)
		if pName == "" {
			continue
		}

		res, err := mgr.ExtractPluginAtomic(ctx, pName, globalPluginsDir, true)
		if err == nil && res != nil {
			installed = append(installed, pName)
			_ = mgr.TogglePlugin(ctx, pName, true, domain.ScopeGlobal, "")
		}
	}

	return installed, nil
}

func setupDirectoriesAndAgent(ctx context.Context, cfg *config.Config, createAgent bool, securityPreset string) error {
	// 1. Create agents directory
	if err := os.MkdirAll(cfg.Storage.AgentsDir, 0755); err != nil {
		return fmt.Errorf("failed to create agents directory %s: %w", cfg.Storage.AgentsDir, err)
	}

	// 2. Initialize SQLite database & migrations
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("failed to initialize sqlite database at %s: %w", cfg.Storage.DBPath, err)
	}
	defer store.Close()

	// 3. Seed initial admin users into whitelist
	for _, adminID := range cfg.Telegram.AdminUserIDs {
		user := &domain.User{
			ID:        strconv.FormatInt(adminID, 10),
			Role:      "admin",
			CreatedAt: time.Now(),
		}
		if err := store.SaveUser(ctx, user); err != nil {
			return fmt.Errorf("failed to seed admin user %d: %w", adminID, err)
		}
	}

	// 4. Create default agent if specified
	if createAgent {
		agentPath := config.ResolveAgentWorkspace(cfg.Storage.AgentsDir, "agyent")
		if err := os.MkdirAll(agentPath, 0755); err != nil {
			return fmt.Errorf("failed to create default agent dir %s: %w", agentPath, err)
		}

		if securityPreset == "" {
			securityPreset = string(domain.PresetBalanced)
		}

		// Register agent in database in uninitialized state (bootstrap onboarding on first message)
		agent := &domain.Agent{
			Name:           "agyent",
			Description:    "Agyent - Autonomous All-in-One Personal AI Assistant & Pair Programmer",
			Status:         domain.StatusUninitialized,
			WorkspacePath:  agentPath,
			SecurityPreset: domain.SecurityPreset(securityPreset),
			CreatedAt:      time.Now(),
			UpdatedAt:      time.Now(),
		}
		if err := store.SaveAgent(ctx, agent); err != nil {
			return fmt.Errorf("failed to register default agent in database: %w", err)
		}
	}

	return nil
}
