package main

import (
	"context"
	"fmt"

	"agyent/internal/doctor"
	"github.com/spf13/cobra"
)

var (
	doctorFixFlag         bool
	doctorJSONFlag        bool
	doctorSessionFlag     string
	doctorSkipNetworkFlag bool
	doctorLiveProbeFlag   bool

	doctorCmd = &cobra.Command{
		Use:     "doctor",
		Aliases: []string{"docter", "doc", "health", "diag"},
		Short:   "Diagnose system health, AGY CLI auth/quota, SQLite DB, and Telegram connectivity",
		Long: `Runs a comprehensive battery of automated diagnostic checks across host environment, 
configuration, Antigravity (AGY) CLI authentication and quota, SQLite database integrity, 
Telegram bot connectivity, security gateway presets, and session locks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			targetSession := doctorSessionFlag
			if targetSession == "" && len(args) > 0 {
				targetSession = args[0]
			}

			opts := doctor.Options{
				ConfigPath:    cfgFile,
				TargetSession: targetSession,
				SkipNetwork:   doctorSkipNetworkFlag,
				LiveProbe:     doctorLiveProbeFlag,
				AutoFix:       doctorFixFlag,
			}

			runner := doctor.NewRunner(opts)
			report, err := runner.Run(ctx)
			if err != nil {
				return fmt.Errorf("doctor failed to complete diagnostic battery: %w", err)
			}

			if doctorJSONFlag {
				if err := report.RenderJSON(cmd.OutOrStdout()); err != nil {
					return fmt.Errorf("error rendering JSON report: %w", err)
				}
			} else {
				report.RenderText(cmd.OutOrStdout())
			}

			if report.FailCount > 0 {
				return fmt.Errorf("diagnostic checks reported %d critical failure(s)", report.FailCount)
			}

			return nil
		},
	}
)

func init() {
	doctorCmd.Flags().BoolVar(&doctorFixFlag, "fix", false, "Automatically attempt to repair fixable directory and database issues")
	doctorCmd.Flags().BoolVar(&doctorJSONFlag, "json", false, "Output diagnostic report in machine-readable JSON format")
	doctorCmd.Flags().StringVar(&doctorSessionFlag, "session", "", "Target specific session key for deep triage (e.g. 'telegram:8718145628:8544450322')")
	doctorCmd.Flags().BoolVar(&doctorSkipNetworkFlag, "skip-network", false, "Skip live network connectivity checks (e.g. Telegram getMe)")
	doctorCmd.Flags().BoolVar(&doctorLiveProbeFlag, "probe", false, "Execute live AGY CLI auth and model quota probe")

	rootCmd.AddCommand(doctorCmd)
}
