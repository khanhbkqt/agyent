package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agyent/internal/core/domain"

	"gopkg.in/yaml.v3"
)

// defaultHeartbeatTemplate is generated when an agent workspace does not have a HEARTBEAT.md yet.
const defaultHeartbeatTemplate = `---
enabled: false
interval: 1h
target_session: auto
channel: telegram
---
# Heartbeat Directives

Periodic background check instructions for this agent.
When activated at each heartbeat interval:
1. Review pending items, system alerts, or scheduled checks.
2. Formulate a clear, concise report to be delivered to the user chat.
`

type heartbeatFrontmatter struct {
	Enabled       bool   `yaml:"enabled"`
	Interval      string `yaml:"interval"`
	TargetSession string `yaml:"target_session"`
	Channel       string `yaml:"channel"`
}

// ReadHeartbeat reads and parses HEARTBEAT.md YAML frontmatter and prompt directives from the agent workspace.
func (m *Manager) ReadHeartbeat(ctx context.Context, workspaceDir string) (*domain.HeartbeatConfig, string, error) {
	if workspaceDir == "" {
		return nil, "", fmt.Errorf("workspace directory cannot be empty")
	}

	hbPath := filepath.Join(workspaceDir, "HEARTBEAT.md")

	// If file does not exist, provision the default template
	if _, err := os.Stat(hbPath); os.IsNotExist(err) {
		_ = os.MkdirAll(workspaceDir, 0755)
		if writeErr := os.WriteFile(hbPath, []byte(defaultHeartbeatTemplate), 0644); writeErr != nil {
			m.logger.Warn("Failed to provision default HEARTBEAT.md", "path", hbPath, "error", writeErr)
		}
	}

	contentBytes, err := os.ReadFile(hbPath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read HEARTBEAT.md at %s: %w", hbPath, err)
	}

	content := string(contentBytes)
	cfg, prompt := parseHeartbeatFile(content)
	cfg.AgentName = filepath.Base(workspaceDir)

	return cfg, prompt, nil
}

// WriteHeartbeat serializes and writes HEARTBEAT.md with YAML frontmatter to the agent workspace.
func (m *Manager) WriteHeartbeat(ctx context.Context, workspaceDir string, cfg domain.HeartbeatConfig, prompt string) error {
	if workspaceDir == "" {
		return fmt.Errorf("workspace directory cannot be empty")
	}

	_ = os.MkdirAll(workspaceDir, 0755)
	hbPath := filepath.Join(workspaceDir, "HEARTBEAT.md")

	intervalStr := formatInterval(cfg.IntervalSeconds)
	targetSession := cfg.TargetSessionKey
	if targetSession == "" {
		targetSession = "auto"
	}
	channel := cfg.Channel
	if channel == "" {
		channel = "telegram"
	}

	fm := heartbeatFrontmatter{
		Enabled:       cfg.Enabled,
		Interval:      intervalStr,
		TargetSession: targetSession,
		Channel:       channel,
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(fm); err != nil {
		return fmt.Errorf("failed to encode heartbeat frontmatter: %w", err)
	}
	buf.WriteString("---\n")

	trimmedPrompt := strings.TrimSpace(prompt)
	if trimmedPrompt == "" {
		trimmedPrompt = "# Heartbeat Directives\n\nNo custom directives configured."
	}
	buf.WriteString(trimmedPrompt)
	buf.WriteString("\n")

	tmpPath := hbPath + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write temporary HEARTBEAT.md: %w", err)
	}

	if err := os.Rename(tmpPath, hbPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to commit HEARTBEAT.md: %w", err)
	}

	return nil
}

func parseHeartbeatFile(content string) (*domain.HeartbeatConfig, string) {
	trimmed := strings.TrimSpace(content)
	cfg := &domain.HeartbeatConfig{
		Enabled:         false,
		IntervalSeconds: 3600,
		Channel:         "telegram",
		Status:          domain.HeartbeatStatusIdle,
	}

	if !strings.HasPrefix(trimmed, "---") {
		return cfg, content
	}

	parts := strings.SplitN(trimmed[3:], "\n---", 2)
	if len(parts) < 2 {
		return cfg, content
	}

	fmContent := parts[0]
	prompt := strings.TrimSpace(strings.TrimPrefix(parts[1], "\n"))

	var fm heartbeatFrontmatter
	if err := yaml.Unmarshal([]byte(fmContent), &fm); err == nil {
		cfg.Enabled = fm.Enabled
		cfg.TargetSessionKey = fm.TargetSession
		if fm.Channel != "" {
			cfg.Channel = fm.Channel
		}
		if fm.Interval != "" {
			cfg.IntervalSeconds = parseInterval(fm.Interval)
		}
	}

	if cfg.IntervalSeconds <= 0 {
		cfg.IntervalSeconds = 3600
	}

	return cfg, prompt
}

func parseInterval(expr string) int {
	expr = strings.TrimSpace(strings.ToLower(expr))
	if expr == "" {
		return 3600
	}

	if val, err := strconv.Atoi(expr); err == nil && val > 0 {
		return val
	}

	d, err := time.ParseDuration(expr)
	if err == nil && d > 0 {
		return int(d.Seconds())
	}

	return 3600
}

func formatInterval(seconds int) string {
	if seconds <= 0 {
		return "1h"
	}
	if seconds%3600 == 0 {
		return fmt.Sprintf("%dh", seconds/3600)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", seconds)
}
