package agy_test

import (
	"errors"
	"testing"

	"agyent/internal/adapters/harness/agy"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOutput_TC_PARS_01_CleanJSON(t *testing.T) {
	stdout := []byte(`{
		"conversation_id": "708ef346-15df-4175-9df0-0c82d5838eef",
		"status": "SUCCESS",
		"response": "Hello! How can I assist you with your project today?\n",
		"duration_seconds": 3.6547759,
		"num_turns": 1,
		"usage": {
			"input_tokens": 13777,
			"output_tokens": 73,
			"thinking_tokens": 62,
			"cache_read_tokens": 0,
			"total_tokens": 13850
		}
	}`)
	stderr := []byte("")

	res, err := agy.ParseOutput(stdout, stderr)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "708ef346-15df-4175-9df0-0c82d5838eef", res.ConversationID)
	assert.Equal(t, "Hello! How can I assist you with your project today?\n", res.ResponseText)
	assert.Equal(t, 3.6547759, res.DurationSec)
	assert.Equal(t, 13777, res.Usage.InputTokens)
	assert.Equal(t, 73, res.Usage.OutputTokens)
	assert.Equal(t, 62, res.Usage.ThinkingTokens)
	assert.Equal(t, 0, res.Usage.CacheReadTokens)
	assert.Equal(t, 13850, res.Usage.TotalTokens)
	assert.Equal(t, 13777, res.Usage.UncachedInputTokens())
	assert.Equal(t, 0.0, res.Usage.CacheHitRatio())
	assert.Empty(t, res.Error)
}

func TestParseOutput_TC_PARS_02_ConversationNotFoundStderr(t *testing.T) {
	stdout := []byte("")
	stderr := []byte(`warning: conversation "708ef346-15df-4175-9df0-0c82d5838eef" not found`)

	res, err := agy.ParseOutput(stdout, stderr)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrConversationNotFound), "expected ErrConversationNotFound, got %v", err)
}

func TestParseOutput_TC_PARS_03_MultipleJSONAndSurroundingLogs(t *testing.T) {
	stdout := []byte(`[INFO] Initializing agent environment...
{"level":"info","component":"bootstrap","msg":"session ready"}
[DEBUG] Running turn execution...
{"conversation_id":"c-999","status":"SUCCESS","response":"result text","duration_seconds":1.2,"usage":{"input_tokens":10,"output_tokens":5,"thinking_tokens":0,"total_tokens":15}}
[METRICS] finished in 1.2s
`)
	stderr := []byte("")

	res, err := agy.ParseOutput(stdout, stderr)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "c-999", res.ConversationID)
	assert.Equal(t, "result text", res.ResponseText)
	assert.Equal(t, 15, res.Usage.TotalTokens)
}

func TestParseOutput_TC_PARS_04_ANSITrueColorAndEscapeCodes(t *testing.T) {
	// ANSI escape codes: \x1b[38;2;255;100;0m (TrueColor), \x1b[1m (Bold), \x1b[0m (Reset), \x1b]0;Title\x07 (OSC)
	stdout := []byte("\x1b]0;Antigravity CLI\x07\x1b[38;2;255;100;0m[WARNING]\x1b[0m Loading dynamic skills...\n\x1b[1m{\"conversation_id\":\"c-ansi\",\"status\":\"SUCCESS\",\"response\":\"clean output text\\n\",\"duration_seconds\":2.0,\"usage\":{\"input_tokens\":50,\"output_tokens\":20,\"thinking_tokens\":5,\"total_tokens\":75}}\x1b[0m\n")
	stderr := []byte("\x1b[31m[INFO] Debug connection active\x1b[0m")

	res, err := agy.ParseOutput(stdout, stderr)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "c-ansi", res.ConversationID)
	assert.Equal(t, "clean output text\n", res.ResponseText)
}

