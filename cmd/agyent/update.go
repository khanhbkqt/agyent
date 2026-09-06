package main

import (
	"context"
	"fmt"
	"os/exec"

	"agyent/internal/updater"
	"github.com/spf13/cobra"
)

var (
	updateCheckFlag       bool
	updateForceFlag       bool
	updateDryRunFlag      bool
	updateSkipPluginsFlag bool
	updateVersionFlag     string
	updateRepoFlag        string

	updateCmd = &cobra.Command{
		Use:     "update",
		Aliases: []string{"upgrade", "self-update"},
		Short:   "Check for updates and automatically upgrade agyent binary and plugins",
		Long: `Connects to GitHub releases, compares the installed version with remote releases, 
downloads platform-specific archives, safely replaces the current agyent binary in-place,
and automatically synchronizes capability plugins.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			repo := updateRepoFlag
			if repo == "" {
				repo = updater.DefaultRepo
			}

			opts := updater.Options{
				Repository:     repo,
				TargetVersion:  updateVersionFlag,
				CurrentVersion: Version,
				Force:          updateForceFlag,
				DryRun:         updateDryRunFlag,
			}

			if updateCheckFlag {
				fmt.Fprintf(out, "🔍 Checking for updates on https://github.com/%s...\n", repo)
				res, err := updater.CheckOnly(ctx, opts)
				if err != nil {
					return fmt.Errorf("failed to check for updates: %w", err)
				}

				if res.IsNewer {
					updater.RenderUpdateNotification(out, res.CurrentVersion, res.TargetVersion, res.ReleaseURL, res.ReleaseNotes)
				} else {
					fmt.Fprintf(out, "✅ agyent is up-to-date! Current version: %s (Latest: %s)\n", res.CurrentVersion, res.TargetVersion)
				}
				return nil
			}

			fmt.Fprintf(out, "🚀 Checking latest release for agyent (current: %s)...\n", Version)
			res, err := updater.Execute(ctx, opts)
			if err != nil {
				return fmt.Errorf("update failed: %w", err)
			}

			if !res.Updated {
				if !res.IsNewer && !opts.Force {
					fmt.Fprintf(out, "✅ agyent is already at the latest version (%s).\n", res.CurrentVersion)
					fmt.Fprintln(out, "   Use 'agyent update --force' to force reinstallation if needed.")
					return nil
				}
				if opts.DryRun {
					fmt.Fprintf(out, "ℹ️  [Dry-Run] Target release %s (%s) verified. No files modified.\n", res.TargetVersion, res.AssetFilename)
					return nil
				}
			}

			updater.RenderUpdateSuccess(out, res.CurrentVersion, res.TargetVersion, res.ReleaseURL)

			// Automatically invoke new binary to synchronize updated embedded plugins
			if !updateSkipPluginsFlag && res.ExecutablePath != "" {
				fmt.Fprintln(out, "\n📦 Updating capability plugins to match new release...")
				syncCmd := exec.CommandContext(ctx, res.ExecutablePath, "plugin", "update", "--all", "--force")
				syncCmd.Stdout = out
				syncCmd.Stderr = cmd.ErrOrStderr()
				if err := syncCmd.Run(); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "⚠️ Warning: Failed to auto-sync plugins: %v\n", err)
				}
			}

			return nil
		},
	}
)

func init() {
	updateCmd.Flags().BoolVar(&updateCheckFlag, "check", false, "Check for available updates without downloading or installing")
	updateCmd.Flags().BoolVar(&updateForceFlag, "force", false, "Force reinstall even if already running the target version")
	updateCmd.Flags().BoolVar(&updateDryRunFlag, "dry-run", false, "Simulate download and asset verification without replacing the binary")
	updateCmd.Flags().BoolVar(&updateSkipPluginsFlag, "skip-plugins", false, "Skip automatic plugin synchronization after binary upgrade")
	updateCmd.Flags().StringVar(&updateVersionFlag, "version", "latest", "Target version tag to install (e.g. 'v1.0.0' or 'latest')")
	updateCmd.Flags().StringVar(&updateRepoFlag, "repo", updater.DefaultRepo, "GitHub repository to fetch releases from")

	rootCmd.AddCommand(updateCmd)
}
