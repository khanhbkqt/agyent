package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"agyent/builtin"
	pluginAdapter "agyent/internal/adapters/plugin"
	"agyent/internal/core/domain"
	"agyent/internal/updater"
	"github.com/spf13/cobra"
)

var (
	pluginAllFlag       bool
	pluginForceFlag     bool
	pluginWorkspaceFlag bool
	pluginTargetDirFlag string

	pluginCmd = &cobra.Command{
		Use:     "plugin",
		Aliases: []string{"plugins"},
		Short:   "Manage, update, and install agyent capability plugins",
		Long: `Provides commands to inspect, install, update, and synchronize capability plugins 
(such as browser-camoufox, database-sqlite, system-diagnostics) between embedded binary releases 
and the local user environment.`,
	}

	pluginListCmd = &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed and embedded capability plugins",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			home, _ := os.UserHomeDir()
			globalHome := filepath.Join(home, ".agyent")
			cwd, _ := os.Getwd()

			mgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)
			plugins, err := mgr.ListPlugins(ctx, globalHome, cwd)
			if err != nil {
				return fmt.Errorf("failed to list plugins: %w", err)
			}

			embeddedList, _ := mgr.ListEmbeddedPlugins(ctx)
			embeddedVersionMap := make(map[string]string)
			for _, ep := range embeddedList {
				embeddedVersionMap[ep.Manifest.Name] = ep.Manifest.Version
			}

			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tVERSION\tEMBEDDED\tSCOPE\tENABLED\tPUBLISHER\tSTATUS")
			fmt.Fprintln(w, "----\t-------\t--------\t-----\t-------\t---------\t------")

			for _, p := range plugins {
				embVer := embeddedVersionMap[p.Manifest.Name]
				if embVer == "" {
					embVer = "-"
				}

				status := "up-to-date"
				if embVer != "-" && updater.IsNewerVersion(embVer, p.Manifest.Version) {
					status = fmt.Sprintf("update available (%s -> %s)", p.Manifest.Version, embVer)
				}

				enabledStr := "yes"
				if !p.Manifest.Enabled {
					enabledStr = "no"
				}

				pub := p.Manifest.Publisher
				if pub == "" {
					pub = "user"
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					p.Manifest.Name,
					p.Manifest.Version,
					embVer,
					p.Scope,
					enabledStr,
					pub,
					status,
				)
			}
			w.Flush()

			return nil
		},
	}

	pluginUpdateCmd = &cobra.Command{
		Use:     "update [plugin-name]",
		Aliases: []string{"sync", "upgrade"},
		Short:   "Update plugins to the latest versions from the embedded binary",
		Long: `Extracts and updates capability plugins from the agyent binary into ~/.agyent/plugins/.
Preserves user profiles, runtime cookies, and custom settings while atomically updating code.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()

			destDir := pluginTargetDirFlag
			if destDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				destDir = filepath.Join(home, ".agyent", "plugins")
			}

			mgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)

			targetPlugin := ""
			if len(args) > 0 {
				targetPlugin = args[0]
			}

			if targetPlugin != "" && !pluginAllFlag {
				fmt.Fprintf(out, "📦 Updating plugin %q to %s...\n", targetPlugin, destDir)
				res, err := mgr.ExtractPluginAtomic(ctx, targetPlugin, destDir, pluginForceFlag)
				if err != nil {
					return fmt.Errorf("failed to update plugin %q: %w", targetPlugin, err)
				}

				if res.Updated {
					fmt.Fprintf(out, "✅ Plugin %q updated successfully to v%s at %s\n", res.Name, res.EmbeddedVersion, res.Path)
				} else if res.Skipped {
					fmt.Fprintf(out, "ℹ️  Plugin %q skipped: %s (version: %s)\n", res.Name, res.Reason, res.InstalledVersion)
				}
				return nil
			}

			fmt.Fprintf(out, "📦 Synchronizing embedded plugins to %s...\n", destDir)
			results, err := mgr.SyncPlugins(ctx, destDir, pluginForceFlag)
			if err != nil {
				return fmt.Errorf("plugin sync failed: %w", err)
			}

			updatedCount := 0
			skippedCount := 0
			for _, r := range results {
				if r.Updated {
					oldVer := r.InstalledVersion
					if oldVer == "" {
						oldVer = "new"
					}
					fmt.Fprintf(out, "   ✓ %s: %s -> v%s (updated)\n", r.Name, oldVer, r.EmbeddedVersion)
					updatedCount++
				} else if r.Skipped {
					fmt.Fprintf(out, "   - %s: %s (%s)\n", r.Name, r.InstalledVersion, r.Reason)
					skippedCount++
				}
			}

			fmt.Fprintf(out, "\n🎉 Plugin synchronization complete! (%d updated, %d up-to-date/skipped)\n", updatedCount, skippedCount)
			return nil
		},
	}

	pluginInstallCmd = &cobra.Command{
		Use:   "install <plugin-name>",
		Short: "Install a builtin plugin into Global or Workspace scope",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			out := cmd.OutOrStdout()
			pluginName := args[0]

			scope := domain.ScopeGlobal
			cwd := ""
			if pluginWorkspaceFlag {
				scope = domain.ScopeWorkspace
				var err error
				cwd, err = os.Getwd()
				if err != nil {
					return err
				}
			}

			mgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)
			if err := mgr.InstallBuiltinPlugin(ctx, pluginName, scope, cwd); err != nil {
				return fmt.Errorf("failed to install plugin %q: %w", pluginName, err)
			}

			fmt.Fprintf(out, "✅ Successfully installed plugin %q in %s scope.\n", pluginName, scope)
			return nil
		},
	}

	pluginEnableCmd = &cobra.Command{
		Use:   "enable <plugin-name>",
		Short: "Enable an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			pluginName := args[0]
			cwd, _ := os.Getwd()

			mgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)
			if err := mgr.TogglePlugin(ctx, pluginName, true, domain.ScopeGlobal, cwd); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✅ Plugin %q enabled.\n", pluginName)
			return nil
		},
	}

	pluginDisableCmd = &cobra.Command{
		Use:   "disable <plugin-name>",
		Short: "Disable an installed plugin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			pluginName := args[0]
			cwd, _ := os.Getwd()

			mgr := pluginAdapter.NewPluginManager("builtin/plugins", &builtin.EmbeddedPluginsFS)
			if err := mgr.TogglePlugin(ctx, pluginName, false, domain.ScopeGlobal, cwd); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✅ Plugin %q disabled.\n", pluginName)
			return nil
		},
	}
)

func init() {
	pluginUpdateCmd.Flags().BoolVar(&pluginAllFlag, "all", false, "Update all embedded plugins")
	pluginUpdateCmd.Flags().BoolVar(&pluginForceFlag, "force", false, "Force overwrite existing files even if version is equal or older")
	pluginUpdateCmd.Flags().StringVar(&pluginTargetDirFlag, "dir", "", "Target plugins directory (default: ~/.agyent/plugins)")

	pluginInstallCmd.Flags().BoolVar(&pluginWorkspaceFlag, "workspace", false, "Install plugin into workspace scope (.agents/plugins) instead of global (~/.agyent/plugins)")

	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginUpdateCmd)
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginEnableCmd)
	pluginCmd.AddCommand(pluginDisableCmd)

	rootCmd.AddCommand(pluginCmd)
}
