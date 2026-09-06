package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/domain"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	secAgentFlag string

	secTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginBottom(1)

	secSuccessStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	secWarningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500"))

	secErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF4040"))

	secKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#43BF6D"))

	secValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	securityCmd = &cobra.Command{
		Use:     "security",
		Aliases: []string{"sec", "guardrail"},
		Short:   "Inspect and manage security gateway policies, presets, and guardrails",
		Long: `Provides commands to inspect the active security posture, list available security presets, 
and switch security presets for the entire gateway or specific agent profiles without monotonic downgrade restrictions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecurityStatus(cmd)
		},
	}

	securityStatusCmd = &cobra.Command{
		Use:     "status",
		Aliases: []string{"info", "show"},
		Short:   "Display the active security gateway status, presets, and jail configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSecurityStatus(cmd)
		},
	}

	securityPresetCmd = &cobra.Command{
		Use:     "preset [unrestricted|developer|workspace_only|balanced|strict|read_only]",
		Aliases: []string{"set-preset", "mode"},
		Short:   "View or set the security preset for the gateway or a specific agent",
		Long: `View or update the security preset. When called with a preset mode argument, updates both config.yaml 
and the persistent SQLite agent records. Because this command runs on the host machine with administrative access, 
it can downgrade or upgrade to any preset level freely.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return runShowCurrentPreset(cmd)
			}
			return runSetPreset(cmd, args[0], secAgentFlag)
		},
	}

	securityListPresetsCmd = &cobra.Command{
		Use:     "list-presets",
		Aliases: []string{"presets", "matrix", "levels"},
		Short:   "List all available security presets, autonomy levels, and guardrail behaviors",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runListPresets(cmd)
		},
	}
)

func init() {
	securityPresetCmd.Flags().StringVarP(&secAgentFlag, "agent", "a", "", "Target agent name (e.g. 'agyent'). If omitted, updates the global preset and default agent.")

	securityCmd.AddCommand(securityStatusCmd)
	securityCmd.AddCommand(securityPresetCmd)
	securityCmd.AddCommand(securityListPresetsCmd)

	rootCmd.AddCommand(securityCmd)
}

func runSecurityStatus(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration from %s: %w", cfgFile, err)
	}

	globalPreset := cfg.Security.Preset
	if globalPreset == "" {
		globalPreset = "balanced"
	}
	level := domain.PresetLevel(domain.SecurityPreset(globalPreset))

	fmt.Fprintln(out, secTitleStyle.Render("🛡️  Agyent Security Gateway Status"))
	fmt.Fprintf(out, "  %s  %s (Level %d)\n", secKeyStyle.Render("Global Security Preset :"), secValueStyle.Render(globalPreset), level)
	fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("Operational Mode       :"), secValueStyle.Render(cfg.Security.Mode))
	fmt.Fprintf(out, "  %s  %d seconds\n", secKeyStyle.Render("HITL Approval Timeout  :"), cfg.Security.ApprovalTimeoutSeconds)
	fmt.Fprintf(out, "  %s  %t\n", secKeyStyle.Render("Enforce Workspace Jail :"), cfg.Security.Filesystem.EnforceWorkspaceJail)
	fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("Configuration File     :"), cfgFile)
	fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("SQLite Database        :"), cfg.Storage.DBPath)
	fmt.Fprintln(out)

	// List agents and their individual security presets
	ctx := context.Background()
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err == nil {
		defer store.Close()
		agents, err := store.ListAgents(ctx)
		if err == nil && len(agents) > 0 {
			fmt.Fprintln(out, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4")).Render("🤖 Registered Agent Profiles:"))
			w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "  NAME\tSECURITY PRESET\tLEVEL\tSTATUS\tWORKSPACE")
			fmt.Fprintln(w, "  ----\t---------------\t-----\t------\t---------")
			for _, a := range agents {
				agentPreset := string(a.SecurityPreset)
				if agentPreset == "" {
					agentPreset = cfg.ResolveAgentPreset(a.Name)
				}
				lvl := domain.PresetLevel(domain.SecurityPreset(agentPreset))
				fmt.Fprintf(w, "  %s\t%s\t%d\t%s\t%s\n", a.Name, agentPreset, lvl, a.Status, a.WorkspacePath)
			}
			w.Flush()
			fmt.Fprintln(out)
		}
	}

	return nil
}

func runShowCurrentPreset(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	preset := cfg.ResolveAgentPreset(secAgentFlag)
	lvl := domain.PresetLevel(domain.SecurityPreset(preset))

	if secAgentFlag != "" {
		fmt.Fprintf(out, "Current security preset for agent %s: %s (Level %d)\n",
			lipgloss.NewStyle().Bold(true).Render(secAgentFlag),
			secValueStyle.Render(preset),
			lvl,
		)
	} else {
		fmt.Fprintf(out, "Current global security preset: %s (Level %d)\n",
			secValueStyle.Render(preset),
			lvl,
		)
	}
	fmt.Fprintf(out, "\nTo change the preset, run:\n  agyent security preset <unrestricted|developer|balanced|strict|read_only>%s\n",
		func() string {
			if secAgentFlag != "" {
				return " --agent " + secAgentFlag
			}
			return ""
		}(),
	)

	return nil
}

