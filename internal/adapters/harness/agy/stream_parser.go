package agy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
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
	eventBus              ports.EventBusPort
	artifactDetector      func() []domain.Attachment
	onMilestone           func()
	turnID                string
	toolHeartbeatInterval time.Duration
}

// NewStreamParser creates a new StreamParser instance.
func NewStreamParser(bus ports.EventBusPort) *StreamParser {
	return &StreamParser{
		eventBus: bus,
	}
}

// SetTurnID assigns a turn identifier to all emitted stream events.
func (p *StreamParser) SetTurnID(turnID string) {
	p.turnID = turnID
}

// SetArtifactDetector sets an optional callback function to detect created or modified artifacts before emitting EventStreamResult.
func (p *StreamParser) SetArtifactDetector(fn func() []domain.Attachment) {
	p.artifactDetector = fn
}

// SetOnMilestone registers an optional callback that gets called on major milestone stream events (init, tool calls, step completions, and result).
func (p *StreamParser) SetOnMilestone(fn func()) {
	p.onMilestone = fn
}

// SetToolHeartbeatInterval sets an optional custom interval for tool progress heartbeat pings (defaults to 10s).
func (p *StreamParser) SetToolHeartbeatInterval(d time.Duration) {
	p.toolHeartbeatInterval = d
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

	var (
		activeToolMu     sync.Mutex
		activeToolCancel context.CancelFunc
	)

	stopActiveToolHeartbeat := func() {
		activeToolMu.Lock()
		defer activeToolMu.Unlock()
		if activeToolCancel != nil {
			activeToolCancel()
			activeToolCancel = nil
		}
	}
	defer stopActiveToolHeartbeat()

	hbInterval := p.toolHeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = 10 * time.Second
	}

	startActiveToolHeartbeat := func(toolName string) {
		activeToolMu.Lock()
		defer activeToolMu.Unlock()
		if activeToolCancel != nil {
			activeToolCancel()
		}
		var hbCtx context.Context
		hbCtx, activeToolCancel = context.WithCancel(ctx)
		go func() {
			ticker := time.NewTicker(hbInterval)
			defer ticker.Stop()
			for {
				select {
				case <-hbCtx.Done():
					return
				case <-ticker.C:
					slog.DebugContext(hbCtx, "Emitting silent tool progress heartbeat ping",
						"tool", toolName, "turn_id", p.turnID, "session_key", sessionKey)
					if p.onMilestone != nil {
						p.onMilestone()
					}
				}
			}
		}()
	}

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
			stopActiveToolHeartbeat()
			if p.onMilestone != nil {
				p.onMilestone()
			}
			cwd := ""
			var tools []string
			if rawEvt.Init != nil {
				cwd = rawEvt.Init.CWD
				tools = rawEvt.Init.Tools
			}
			initPayload := domain.StreamInitPayload{
				SessionKey:     sessionKey,
				ConversationID: conversationID,
				TurnID:         p.turnID,
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

				// Reset watchdog on tool executions or step completion milestones
				if p.onMilestone != nil && (step.StepType == "tool" || step.State == "DONE") {
					p.onMilestone()
				}

				if step.StepType == "agent_response" && step.TextDelta != "" {
					stopActiveToolHeartbeat()
					deltaPayload := domain.StreamDeltaPayload{
						SessionKey:     sessionKey,
						ConversationID: conversationID,
						TurnID:         p.turnID,
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

					if step.State == "DONE" {
						stopActiveToolHeartbeat()
					} else {
						startActiveToolHeartbeat(toolName)
					}

					toolPayload := domain.StreamToolPayload{
						SessionKey:      sessionKey,
						ConversationID:  conversationID,
						TurnID:          p.turnID,
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
				} else {
					stopActiveToolHeartbeat()
				}
			}

		case "result":
			stopActiveToolHeartbeat()
			hasResult = true
			if p.onMilestone != nil {
				p.onMilestone()
			}
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
					TurnID:          p.turnID,
					Status:          res.Status,
					Response:        res.Response,
					Error:           res.Error,
					DurationSeconds: res.DurationSeconds,
					NumTurns:        res.NumTurns,
					Usage:           usage,
					Artifacts:       artifacts,
				}
				if p.eventBus != nil {
					if res.Status == "INTERRUPTED" {
						_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamInterrupted, domain.StreamInterruptedPayload{
							SessionKey:     sessionKey,
							ConversationID: conversationID,
							TurnID:         p.turnID,
							Reason:         "Preempted by incoming user message",
							Timestamp:      time.Now(),
						}))
					} else {
						_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamResult, *lastResult))
					}
				}
			} else {
				lastResult = &domain.StreamResultPayload{
					SessionKey:     sessionKey,
					ConversationID: conversationID,
					TurnID:         p.turnID,
					Status:         "SUCCESS",
				}
				if p.eventBus != nil {
					_ = p.eventBus.SyncEmit(ctx, domain.NewEvent(domain.EventStreamResult, *lastResult))
				}
			}
			return lastResult, nil

		case "error":
			hasResult = true
			errPayload := domain.StreamErrorPayload{
				SessionKey:     sessionKey,
				ConversationID: conversationID,
				TurnID:         p.turnID,
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
