package doctor

import (
	"fmt"
	"os"
	"strings"

	"agyent/internal/config"
)

// CheckConfiguration validates configuration file existence, syntax, and business constraints.
func (d *DoctorRunner) CheckConfiguration() ([]CheckResult, *config.Config) {
	var results []CheckResult

	expandedConfigPath, err := config.ExpandPath(d.opts.ConfigPath)
	if err != nil {
		results = append(results, CheckResult{
			Name:        "Config Path Expansion",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to expand configuration path %q: %v", d.opts.ConfigPath, err),
			Remediation: "Check configuration path syntax.",
		})
		return results, nil
	}

	// 1. Config File Existence
	if _, err := os.Stat(expandedConfigPath); os.IsNotExist(err) {
		results = append(results, CheckResult{
			Name:        "Configuration File Existence",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Configuration file not found at %s", expandedConfigPath),
			Remediation: "Run 'agyent init' to create initial configuration interactively, or copy example config.",
			CanAutoFix:  true,
		})
		return results, nil
	}

	results = append(results, CheckResult{
		Name:     "Configuration File Existence",
		Category: CategoryConfig,
		Status:   StatusPass,
		Message:  fmt.Sprintf("Found configuration file at %s", expandedConfigPath),
	})

	// 2. Load and parse YAML
	cfg, err := config.Load(d.opts.ConfigPath)
	if err != nil {
		results = append(results, CheckResult{
			Name:        "YAML Syntax & Parsing",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to parse configuration YAML: %v", err),
			Remediation: "Fix syntax errors in YAML file or run 'agyent init --non-interactive' to re-generate.",
		})
		return results, nil
	}

	results = append(results, CheckResult{
		Name:     "YAML Syntax & Parsing",
		Category: CategoryConfig,
		Status:   StatusPass,
		Message:  "Configuration YAML parsed successfully",
	})

	// 3. Structural Validation
	if err := cfg.Validate(); err != nil {
		results = append(results, CheckResult{
			Name:        "Configuration Schema Validation",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Configuration validation failed: %v", err),
			Remediation: "Check missing or invalid fields in config file.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "Configuration Schema Validation",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  "All required configuration constraints satisfied",
		})
	}

	// 4. Admin Whitelist Check
	if len(cfg.Telegram.AdminUserIDs) == 0 {
		results = append(results, CheckResult{
			Name:        "Telegram Admin Whitelist",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     "No admin user IDs configured (admin_user_ids is empty)",
			Remediation: "Specify at least one numeric Telegram User ID under 'telegram.admin_user_ids'.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "Telegram Admin Whitelist",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  fmt.Sprintf("%d admin user ID(s) configured: %v", len(cfg.Telegram.AdminUserIDs), cfg.Telegram.AdminUserIDs),
		})
	}

	// 5. Bot Token Count & Mode
	bots := cfg.Telegram.GetNormalizedBots()
	if len(bots) == 0 {
		results = append(results, CheckResult{
			Name:        "Telegram Bot Configuration",
			Category:    CategoryConfig,
			Status:      StatusFail,
			Message:     "No Telegram bot token configured",
			Remediation: "Add bot token from @BotFather in config under 'telegram.bot_token' or 'telegram.bots'.",
		})
	} else {
		var botNames []string
		for _, b := range bots {
			botNames = append(botNames, b.Name)
		}
		results = append(results, CheckResult{
			Name:     "Telegram Bot Configuration",
			Category: CategoryConfig,
			Status:   StatusPass,
			Message:  fmt.Sprintf("%d bot instance(s) configured [%s] (Mode: %s)", len(bots), strings.Join(botNames, ", "), cfg.Telegram.Mode),
		})
	}

	return results, cfg
}
