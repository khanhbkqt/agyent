package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"agyent/internal/wizard"

	"github.com/spf13/cobra"
)

var (
	nonInteractive      bool
	tokenFlag           string
	adminFlag           string
	zaloTokenFlag       string
	zaloGroupFlag       string
	zaloAdminFlag       string
	agentsDirFlag       string
	agyPathFlag         string
	debounceFlag        float64
	createDefaultAgent  bool
	securityPresetFlag  string
	approvalTimeoutFlag int
	pluginsFlag         string
	enableAllPlugins    bool
	skipPlugins         bool

	initCmd = &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration and workspace for agyent",
		Long:  `Run an interactive wizard to configure Telegram and Zalo bot credentials, security posture, plugins, workspace paths, and agy CLI integration.`,
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()

			var adminIDs []int64
			if adminFlag != "" {
				var err error
				adminIDs, err = wizard.ParseAdminIDs(adminFlag)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error parsing admin IDs: %v\n", err)
					os.Exit(1)
				}
			}

			var zaloAdmins []string
			if zaloAdminFlag != "" {
				parts := strings.Split(zaloAdminFlag, ",")
				for _, p := range parts {
					if trimmed := strings.TrimSpace(p); trimmed != "" {
						zaloAdmins = append(zaloAdmins, trimmed)
					}
				}
			}

			var pluginsList []string
			if pluginsFlag != "" {
				parts := strings.Split(pluginsFlag, ",")
				for _, p := range parts {
					if trimmed := strings.TrimSpace(p); trimmed != "" {
						pluginsList = append(pluginsList, trimmed)
					}
				}
			}

			opts := wizard.WizardOptions{
				NonInteractive:     nonInteractive,
				ConfigPath:         cfgFile,
				BotToken:           tokenFlag,
				AdminUserIDs:       adminIDs,
				ZaloBotToken:       zaloTokenFlag,
				ZaloGroupID:        zaloGroupFlag,
				ZaloAdminUserIDs:   zaloAdmins,
				AgentsDir:          agentsDirFlag,
				AGYPath:            agyPathFlag,
				DebounceSeconds:    debounceFlag,
				CreateDefaultAgent: createDefaultAgent,
				SecurityPreset:     securityPresetFlag,
				ApprovalTimeout:    approvalTimeoutFlag,
				Plugins:            pluginsList,
				EnableAllPlugins:   enableAllPlugins,
				SkipPlugins:        skipPlugins,
			}

			_, err := wizard.RunWizard(ctx, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Initialization failed: %v\n", err)
				os.Exit(1)
			}
		},
	}
)

func init() {
	initCmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Run in non-interactive mode using flags and defaults")
	initCmd.Flags().StringVar(&tokenFlag, "token", "", "Telegram Bot Token (for non-interactive setup)")
	initCmd.Flags().StringVar(&adminFlag, "admin", "", "Telegram Admin User ID(s), comma-separated (for non-interactive setup)")
	initCmd.Flags().StringVar(&zaloTokenFlag, "zalo-token", "", "Zalo Bot Token (for non-interactive setup)")
	initCmd.Flags().StringVar(&zaloGroupFlag, "zalo-group", "", "Target Zalo Group Chat ID (for non-interactive setup)")
	initCmd.Flags().StringVar(&zaloAdminFlag, "zalo-admin", "", "Zalo Admin User ID(s), comma-separated (for non-interactive setup)")
	initCmd.Flags().StringVar(&agentsDirFlag, "agents-dir", "", "Agent Workspaces directory path")
	initCmd.Flags().StringVar(&agyPathFlag, "agy-path", "", "Path to AGY CLI binary")
	initCmd.Flags().Float64Var(&debounceFlag, "debounce", 2.0, "Message debounce duration in seconds")
	initCmd.Flags().BoolVar(&createDefaultAgent, "create-default-agent", true, "Automatically create starter 'agyent' agent")
	initCmd.Flags().StringVar(&securityPresetFlag, "security-preset", "balanced", "Default security preset ('unrestricted', 'developer', 'balanced', 'strict', 'read_only')")
	initCmd.Flags().IntVar(&approvalTimeoutFlag, "approval-timeout", 60, "Security HITL approval timeout in seconds")
	initCmd.Flags().StringVar(&pluginsFlag, "plugins", "", "Comma-separated list of capability plugins to install (e.g. 'browser-camoufox,database-sqlite')")
	initCmd.Flags().BoolVar(&enableAllPlugins, "enable-all-plugins", false, "Install and enable all builtin capability plugins")
	initCmd.Flags().BoolVar(&skipPlugins, "skip-plugins", false, "Skip installing capability plugins during setup")

	rootCmd.AddCommand(initCmd)
}
