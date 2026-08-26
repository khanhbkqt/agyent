package main

import (
	"context"
	"fmt"
	"os"

	"agyent/internal/wizard"

	"github.com/spf13/cobra"
)

var (
	nonInteractive     bool
	tokenFlag          string
	adminFlag          string
	agentsDirFlag      string
	agyPathFlag        string
	debounceFlag       float64
	createDefaultAgent bool

	initCmd = &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration and workspace for agyent",
		Long:  `Run an interactive wizard to configure Telegram bot credentials, admin IDs, workspace paths, and agy CLI integration.`,
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

			opts := wizard.WizardOptions{
				NonInteractive:     nonInteractive,
				ConfigPath:         cfgFile,
				BotToken:           tokenFlag,
				AdminUserIDs:       adminIDs,
				AgentsDir:          agentsDirFlag,
				AGYPath:            agyPathFlag,
				DebounceSeconds:    debounceFlag,
				CreateDefaultAgent: createDefaultAgent,
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
	initCmd.Flags().StringVar(&agentsDirFlag, "agents-dir", "", "Agent Workspaces directory path")
	initCmd.Flags().StringVar(&agyPathFlag, "agy-path", "", "Path to AGY CLI binary")
	initCmd.Flags().Float64Var(&debounceFlag, "debounce", 2.0, "Message debounce duration in seconds")
	initCmd.Flags().BoolVar(&createDefaultAgent, "create-default-agent", true, "Automatically create starter 'agyent' agent")

	rootCmd.AddCommand(initCmd)
}
