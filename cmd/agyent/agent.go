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
	"agyent/internal/core/ports"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	agentPresetFlag    string
	agentDescFlag      string
	agentWorkspaceFlag string

	agentCmd = &cobra.Command{
		Use:     "agent",
		Aliases: []string{"agents", "a"},
		Short:   "Manage, configure, and inspect AI agent profiles and security presets",
		Long: `Provides administrative commands to manage agent profiles, inspect agent configurations,
and update per-agent security presets directly on the host machine.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentList(cmd)
		},
	}

	agentListCmd = &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "show-all"},
		Short:   "List all registered agent profiles and their security presets",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentList(cmd)
		},
	}

	agentPresetCmd = &cobra.Command{
		Use:     "preset <agent_name> [unrestricted|developer|balanced|strict|read_only]",
		Aliases: []string{"set-preset", "security-preset"},
		Short:   "View or set the security preset for a specific agent",
		Long: `Configure the security preset for a specific agent profile.
Available presets:
  - unrestricted (Level 0): 100% full autonomy, zero HITL approval prompts
  - developer    (Level 1): Permissive inside workspace, prompts for external tools
  - balanced     (Level 2): Standard protection with interactive approvals for risky tools
  - strict       (Level 3): Whitelist-only command execution with strict path jail
  - read_only    (Level 4): Read-only inspection only, no file edits or shell execution`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return cmd.Help()
			}
			if len(args) == 1 {
				return runShowAgentPreset(cmd, args[0])
			}
			return runSetAgentPreset(cmd, args[0], args[1])
		},
	}

	agentInfoCmd = &cobra.Command{
		Use:     "info <agent_name>",
		Aliases: []string{"get", "show"},
		Short:   "Display detailed profile information for an agent",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentInfo(cmd, args[0])
		},
	}

	agentCreateCmd = &cobra.Command{
		Use:     "create <agent_name>",
		Aliases: []string{"new", "add"},
		Short:   "Create a new agent profile with custom security preset and workspace",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentCreate(cmd, args[0])
		},
	}
)

func init() {
	agentPresetCmd.Flags().StringVarP(&agentPresetFlag, "preset", "p", "", "Security preset to assign")
	agentCreateCmd.Flags().StringVarP(&agentPresetFlag, "preset", "p", "balanced", "Security preset (unrestricted|developer|balanced|strict|read_only)")
	agentCreateCmd.Flags().StringVarP(&agentDescFlag, "desc", "d", "AI Assistant", "Description of the agent")
	agentCreateCmd.Flags().StringVarP(&agentWorkspaceFlag, "workspace", "w", "", "Custom workspace path (defaults to ~/.agyent/workspace-<name>)")

	agentCmd.AddCommand(agentListCmd)
	agentCmd.AddCommand(agentPresetCmd)
	agentCmd.AddCommand(agentInfoCmd)
	agentCmd.AddCommand(agentCreateCmd)

	rootCmd.AddCommand(agentCmd)
}

func runAgentList(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration from %s: %w", cfgFile, err)
	}

	ctx := context.Background()
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database at %s: %w", cfg.Storage.DBPath, err)
	}
	defer store.Close()

	agents, err := store.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("failed to list agents: %w", err)
	}

	fmt.Fprintln(out, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4")).Render("🤖 Registered Agent Profiles & Security Presets:"))
	fmt.Fprintln(out)

	w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "  NAME\tSECURITY PRESET\tLEVEL\tOWNER\tSTATUS\tWORKSPACE")
	fmt.Fprintln(w, "  ----\t---------------\t-----\t-----\t------\t---------")

	for _, a := range agents {
		preset := string(a.SecurityPreset)
		if preset == "" {
			preset = cfg.ResolveAgentPreset(a.Name)
		}
		lvl := domain.PresetLevel(domain.SecurityPreset(preset))
		owner := a.OwnerID
		if owner == "" {
			owner = "(unclaimed)"
		}

		var presetStyled string
		switch domain.SecurityPreset(preset) {
		case domain.PresetUnrestricted:
			presetStyled = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FF5F56")).Render(preset)
		case domain.PresetStrict, domain.PresetReadOnly:
			presetStyled = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#43BF6D")).Render(preset)
		default:
			presetStyled = preset
		}

		fmt.Fprintf(w, "  %s\t%s\t%d\t%s\t%s\t%s\n",
			a.Name,
			presetStyled,
			lvl,
			owner,
			a.Status,
			a.WorkspacePath,
		)
	}
	w.Flush()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "💡 To update an agent's security preset, run:")
	fmt.Fprintln(out, "   agyent agent preset <agent_name> <unrestricted|developer|balanced|strict|read_only>")
	return nil
}

func runShowAgentPreset(cmd *cobra.Command, agentName string) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	ctx := context.Background()
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	agent, err := store.GetAgent(ctx, agentName)
	if err != nil || agent == nil {
		return fmt.Errorf("agent %q not found", agentName)
	}

	preset := string(agent.SecurityPreset)
	if preset == "" {
		preset = cfg.ResolveAgentPreset(agentName)
	}
	lvl := domain.PresetLevel(domain.SecurityPreset(preset))

	fmt.Fprintf(out, "Agent: %s\n", lipgloss.NewStyle().Bold(true).Render(agentName))
	fmt.Fprintf(out, "Security Preset: %s (Level %d)\n",
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#43BF6D")).Render(preset),
		lvl,
	)
	fmt.Fprintf(out, "\nTo update this agent's preset, run:\n   agyent agent preset %s <unrestricted|developer|balanced|strict|read_only>\n", agentName)
	return nil
}

func runSetAgentPreset(cmd *cobra.Command, agentName string, presetStr string) error {
	if os.Getenv("AGYENT_TURN_ID") != "" || os.Getenv("AGYENT_SUBPROCESS") != "" {
		return fmt.Errorf("⛔ [Security Violation]: Administrative CLI commands cannot be invoked from within an active agent session (AGYENT_TURN_ID detected). Self-privilege escalation is strictly forbidden.")
	}

	out := cmd.OutOrStdout()
	presetStr = strings.ToLower(strings.TrimSpace(presetStr))

	validPresets := map[string]bool{
		"unrestricted": true,
		"developer":    true,
		"balanced":     true,
		"strict":       true,
		"read_only":    true,
	}

	if !validPresets[presetStr] {
		return fmt.Errorf("invalid security preset %q. Allowed: unrestricted, developer, balanced, strict, read_only", presetStr)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration from %s: %w", cfgFile, err)
	}

	ctx := context.Background()
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database at %s: %w", cfg.Storage.DBPath, err)
	}
	defer store.Close()

	agent, err := store.GetAgent(ctx, agentName)
	if err != nil || agent == nil {
		return fmt.Errorf("agent %q not found in SQLite database", agentName)
	}

	oldPreset := string(agent.SecurityPreset)
	if oldPreset == "" {
		oldPreset = "balanced"
	}

	// Update SQLite database (Authoritative for runtime execution turns)
	agent.SecurityPreset = domain.SecurityPreset(presetStr)
	agent.UpdatedAt = time.Now()
	if err := store.SaveAgent(ctx, agent); err != nil {
		return fmt.Errorf("failed to update agent in database: %w", err)
	}

	// Also update config.yaml for parity
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]config.AgentProfileConfig)
	}
	prof := cfg.Agents[agentName]
	prof.SecurityPreset = presetStr
	cfg.Agents[agentName] = prof
	_ = config.Save(cfgFile, cfg)

	fmt.Fprintln(out, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#04B575")).Render("✅ Security Preset Updated Successfully"))
	fmt.Fprintf(out, "  Agent Name     :  %s\n", agentName)
	fmt.Fprintf(out, "  Preset Change  :  %s -> %s (Level %d)\n",
		oldPreset,
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFD700")).Render(presetStr),
		domain.PresetLevel(domain.SecurityPreset(presetStr)),
	)
	fmt.Fprintf(out, "  SQLite DB      :  %s\n", cfg.Storage.DBPath)
	if presetStr == "unrestricted" {
		fmt.Fprintln(out, lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Render("  ⚠️  Notice: 'unrestricted' grants full tool autonomy without Human-in-the-Loop prompts."))
	}
	fmt.Fprintln(out)
	return nil
}

func runAgentInfo(cmd *cobra.Command, agentName string) error {
	out := cmd.OutOrStdout()
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	ctx := context.Background()
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	agent, err := store.GetAgent(ctx, agentName)
	if err != nil || agent == nil {
		return fmt.Errorf("agent %q not found", agentName)
	}

	preset := string(agent.SecurityPreset)
	if preset == "" {
		preset = cfg.ResolveAgentPreset(agentName)
	}

	owner := agent.OwnerID
	if owner == "" {
		owner = "(unclaimed)"
	}

	fmt.Fprintln(out, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7D56F4")).Render(fmt.Sprintf("📋 Agent Profile: %s", agent.Name)))
	fmt.Fprintf(out, "  Description     : %s\n", agent.Description)
	fmt.Fprintf(out, "  Security Preset : %s (Level %d)\n", preset, domain.PresetLevel(domain.SecurityPreset(preset)))
	fmt.Fprintf(out, "  Owner ID        : %s\n", owner)
	fmt.Fprintf(out, "  Status          : %s\n", agent.Status)
	fmt.Fprintf(out, "  Workspace Path  : %s\n", agent.WorkspacePath)
	fmt.Fprintf(out, "  Default Model   : %s\n", agent.DefaultModel)
	fmt.Fprintf(out, "  Default Effort  : %s\n", agent.DefaultEffort)
	fmt.Fprintf(out, "  Created At      : %s\n", agent.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(out, "  Updated At      : %s\n", agent.UpdatedAt.Format(time.RFC3339))
	return nil
}

func runAgentCreate(cmd *cobra.Command, name string) error {
	if os.Getenv("AGYENT_TURN_ID") != "" || os.Getenv("AGYENT_SUBPROCESS") != "" {
		return fmt.Errorf("⛔ [Security Violation]: Administrative CLI commands cannot be invoked from within an active agent session (AGYENT_TURN_ID detected). Self-privilege escalation is strictly forbidden.")
	}

	out := cmd.OutOrStdout()
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return fmt.Errorf("invalid agent name %q: must not contain path separators or '..'", name)
	}

	preset := strings.ToLower(strings.TrimSpace(agentPresetFlag))
	if preset == "" {
		preset = "balanced"
	}
	validPresets := map[string]bool{
		"unrestricted": true,
		"developer":    true,
		"balanced":     true,
		"strict":       true,
		"read_only":    true,
	}
	if !validPresets[preset] {
		return fmt.Errorf("invalid security preset %q. Allowed: unrestricted, developer, balanced, strict, read_only", preset)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	ctx := context.Background()
	store, err := sqlite.Open(cfg.Storage.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer store.Close()

	wsPath := agentWorkspaceFlag
	if wsPath == "" {
		wsPath = config.ResolveAgentWorkspace(cfg.Storage.AgentsDir, name)
	}
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		return fmt.Errorf("failed to create agent workspace at %s: %w", wsPath, err)
	}

	agent := &domain.Agent{
		Name:           name,
		Description:    agentDescFlag,
		Status:         domain.StatusUninitialized,
		WorkspacePath:  wsPath,
		SecurityPreset: domain.SecurityPreset(preset),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := store.CreateAgent(ctx, agent); err != nil {
		if ports.ErrAlreadyExists != nil && strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("agent %q already exists", name)
		}
		return fmt.Errorf("failed to register agent: %w", err)
	}

	fmt.Fprintln(out, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#04B575")).Render(fmt.Sprintf("🎉 Agent %q Created Successfully!", name)))
	fmt.Fprintf(out, "  Security Preset : %s (Level %d)\n", preset, domain.PresetLevel(domain.SecurityPreset(preset)))
	fmt.Fprintf(out, "  Workspace Path  : %s\n", wsPath)
	fmt.Fprintf(out, "  Status          : %s\n", agent.Status)
	return nil
}
