package domain

import "time"

// EventType identifies the lifecycle and streaming event category.
type EventType string

const (
	// Lifecycle Events
	EventMessageReceived  EventType = "message.received"
	EventMessageDebounced EventType = "message.debounced"
	EventPreExecution     EventType = "execution.pre"
	EventPostExecution    EventType = "execution.post"
	EventArtifactDetected EventType = "artifact.detected"
	EventErrorOccurred    EventType = "error.occurred"

	// Real-Time Streaming Events
	EventStreamInit   EventType = "stream.init"
	EventStreamDelta  EventType = "stream.delta"
	EventStreamTool   EventType = "stream.tool"
	EventStreamResult EventType = "stream.result"
	EventStreamError  EventType = "stream.error"

	// Subagent Lifecycle & Coordination Events
	EventSubagentDispatched   EventType = "subagent.dispatched"
	EventSubagentProgress     EventType = "subagent.progress"
	EventSubagentWaitingInput EventType = "subagent.waiting_input"
	EventSubagentCompleted    EventType = "subagent.completed"
	EventSubagentFailed       EventType = "subagent.failed"
	EventSubagentCancelled    EventType = "subagent.cancelled"
)

// SubagentEventPayload carries subagent task lifecycle transitions on the EventBus.
type SubagentEventPayload struct {
	Task SubagentTask `json:"task"`
}

func (p SubagentEventPayload) GetSessionKey() string { return p.Task.ParentSessionKey }

// SessionScopedPayload is an optional interface implemented by payloads that belong to a specific session.
type SessionScopedPayload interface {
	GetSessionKey() string
}

// StreamInitPayload carries initialization metadata of a streaming session.
type StreamInitPayload struct {
	SessionKey     string    `json:"session_key"`
	ConversationID string    `json:"conversation_id"`
	CWD            string    `json:"cwd"`
	Tools          []string  `json:"tools"`
	Timestamp      time.Time `json:"timestamp"`
}

func (p StreamInitPayload) GetSessionKey() string { return p.SessionKey }

// StreamDeltaPayload carries an incremental token text delta from the LLM.
type StreamDeltaPayload struct {
	SessionKey     string `json:"session_key"`
	ConversationID string `json:"conversation_id"`
	StepIndex      int    `json:"step_index"`
	TextDelta      string `json:"text_delta"`
}

func (p StreamDeltaPayload) GetSessionKey() string { return p.SessionKey }

// StreamToolPayload carries the execution state change of an AGY tool.
type StreamToolPayload struct {
	SessionKey      string         `json:"session_key"`
	ConversationID  string         `json:"conversation_id"`
	StepIndex       int            `json:"step_index"`
	State           string         `json:"state"` // "ACTIVE", "DONE"
	ToolName        string         `json:"tool_name"`
	Parameters      map[string]any `json:"parameters,omitempty"`
	Output          any            `json:"output,omitempty"`
	DurationSeconds float64        `json:"duration_seconds,omitempty"`
}

func (p StreamToolPayload) GetSessionKey() string { return p.SessionKey }

// StreamResultPayload carries the final execution result summary of a turn.
type StreamResultPayload struct {
	SessionKey      string       `json:"session_key"`
	ConversationID  string       `json:"conversation_id"`
	Status          string       `json:"status"` // "SUCCESS", "ERROR"
	Response        string       `json:"response"`
	Error           string       `json:"error,omitempty"`
	DurationSeconds float64      `json:"duration_seconds"`
	NumTurns        int          `json:"num_turns,omitempty"`
	Usage           TokenUsage   `json:"usage"`
	Artifacts       []Attachment `json:"artifacts,omitempty"`
}

func (p StreamResultPayload) GetSessionKey() string { return p.SessionKey }

// StreamErrorPayload carries mid-stream abrupt failure details.
type StreamErrorPayload struct {
	SessionKey     string `json:"session_key"`
	ConversationID string `json:"conversation_id"`
	Error          string `json:"error"`
}

func (p StreamErrorPayload) GetSessionKey() string { return p.SessionKey }

// Event represents an internal system event broadcast across the application.
type Event struct {
	Type      EventType `json:"type"`
	Payload   any       `json:"payload"`
	Timestamp time.Time `json:"timestamp"`
}

// NewEvent constructs a new Event with current timestamp.
func NewEvent(eventType EventType, payload any) Event {
	return Event{
		Type:      eventType,
		Payload:   payload,
		Timestamp: time.Now(),
	}
}

// Channel extracts the channel prefix (e.g. "telegram") from session-scoped payloads if available.
func (e Event) Channel() string {
	if p, ok := e.Payload.(SessionScopedPayload); ok {
		key := p.GetSessionKey()
		for i := 0; i < len(key); i++ {
			if key[i] == ':' {
				return key[:i]
			}
		}
		return key
	}
	return ""
}
