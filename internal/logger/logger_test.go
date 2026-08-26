package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.expected, ParseLevel(tc.input))
		})
	}
}

func TestInit_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	l := Init(Options{
		Level:  "info",
		Format: "text",
		Output: &buf,
	})
	require.NotNil(t, l)

	l.Info("test info message", slog.String("key", "val"))

	out := buf.String()
	assert.Contains(t, out, "test info message")
	assert.Contains(t, out, "key=val")
	assert.Contains(t, out, "level=INFO")
}

func TestInit_JsonFormat(t *testing.T) {
	var buf bytes.Buffer
	l := Init(Options{
		Level:  "debug",
		Format: "json",
		Output: &buf,
	})
	require.NotNil(t, l)

	l.Debug("debug message", slog.Int("count", 42))

	out := strings.TrimSpace(buf.String())
	var parsed map[string]any
	err := json.Unmarshal([]byte(out), &parsed)
	require.NoError(t, err)

	assert.Equal(t, "debug message", parsed["msg"])
	assert.Equal(t, "DEBUG", parsed["level"])
	assert.Equal(t, float64(42), parsed["count"])
	assert.NotEmpty(t, parsed["source"])
}

func TestNamed(t *testing.T) {
	var buf bytes.Buffer
	_ = Init(Options{
		Level:  "info",
		Format: "text",
		Output: &buf,
	})

	named := Named("engine")
	named.Info("engine started")

	out := buf.String()
	assert.Contains(t, out, "module=engine")
	assert.Contains(t, out, "engine started")
}
