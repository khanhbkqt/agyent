package updater

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	noticeBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7D56F4")).
			Padding(0, 1).
			MarginTop(1).
			MarginBottom(1)

	newVerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#04B575"))

	oldVerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500"))

	cmdStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#00D7D7"))

	urlStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#5FAFFF")).
			Underline(true)
)

// RenderUpdateNotification renders an attention-grabbing box notifying the user of an available update.
func RenderUpdateNotification(w io.Writer, currentVer, latestVer, releaseURL, releaseNotes string) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🚀 %s\n", lipgloss.NewStyle().Bold(true).Render("NEW VERSION AVAILABLE!")))
	b.WriteString(fmt.Sprintf("   • Current: %s ➔ Latest: %s\n", oldVerStyle.Render(currentVer), newVerStyle.Render(latestVer)))
	if releaseURL != "" {
		b.WriteString(fmt.Sprintf("   • Release: %s\n", urlStyle.Render(releaseURL)))
	}
	b.WriteString(fmt.Sprintf("\n   👉 Run %s to automatically upgrade.", cmdStyle.Render("agyent update")))

	if releaseNotes != "" {
		snippet := strings.TrimSpace(releaseNotes)
		lines := strings.Split(snippet, "\n")
		if len(lines) > 6 {
			lines = lines[:6]
			lines = append(lines, "   ...")
		}
		b.WriteString("\n\n📝 " + lipgloss.NewStyle().Bold(true).Render("Release Highlights:") + "\n")
		for _, l := range lines {
			b.WriteString(fmt.Sprintf("   %s\n", l))
		}
	}

	fmt.Fprintln(w, noticeBoxStyle.Render(b.String()))
}

// RenderUpdateSuccess renders a success banner after a successful update.
func RenderUpdateSuccess(w io.Writer, oldVer, newVer, releaseURL string) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🎉 %s\n", lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#04B575")).Render("SUCCESSFULLY UPDATED AGYENT!")))
	b.WriteString(fmt.Sprintf("   • Upgraded from %s to %s\n", oldVerStyle.Render(oldVer), newVerStyle.Render(newVer)))
	if releaseURL != "" {
		b.WriteString(fmt.Sprintf("   • Release notes: %s\n", urlStyle.Render(releaseURL)))
	}
	b.WriteString(fmt.Sprintf("\n   👉 Check health with: %s\n", cmdStyle.Render("agyent doctor")))
	b.WriteString(fmt.Sprintf("   👉 Start gateway with: %s", cmdStyle.Render("agyent run")))

	fmt.Fprintln(w, noticeBoxStyle.Render(b.String()))
}
