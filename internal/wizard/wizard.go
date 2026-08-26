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
	"regexp"
	"strconv"
	"strings"
	"time"

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

	telegramTokenRegex = regexp.MustCompile(`^\d{6,15}:[A-Za-z0-9_-]{30,50}$`)
)

// WizardOptions contains options and presets for running the setup wizard.
type WizardOptions struct {
	NonInteractive     bool
	ConfigPath         string
	DBPath             string
	BotToken           string
	AdminUserIDs       []int64
	AgentsDir          string
	AGYPath            string
	DebounceSeconds    float64
	CreateDefaultAgent bool
	TelegramAPIBaseURL string // Defaults to https://api.telegram.org if empty
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

	// Create directories & default agent if requested
	if err := setupDirectoriesAndAgent(ctx, cfg, opts.CreateDefaultAgent); err != nil {
		return nil, err
	}

	return cfg, nil
}

func runInteractive(ctx context.Context, cfg *config.Config, opts WizardOptions) (*config.Config, error) {
	fmt.Println(titleStyle.Render("🚀 Welcome to the agyent Setup Wizard!"))
	fmt.Println("This wizard will help you configure your agyent daemon.")
	fmt.Println()

	var (
		botTokenInput      = opts.BotToken
		adminIDsInput      = ""
		agentsDirInput     = cfg.Storage.AgentsDir
		agyBinaryInput     = cfg.AGY.BinaryPath
		debounceInput      = fmt.Sprintf("%.1f", cfg.Storage.DebounceSeconds)
		createDefaultAgent = true
	)

	if len(opts.AdminUserIDs) > 0 {
		var parts []string
		for _, id := range opts.AdminUserIDs {
			parts = append(parts, strconv.FormatInt(id, 10))
		}
		adminIDsInput = strings.Join(parts, ", ")
	}

	form := huh.NewForm(
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

			huh.NewConfirm().
				Title("Create default agent ('agyent')?").
				Description("Initializes a starter personal AI assistant profile").
				Value(&createDefaultAgent),
		),
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

	cfg.Telegram.BotToken = strings.TrimSpace(botTokenInput)
	cfg.Telegram.AdminUserIDs = adminIDs
	cfg.Storage.AgentsDir = strings.TrimSpace(agentsDirInput)
	cfg.AGY.BinaryPath = strings.TrimSpace(agyBinaryInput)
	cfg.Storage.DebounceSeconds = debounceVal

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

	// 4. Setup directories and agent
	if err := setupDirectoriesAndAgent(ctx, cfg, createDefaultAgent); err != nil {
		return nil, err
	}

	fmt.Println()
	fmt.Println(successStyle.Render("🎉 agyent initialization complete!"))
	fmt.Println("You can now start the gateway daemon with:")
	fmt.Println("  agyent run")
	fmt.Println()

	return cfg, nil
}

func setupDirectoriesAndAgent(ctx context.Context, cfg *config.Config, createAgent bool) error {
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

		// Register agent in database in uninitialized state (bootstrap onboarding on first message)
		agent := &domain.Agent{
			Name:          "agyent",
			Description:   "Agyent - Autonomous All-in-One Personal AI Assistant & Pair Programmer",
			Status:        domain.StatusUninitialized,
			WorkspacePath: agentPath,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		if err := store.SaveAgent(ctx, agent); err != nil {
			return fmt.Errorf("failed to register default agent in database: %w", err)
		}
	}

	return nil
}