func TestParseOutput_TC_PARS_05_CorruptedOrTruncatedJSON(t *testing.T) {
	testCases := []struct {
		name   string
		stdout []byte
		stderr []byte
	}{
		{
			name:   "Truncated JSON",
			stdout: []byte(`{"conversation_id":"123", "status":"SUCC`),
			stderr: []byte(""),
		},
		{
			name:   "Random Non-JSON Text",
			stdout: []byte("Fatal error: unexpected end of file on pipe"),
			stderr: []byte("exit code 1"),
		},
		{
			name:   "Empty Buffers",
			stdout: []byte(""),
			stderr: []byte(""),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := agy.ParseOutput(tc.stdout, tc.stderr)
			assert.Nil(t, res)
			require.Error(t, err)
			assert.True(t, errors.Is(err, ports.ErrOutputParse), "expected ErrOutputParse, got: %v", err)
		})
	}
}

func TestParseOutput_TC_PARS_06_ZeroOrNilTokenUsage(t *testing.T) {
	stdout := []byte(`{
		"conversation_id": "c-zero-usage",
		"status": "SUCCESS",
		"response": "Response without usage stats",
		"duration_seconds": 0.5,
		"usage": null
	}`)
	stderr := []byte("")

	res, err := agy.ParseOutput(stdout, stderr)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "c-zero-usage", res.ConversationID)
	assert.Equal(t, 0, res.Usage.TotalTokens)
	assert.Equal(t, 0, res.Usage.InputTokens)
}

func TestParseOutput_TC_PARS_07_StatusErrorWithConversationLoss(t *testing.T) {
	stdout := []byte(`{
		"conversation_id": "bad-id",
		"status": "ERROR",
		"response": "",
		"duration_seconds": 0.1,
		"error": "failed to load conversation transcript for bad-id"
	}`)
	stderr := []byte("ERROR: transcript not found")

	res, err := agy.ParseOutput(stdout, stderr)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrConversationNotFound), "expected ErrConversationNotFound, got %v", err)
}

func TestParseOutput_TC_PARS_08_StdoutSuccessWithWarningOnStderr(t *testing.T) {
	// If agy warns about an old conversation on stderr but successfully creates a new one on stdout
	stdout := []byte(`{
		"conversation_id": "new-conv-uuid-999",
		"status": "SUCCESS",
		"response": "Recovered turn response",
		"duration_seconds": 1.5,
		"usage": {"input_tokens": 100, "output_tokens": 30, "thinking_tokens": 10, "total_tokens": 140}
	}`)
	stderr := []byte(`warning: conversation "old-id" not found`)

	res, err := agy.ParseOutput(stdout, stderr)
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, "new-conv-uuid-999", res.ConversationID)
	assert.Equal(t, "Recovered turn response", res.ResponseText)
}

func TestParseOutput_TC_PARS_09_NativeDeniedActions(t *testing.T) {
	stdout := []byte(`{
		"conversation_id": "c-denied-123",
		"status": "DENIED",
		"response": "",
		"duration_seconds": 0.2,
		"denied_actions": [{"tool": "run_command", "reason": "unauthorized"}],
		"error": "headless permission denied"
	}`)
	stderr := []byte("")

	res, err := agy.ParseOutput(stdout, stderr)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "native permission denial")
	require.NotNil(t, res)
	assert.False(t, res.Success)
	assert.Equal(t, domain.StatusNativePermissionDenied, res.Outcome)
	assert.Equal(t, "native permission denial: headless permission denied", res.Error)
}

func TestParseOutput_TC_PARS_10_TTYError(t *testing.T) {
	stdout := []byte("CLI error: bubbletea: error opening TTY: bubbletea: could not open TTY: open /dev/tty: no such device or address\n")
	stderr := []byte("")

	res, err := agy.ParseOutput(stdout, stderr)
	assert.Nil(t, res)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrProcessExecution))
	assert.Contains(t, err.Error(), "headless environment blocked interactive TTY prompt")
}

