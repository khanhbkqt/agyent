package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4"))

	categoryStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00D7D7")).
			MarginTop(1)

	passStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	warnStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFA500"))

	failStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FF4040"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5FAFFF"))

	subtleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8A8A8A"))
)

// RenderText writes human-readable styled diagnostic output to w.
func (r *DiagnosticReport) RenderText(w io.Writer) {
	fmt.Fprintln(w, "================================================================================")
	fmt.Fprintln(w, headerStyle.Render("🩺 AGYENT DOCTOR — COMPREHENSIVE SYSTEM & HEALTH DIAGNOSTICS"))
	fmt.Fprintln(w, "================================================================================")

	// Group results by category
	categories := []Category{
		CategorySystem,
		CategoryConfig,
		CategoryAGY,
		CategoryStorage,
		CategoryTelegram,
		CategorySecurity,
		CategoryPlugins,
	}

	resultsByCategory := make(map[Category][]CheckResult)
	for _, res := range r.Results {
		resultsByCategory[res.Category] = append(resultsByCategory[res.Category], res)
	}

	for _, cat := range categories {
		items := resultsByCategory[cat]
		if len(items) == 0 {
			continue
		}

		fmt.Fprintln(w, categoryStyle.Render(fmt.Sprintf("📋 %s:", strings.ToUpper(string(cat)))))
		for _, item := range items {
			var icon string
			switch item.Status {
			case StatusPass:
				icon = passStyle.Render("  ✅")
			case StatusWarn:
				icon = warnStyle.Render("  ⚠️ ")
			case StatusFail:
				icon = failStyle.Render("  ❌")
			case StatusInfo:
				icon = infoStyle.Render("  ℹ️ ")
			}

			fmt.Fprintf(w, "%s %-36s %s\n", icon, item.Name+":", item.Message)
			if item.Details != "" {
				fmt.Fprintf(w, "     %s\n", subtleStyle.Render("Details: "+item.Details))
			}
			if item.Remediation != "" && (item.Status == StatusWarn || item.Status == StatusFail) {
				fmt.Fprintf(w, "     👉 %s\n", warnStyle.Render("Remediation: "+item.Remediation))
			}
		}
	}

	fmt.Fprintln(w, "\n================================================================================")
	fmt.Fprintf(w, "📊 SUMMARY: ")

	switch r.SummaryStatus() {
	case StatusPass:
		fmt.Fprintln(w, passStyle.Render("HEALTHY — All diagnostic checks passed successfully!"))
	case StatusWarn:
		fmt.Fprintln(w, warnStyle.Render("WARNING — Some issues or limits were detected, but daemon can operate."))
	case StatusFail:
		fmt.Fprintln(w, failStyle.Render("ACTION REQUIRED — Critical failures detected that must be resolved."))
	}

	fmt.Fprintf(w, "   • Passed: %d | Warnings: %d | Errors: %d | Info: %d (Elapsed: %dms)\n",
		r.PassedCount, r.WarnCount, r.FailCount, r.InfoCount, r.DurationMs)

	if r.FailCount > 0 || r.WarnCount > 0 {
		fmt.Fprintln(w, "\n💡 Quick Tips:")
		if r.FailCount > 0 {
			fmt.Fprintln(w, "   • Run 'agyent doctor --fix' to automatically resolve repairable directory & DB issues.")
			fmt.Fprintln(w, "   • Run 'agy' in your terminal if Google authentication or OAuth token has expired.")
		}
		if r.WarnCount > 0 {
			fmt.Fprintln(w, "   • In Telegram, send '/new' or '/reset' to clear conversation context if tokens > 1.0M.")
		}
	}
	fmt.Fprintln(w, "================================================================================")
}

// RenderJSON serializes the DiagnosticReport to indented JSON.
func (r *DiagnosticReport) RenderJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
