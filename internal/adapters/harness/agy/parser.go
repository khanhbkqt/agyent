package agy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var (
	// ansiRegex strips all terminal ANSI escape sequences (16/256/24-bit TrueColor, OSC, cursor commands)
	ansiRegex = regexp.MustCompile(`(?i)\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\].*?(\x07|\x1b\\)|\x1b[PX^_].*?\x1b\\|\x1b.`)

	// convNotFoundRegex matches all variants of missing/expired conversation or transcript warnings
	convNotFoundRegex = regexp.MustCompile(`(?i)(conversation.*not found|invalid conversation|failed to load conversation|no such conversation|transcript not found|conversation expired)`)
)

// StripANSI removes ANSI escape codes from string s.
func StripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

type agyJSONPayload struct {
	Status          string             `json:"status"` // "SUCCESS" | "ERROR"
	ConversationID  string             `json:"conversation_id"`
	Response        string             `json:"response"`
	DurationSeconds float64            `json:"duration_seconds"`
	NumTurns        int                `json:"num_turns"`
	Usage           *domain.TokenUsage `json:"usage"`
	Error           string             `json:"error,omitempty"`
}

// ParseOutput cleans ANSI escape sequences, extracts the last valid JSON envelope from stdout,
// inspects stderr for missing session notices, and converts to domain.ExecutionResult.
func ParseOutput(stdoutBytes, stderrBytes []byte) (*domain.ExecutionResult, error) {
	cleanStdout := ansiRegex.ReplaceAll(stdoutBytes, nil)
	cleanStderr := string(ansiRegex.ReplaceAll(stderrBytes, nil))

	// 1. Try to extract valid JSON payload from stdout first
	payload, err := extractLastJSONPayload(cleanStdout)
	if err != nil {
		// If stdout could not be parsed, check if conversation loss is signaled in stderr or stdout
		combinedText := strings.TrimSpace(cleanStderr + " " + string(cleanStdout))
		if convNotFoundRegex.MatchString(combinedText) {
			return nil, fmt.Errorf("%w: %s", ports.ErrConversationNotFound, combinedText)
		}
		return nil, fmt.Errorf("%w: stdout='%s', stderr='%s': %v", ports.ErrOutputParse, string(cleanStdout), cleanStderr, err)
	}

	// 2. If valid JSON was extracted but indicates an error status, check for conversation not found
	if payload.Status == "ERROR" || payload.Error != "" {
		errCombined := strings.TrimSpace(payload.Error + " " + cleanStderr)
		if convNotFoundRegex.MatchString(errCombined) {
			return nil, fmt.Errorf("%w: %s", ports.ErrConversationNotFound, payload.Error)
		}
	}

	usage := domain.TokenUsage{}
	if payload.Usage != nil {
		usage = *payload.Usage
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.ThinkingTokens
		}
	}

	return &domain.ExecutionResult{
		Success:        payload.Status == "SUCCESS" || (payload.Status == "" && payload.Error == ""),
		ConversationID: payload.ConversationID,
		ResponseText:   payload.Response,
		DurationSec:    payload.DurationSeconds,
		Usage:          usage,
		Error:          payload.Error,
	}, nil
}

// extractLastJSONPayload scans backwards from the end of the byte slice to locate the root JSON envelope.
func extractLastJSONPayload(data []byte) (*agyJSONPayload, error) {
	lastClose := bytes.LastIndexByte(data, '}')
	if lastClose == -1 {
		return nil, errors.New("no closing brace found")
	}

	// Scan backwards from lastClose - 1 down to 0
	for i := lastClose - 1; i >= 0; i-- {
		if data[i] == '{' {
			candidate := data[i : lastClose+1]

			// Fast heuristic pre-check: skip unmarshaling non-AGY JSON chunks to avoid O(N^2) CPU overhead
			if !bytes.Contains(candidate, []byte(`"conversation_id"`)) &&
				!bytes.Contains(candidate, []byte(`"status"`)) {
				continue
			}

			var p agyJSONPayload
			if err := json.Unmarshal(candidate, &p); err == nil && (p.ConversationID != "" || p.Status != "" || p.Response != "") {
				return &p, nil
			}
		}
	}

	return nil, errors.New("failed to find valid agy json envelope")
}
