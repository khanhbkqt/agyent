package agy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// StreamEvent represents a single raw NDJSON line from AGY CLI stream.
type StreamEvent struct {
	Event          string           `json:"event"`
	ConversationID string           `json:"conversation_id,omitempty"`
	Init           *InitPayload     `json:"init,omitempty"`
	StepUpdate     *StepUpdateEvent `json:"step_update,omitempty"`
	Result         *ResultPayload   `json:"result,omitempty"`
	Error          string           `json:"error,omitempty"`
}

type InitPayload struct {
	CWD            string   `json:"cwd"`
	Tools          []string `json:"tools"`
	PermissionMode string   `json:"permission_mode"`
}

type StepUpdateEvent struct {
	ConversationID  string                 `json:"conversation_id"`
	StepIndex       int                    `json:"step_index"`
	State           string                 `json:"state"`     // "ACTIVE", "DONE"
	StepType        string                 `json:"step_type"` // "tool", "agent_response", "checkpoint", "user_input"
	ToolName        string                 `json:"tool_name,omitempty"`
	TextDelta       string                 `json:"text_delta,omitempty"`
	DurationSeconds float64                `json:"duration_seconds,omitempty"`
	ToolInfo        *ToolInfoPayload       `json:"tool_info,omitempty"`
	Usage           map[string]interface{} `json:"usage,omitempty"`
}

type ToolInfoPayload struct {
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters"`
	Output     interface{}            `json:"output,omitempty"`
}

type ResultPayload struct {
	ConversationID  string            `json:"conversation_id"`
	Status          string            `json:"status"` // "SUCCESS", "ERROR"
	Response        string            `json:"response"`
	Error           string            `json:"error,omitempty"`
	DurationSeconds float64           `json:"duration_seconds"`
	NumTurns        int               `json:"num_turns"`
	Usage           domain.TokenUsage `json:"usage"`
}

// StreamParser reads NDJSON lines from an io.Reader and translates them into domain.Events on the EventBus.
type StreamParser struct {
	eventBus         ports.EventBusPort
	artifactDetector func() []domain.Attachment
}

// NewStreamParser creates a new StreamParser instance.
func NewStreamParser(bus ports.EventBusPort) *StreamParser {
	return &StreamParser{
		eventBus: bus,
	}
}

// SetArtifactDetector sets an optional callback function to detect created or modified artifacts before emitting EventStreamResult.
func (p *StreamParser) SetArtifactDetector(fn func() []domain.Attachment) {
	p.artifactDetector = fn
}

// ParseAndEmitStream processes NDJSON lines from reader and dispatches domain events until EOF.
// It supports large NDJSON lines up to 10MB (to prevent bufio.ErrTooLong on large tool/diff outputs).
func (p *StreamParser) ParseAndEmitStream(ctx context.Context, sessionKey string, reader io.Reader) (*domain.StreamResultPayload, error) {
	if reader == nil {
		return nil, errors.New("stream parser: reader is nil")
	}

	scanner := bufio.NewScanner(reader)
	// Allocate initial 64KB buffer and allow growth up to 10MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var lastResult *domain.StreamResultPayload
	var conversationID string
	hasResult := false

	for scanner.Scan() {
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Strip ANSI escape codes if present
		cleanLine := StripANSI(line)
		if !strings.HasPrefix(cleanLine, "{") {
			continue
		}

		var rawEvt StreamEvent
		if err := json.Unmarshal([]byte(cleanLine), &rawEvt); err != nil {
			// Skip corrupted non-JSON diagnostic lines
			continue
		}

		if rawEvt.ConversationID != "" {
			conversationID = rawEvt.ConversationID
		}

		switch rawEvt.Event {
		case "init":
			cwd := ""
			var tools []string
			if rawEvt.Init != nil {
				cwd = rawEvt.Init.CWD
				tools = rawEvt.Init.Tools
			}
			initPayload := domain.StreamInitPayload{
				SessionKey:     sessionKey,
				ConversationID: conversationID,
				CWD:            cwd,
				Tools:          tools,
				Timestamp:      time.Now(),
			}
			if p.eventBus != nil {
				_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamInit, initPayload))
			}

		case "step_update":
			if rawEvt.StepUpdate != nil {
				step := rawEvt.StepUpdate
				if step.ConversationID != "" {
					conversationID = step.ConversationID
				}

				if step.StepType == "agent_response" && step.TextDelta != "" {
					deltaPayload := domain.StreamDeltaPayload{
						SessionKey:     sessionKey,
						ConversationID: conversationID,
						StepIndex:      step.StepIndex,
						TextDelta:      step.TextDelta,
					}
					if p.eventBus != nil {
						_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamDelta, deltaPayload))
					}
				} else if step.StepType == "tool" {
					var params map[string]any
					var output any
					toolName := step.ToolName
					if step.ToolInfo != nil {
						if toolName == "" {
							toolName = step.ToolInfo.Name
						}
						params = step.ToolInfo.Parameters
						output = step.ToolInfo.Output
					}
					toolPayload := domain.StreamToolPayload{
						SessionKey:      sessionKey,
						ConversationID:  conversationID,
						StepIndex:       step.StepIndex,
						State:           step.State,
						ToolName:        toolName,
						Parameters:      params,
						Output:          output,
						DurationSeconds: step.DurationSeconds,
					}
					if p.eventBus != nil {
						_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamTool, toolPayload))
					}
				}
			}

		case "result":
			hasResult = true
			if rawEvt.Result != nil {
				res := rawEvt.Result
				if res.ConversationID != "" {
					conversationID = res.ConversationID
				}
				usage := res.Usage
				if usage.TotalTokens == 0 {
					usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.ThinkingTokens
				}
				var artifacts []domain.Attachment
				if p.artifactDetector != nil {
					artifacts = p.artifactDetector()
				}
				lastResult = &domain.StreamResultPayload{
					SessionKey:      sessionKey,
					ConversationID:  conversationID,
					Status:          res.Status,
					Response:        res.Response,
					Error:           res.Error,
					DurationSeconds: res.DurationSeconds,
					NumTurns:        res.NumTurns,
					Usage:           usage,
					Artifacts:       artifacts,
				}
				if p.eventBus != nil {
					_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamResult, *lastResult))
				}
			}

		case "error":
			hasResult = true
			errPayload := domain.StreamErrorPayload{
				SessionKey:     sessionKey,
				ConversationID: conversationID,
				Error:          rawEvt.Error,
			}
			if p.eventBus != nil {
				_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamError, errPayload))
			}
			if convNotFoundRegex.MatchString(rawEvt.Error) {
				return nil, fmt.Errorf("%w: %s", ports.ErrConversationNotFound, rawEvt.Error)
			}
			return nil, fmt.Errorf("stream execution failed: %s", rawEvt.Error)
		}
	}

	if err := scanner.Err(); err != nil {
		if p.eventBus != nil {
			_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamError, domain.StreamErrorPayload{
				SessionKey:     sessionKey,
				ConversationID: conversationID,
				Error:          fmt.Sprintf("scanner error: %v", err),
			}))
		}
		return nil, fmt.Errorf("stream scanner error: %w", err)
	}

	// Mid-stream crash guard: If stream completed without a result event
	if !hasResult {
		errPayload := domain.StreamErrorPayload{
			SessionKey:     sessionKey,
			ConversationID: conversationID,
			Error:          "subprocess stream terminated abruptly without result event",
		}
		if p.eventBus != nil {
			_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamError, errPayload))
		}
		return nil, errors.New("stream parser: incomplete stream without result event")
	}

	return lastResult, nil
}
