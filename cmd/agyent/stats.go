package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"github.com/spf13/cobra"
)

var (
	statsAgentFlag   string
	statsSessionFlag string
	statsDBFlag      string
	statsJSONFlag    bool

	statsCmd = &cobra.Command{
		Use:     "stats [agent_name]",
		Aliases: []string{"tokens", "metrics", "analytics", "token"},
		Short:   "Display token metrics, KV-cache efficiency, compaction savings, and model breakdown",
		Long: `Query and visualize global or filtered token consumption metrics directly from the local SQLite database.
Allows operators and CI/CD pipelines to evaluate token efficiency, prefix KV-cache hit rates, 
compaction bloat reduction, and per-agent / per-model distributions.`,
		Example: `  # View global token metrics across all agents & sessions
  agyent stats

  # Filter token statistics for a specific agent persona
  agyent stats --agent agyent
  agyent stats coder

  # Filter for a specific session key
  agyent stats --session telegram:123456789

  # Export raw JSON metrics for external monitoring
  agyent stats --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// 1. Resolve agent flag if passed as positional argument
			targetAgent := statsAgentFlag
			if targetAgent == "" && len(args) > 0 {
				if args[0] != "global" && args[0] != "all" {
					targetAgent = args[0]
				}
			}

			// 2. Resolve Database Path
			dbPath := statsDBFlag
			if dbPath == "" {
				cfg, err := config.Load(cfgFile)
				if err == nil && cfg != nil && cfg.Storage.DBPath != "" {
					dbPath = cfg.Storage.DBPath
				} else {
					homeDir, _ := os.UserHomeDir()
					dbPath = filepath.Join(homeDir, ".agyent", "agyent.db")
				}
			}

			if strings.HasPrefix(dbPath, "~/") || strings.HasPrefix(dbPath, "~\\") {
				homeDir, _ := os.UserHomeDir()
				dbPath = filepath.Join(homeDir, dbPath[2:])
			}

			if _, err := os.Stat(dbPath); os.IsNotExist(err) {
				return fmt.Errorf("database file not found at %s. Start agyent or run 'agyent init' first", dbPath)
			}

			// 3. Open SQLite Storage
			store, err := sqlite.Open(dbPath)
			if err != nil {
				return fmt.Errorf("failed to open database at %s: %w", dbPath, err)
			}
			defer store.Close()

			// 4. Query Analytics Report
			report, err := store.GetTokenEfficiencyReport(ctx, statsSessionFlag, targetAgent)
			if err != nil {
				return fmt.Errorf("failed to generate token efficiency report: %w", err)
			}

			// 5. Output JSON or Styled Terminal View
			if statsJSONFlag {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}

			renderStatsTerminal(cmd.OutOrStdout(), dbPath, report)
			return nil
		},
	}
)

func init() {
	statsCmd.Flags().StringVarP(&statsAgentFlag, "agent", "a", "", "Filter metrics for a specific agent persona (e.g. agyent, coder)")
	statsCmd.Flags().StringVarP(&statsSessionFlag, "session", "s", "", "Filter metrics for a specific session key (e.g. telegram:123456)")
	statsCmd.Flags().StringVar(&statsDBFlag, "db", "", "Path to SQLite database file (defaults to config DBPath)")
	statsCmd.Flags().BoolVar(&statsJSONFlag, "json", false, "Output report as JSON")

	rootCmd.AddCommand(statsCmd)
}

func formatCLIInt(n int) string {
	in := strconv.Itoa(n)
	out := make([]byte, 0, len(in)+(len(in)-1)/3)
	for i, c := range in {
		if i > 0 && (len(in)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

func renderStatsTerminal(w io.Writer, dbPath string, report *domain.TokenEfficiencyReport) {
	fmt.Fprintln(w, "================================================================================")
	fmt.Fprintln(w, "                  📊 AGYENT TOKEN ANALYTICS & EFFICIENCY DASHBOARD              ")
	fmt.Fprintln(w, "================================================================================")

	scopeDesc := "🌐 Global (All Sessions & Agents)"
	if report.AgentFilter != "" && report.SessionKey != "" {
		scopeDesc = fmt.Sprintf("🎯 Agent: [%s] | Session: [%s]", report.AgentFilter, report.SessionKey)
	} else if report.AgentFilter != "" {
		scopeDesc = fmt.Sprintf("🎯 Filtered Agent: [%s]", report.AgentFilter)
	} else if report.SessionKey != "" {
		scopeDesc = fmt.Sprintf("🧵 Filtered Session: [%s]", report.SessionKey)
	}

	fmt.Fprintf(w, "• Database:   %s\n", dbPath)
	fmt.Fprintf(w, "• Scope:      %s\n", scopeDesc)
	fmt.Fprintf(w, "• Generated:  %s\n", report.GeneratedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintln(w, "--------------------------------------------------------------------------------")

	// 1. Today
	fmt.Fprintln(w, "📅 TODAY'S CONSUMPTION:")
	if report.TodayTurns > 0 {
		todayHit := report.TodayUsage.CacheHitRatio()
		todaySaved := report.TodayUsage.EffectiveCostSavingsRatio()
		fmt.Fprintf(w, "   • Turns:             %d\n", report.TodayTurns)
		fmt.Fprintf(w, "   • Total Billed:      %s tokens\n", formatCLIInt(report.TodayUsage.EffectiveTotalTokens()))
		fmt.Fprintf(w, "   • ⚡ KV-Cache Read:   %s (%.1f%% Cache Hit)\n", formatCLIInt(report.TodayUsage.CacheReadTokens), todayHit)
		fmt.Fprintf(w, "   • Fresh Input:       %s tokens\n", formatCLIInt(report.TodayUsage.UncachedInputTokens()))
		fmt.Fprintf(w, "   • Output Produced:   %s (Thinking: %s)\n", formatCLIInt(report.TodayUsage.OutputTokens), formatCLIInt(report.TodayUsage.ThinkingTokens))
		fmt.Fprintf(w, "   • 💰 Est. Cost Saved: ~%.1f%%\n", todaySaved)
	} else {
		fmt.Fprintln(w, "   • (No turns executed today)")
	}
	fmt.Fprintln(w, "")

	// 2. Past 7 Days
	fmt.Fprintln(w, "🗓️  PAST 7 DAYS:")
	if report.Past7DaysTurns > 0 {
		p7Hit := report.Past7DaysUsage.CacheHitRatio()
		p7Saved := report.Past7DaysUsage.EffectiveCostSavingsRatio()
		fmt.Fprintf(w, "   • Turns:             %d\n", report.Past7DaysTurns)
		fmt.Fprintf(w, "   • Total Billed:      %s tokens\n", formatCLIInt(report.Past7DaysUsage.EffectiveTotalTokens()))
		fmt.Fprintf(w, "   • ⚡ KV-Cache Read:   %s (%.1f%% Cache Hit)\n", formatCLIInt(report.Past7DaysUsage.CacheReadTokens), p7Hit)
		fmt.Fprintf(w, "   • Fresh Input:       %s tokens\n", formatCLIInt(report.Past7DaysUsage.UncachedInputTokens()))
		fmt.Fprintf(w, "   • Output Produced:   %s tokens\n", formatCLIInt(report.Past7DaysUsage.OutputTokens))
		fmt.Fprintf(w, "   • 💰 Est. Cost Saved: ~%.1f%%\n", p7Saved)
	} else {
		fmt.Fprintln(w, "   • (No turns executed in the past 7 days)")
	}
	fmt.Fprintln(w, "")

	// 3. All-Time Lifetime
	fmt.Fprintln(w, "🌐 ALL-TIME LIFETIME:")
	if report.AllTimeTurns > 0 {
		fmt.Fprintf(w, "   • Total Turns:       %d\n", report.AllTimeTurns)
		fmt.Fprintf(w, "   • Total Input:       %s tokens\n", formatCLIInt(report.AllTimeUsage.GrossInputTokens()))
		fmt.Fprintf(w, "   • ⚡ Total Cached:    %s (%.1f%% Lifetime Cache Hit)\n", formatCLIInt(report.AllTimeUsage.CacheReadTokens), report.AvgCacheHitRatio)
		fmt.Fprintf(w, "   • Total Output:      %s (Thinking: %s)\n", formatCLIInt(report.AllTimeUsage.OutputTokens), formatCLIInt(report.AllTimeUsage.ThinkingTokens))
		fmt.Fprintf(w, "   • 💎 Net Saved:      ~%.1f%% (Prefix KV-Cache Discount)\n", report.TotalCostSavedPct)
	} else {
		fmt.Fprintln(w, "   • (No lifetime turns recorded)")
	}
	fmt.Fprintln(w, "")

	// 4. Compactor Efficiency
	fmt.Fprintln(w, "🧹 CONTEXT COMPACTOR SAVINGS:")
	if report.TotalCompactions > 0 {
		fmt.Fprintf(w, "   • Compaction Runs:   %d times\n", report.TotalCompactions)
		fmt.Fprintf(w, "   • Bloat Prevented:   ~%s tokens\n", formatCLIInt(int(report.EstTokensSaved)))
		fmt.Fprintf(w, "   • Avg Compression:   ~99.6%% reduction per compaction\n")
	} else {
		fmt.Fprintln(w, "   • Compaction Runs:   0 (Context window has not required compaction yet)")
	}
	fmt.Fprintln(w, "")

	// 5. Agent Breakdown
	if len(report.AgentBreakdown) > 0 {
		fmt.Fprintln(w, "👥 BREAKDOWN BY AGENT PERSONA:")
		for _, ab := range report.AgentBreakdown {
			hitRatio := ab.Usage.CacheHitRatio()
			fmt.Fprintf(w, "   • %-18s %4d turns | %12s tokens | ⚡ %5.1f%% cached\n",
				ab.AgentName, ab.TurnCount, formatCLIInt(ab.Usage.TotalTokens), hitRatio)
		}
		fmt.Fprintln(w, "")
	}

	// 6. Model Breakdown
	if len(report.ModelBreakdown) > 0 {
		fmt.Fprintln(w, "🤖 BREAKDOWN BY AI MODEL:")
		for _, m := range report.ModelBreakdown {
			hitRatio := m.Usage.CacheHitRatio()
			fmt.Fprintf(w, "   • %-22s %4d turns | %12s tokens | ⚡ %5.1f%% cached\n",
				m.DisplayName, m.TurnCount, formatCLIInt(m.Usage.TotalTokens), hitRatio)
		}
	}

	fmt.Fprintln(w, "================================================================================")
}
