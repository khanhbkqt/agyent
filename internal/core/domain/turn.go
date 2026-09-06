package domain

import (
	"time"
)

// TurnStatus defines the lifecycle state of an in-flight conversation turn.
type TurnStatus string

const (
	TurnStatusPending          TurnStatus = "PENDING"
	TurnStatusExecuting        TurnStatus = "EXECUTING"
	TurnStatusWaitingApproval  TurnStatus = "WAITING_APPROVAL"
	TurnStatusInterruptedCrash TurnStatus = "INTERRUPTED_CRASH"
	TurnStatusRecovering       TurnStatus = "RECOVERING"
	TurnStatusCompleted        TurnStatus = "COMPLETED"
	TurnStatusFailed           TurnStatus = "FAILED"
)

// InFlightTurn represents an in-flight conversation turn tracked in SQLite for crash resilience and auto-recovery.
type InFlightTurn struct {
	TurnID           string     `json:"turn_id"`
	SessionKey       string     `json:"session_key"`
	ConversationID   string     `json:"conversation_id"`
	AgentName        string     `json:"agent_name"`
	ProjectName      string     `json:"project_name"`
	Channel          string     `json:"channel"`
	ChatID           string     `json:"chat_id"`
	ThreadID         string     `json:"thread_id,omitempty"`
	InboundMessageID int64      `json:"inbound_message_id"`
	BotID            int64      `json:"bot_id"`
	UserID           string     `json:"user_id"`
	UserName         string     `json:"user_name,omitempty"`
	Prompt           string     `json:"prompt"`
	IsEphemeral      bool       `json:"is_ephemeral"`
	Status           TurnStatus `json:"status"`
	RetryCount       int        `json:"retry_count"`
	MaxRetries       int        `json:"max_retries"`
	RecoveryMode     string     `json:"recovery_mode"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// IsTerminal returns true if the in-flight turn has reached a terminal status.
func (t *InFlightTurn) IsTerminal() bool {
	return t.Status == TurnStatusCompleted || t.Status == TurnStatusFailed
}