func runSetPreset(cmd *cobra.Command, mode string, agentName string) error {
	if os.Getenv("AGYENT_TURN_ID") != "" || os.Getenv("AGYENT_SUBPROCESS") != "" {
		return fmt.Errorf("⛔ [Security Violation]: Administrative CLI commands cannot be invoked from within an active agent session (AGYENT_TURN_ID detected). Self-privilege escalation is strictly forbidden.")
	}

	out := cmd.OutOrStdout()
	mode = strings.ToLower(strings.TrimSpace(mode))

	// Validate mode
	validPresets := map[string]bool{
		"unrestricted":   true,
		"developer":      true,
		"workspace_only": true,
		"balanced":       true,
		"strict":         true,
		"read_only":      true,
	}

	if !validPresets[mode] {
		return fmt.Errorf("invalid security preset %q. Allowed options: unrestricted, developer, workspace_only, balanced, strict, read_only", mode)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration from %s: %w", cfgFile, err)
	}

	targetAgent := agentName
	isGlobal := false
	if targetAgent == "" {
		isGlobal = true
		targetAgent = "agyent"
	}

	// Update Config struct
	oldPreset := cfg.ResolveAgentPreset(targetAgent)
	if isGlobal {
		cfg.Security.Preset = mode
		// Also update Security effective sub-configs
		effSec := config.GetEffectiveSecurityPreset(mode)
		cfg.Security.Mode = effSec.Mode
		cfg.Security.Filesystem.EnforceWorkspaceJail = effSec.Filesystem.EnforceWorkspaceJail
		cfg.Security.Commands = effSec.Commands
		cfg.Security.DLP = effSec.DLP
	}

	if cfg.Agents == nil {
		cfg.Agents = make(map[string]config.AgentProfileConfig)
	}
	prof := cfg.Agents[targetAgent]
	prof.SecurityPreset = mode
	cfg.Agents[targetAgent] = prof

	// Save to config.yaml
	if err := config.Save(cfgFile, cfg); err != nil {
		return fmt.Errorf("failed to save configuration to %s: %w", cfgFile, err)
	}

	// Synchronize with SQLite database if available
	dbUpdated := false
	ctx := context.Background()
	if store, err := sqlite.Open(cfg.Storage.DBPath); err == nil {
		defer store.Close()
		agent, getErr := store.GetAgent(ctx, targetAgent)
		if getErr == nil && agent != nil {
			agent.SecurityPreset = domain.SecurityPreset(mode)
			agent.UpdatedAt = time.Now()
			if saveErr := store.SaveAgent(ctx, agent); saveErr == nil {
				dbUpdated = true
			}
		} else if isGlobal {
			// Update all existing agents in DB if global update
			if agents, listErr := store.ListAgents(ctx); listErr == nil {
				for _, a := range agents {
					a.SecurityPreset = domain.SecurityPreset(mode)
					a.UpdatedAt = time.Now()
					_ = store.SaveAgent(ctx, &a)
				}
				dbUpdated = true
			}
		}
	}

	// Render output
	fmt.Fprintln(out, secSuccessStyle.Render("✅ Security Preset Updated Successfully"))
	if isGlobal {
		fmt.Fprintf(out, "  %s  %s -> %s\n", secKeyStyle.Render("Global Preset    :"), oldPreset, secSuccessStyle.Render(mode))
		fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("Default Agent    :"), targetAgent)
	} else {
		fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("Target Agent     :"), targetAgent)
		fmt.Fprintf(out, "  %s  %s -> %s\n", secKeyStyle.Render("Agent Preset     :"), oldPreset, secSuccessStyle.Render(mode))
	}
	fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("Config Saved To  :"), cfgFile)
	if dbUpdated {
		fmt.Fprintf(out, "  %s  %s\n", secKeyStyle.Render("SQLite DB Synced :"), cfg.Storage.DBPath)
	}
	fmt.Fprintln(out)

	return nil
}

func runListPresets(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, secTitleStyle.Render("🛡️  Agyent Security Presets Matrix"))
	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "  PRESET\tLEVEL\tAUTONOMY\tWORKSPACE JAIL\tHITL CONFIRMATION\tDESCRIPTION")
	fmt.Fprintln(w, "  ------\t-----\t--------\t--------------\t-----------------\t-----------")

	presets := []struct {
		name        string
		level       int
		autonomy    string
		jail        string
		hitl        string
		description string
	}{
		{
			name:        "unrestricted",
			level:       0,
			autonomy:    "Full",
			jail:        "Disabled",
			hitl:        "None",
			description: "Full autonomy, no approval required, unrestricted filesystem/command access",
		},
		{
			name:        "developer",
			level:       1,
			autonomy:    "High",
			jail:        "Optional",
			hitl:        "Sensitive only",
			description: "Full developer workflow inside repo, HITL for critical system-level modifications",
		},
		{
			name:        "workspace_only",
			level:       2,
			autonomy:    "Autonomous",
			jail:        "Enforced",
			hitl:        "Forbidden Paths Only",
			description: "Standard agent containment: autonomous inside workspace, all outer paths strictly denied",
		},
		{
			name:        "balanced",
			level:       3,
			autonomy:    "Medium",
			jail:        "Enforced",
			hitl:        "Shell / Out-of-jail",
			description: "Standard containment: workspace jailing, HITL for sensitive commands and outer files",
		},
		{
			name:        "strict",
			level:       4,
			autonomy:    "Low",
			jail:        "Enforced",
			hitl:        "Non-whitelisted",
			description: "High security posture: only explicitly whitelisted commands are permitted",
		},
		{
			name:        "read_only",
			level:       5,
			autonomy:    "Zero-Write",
			jail:        "Enforced",
			hitl:        "All Writes Denied",
			description: "Audit & exploration only: zero file modifications or terminal executions allowed",
		},
	}

	for _, p := range presets {
		fmt.Fprintf(w, "  %s\t%d\t%s\t%s\t%s\t%s\n", p.name, p.level, p.autonomy, p.jail, p.hitl, p.description)
	}
	w.Flush()
	fmt.Fprintln(out)

	return nil
}
