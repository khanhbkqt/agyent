package evolution

import (
	"strings"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.TaskStateGuardPort = (*TaskStateGuard)(nil)

// TaskStateGuard evaluates if a conversation snapshot represents a completed unit of work or is paused in-progress.
type TaskStateGuard struct{}

// NewTaskStateGuard constructs a new TaskStateGuard instance.
func NewTaskStateGuard() *TaskStateGuard {
	return &TaskStateGuard{}
}

// EvaluateTaskState checks turn status, trailing questions, and command types to prevent premature reflection.
func (g *TaskStateGuard) EvaluateTaskState(snapshot domain.ConversationSnapshot) domain.TaskProgressState {
	// 1. Check if the conversation is an ephemeral turn (/ask)
	if len(snapshot.Turns) > 0 {
		firstTurn := snapshot.Turns[0]
		if firstTurn.IsCommand() {
			cmd, _ := firstTurn.CommandArgs()
			if strings.EqualFold(cmd, "/ask") {
				return domain.TaskStateEphemeral
			}
		}
	}

	// 2. Check if the last turn ended in a failure/error that hasn't been fixed yet
	if strings.EqualFold(snapshot.LastTurnStatus, "ERROR") {
		return domain.TaskStateInProgressPaused
	}

	// Check the latest audit log entry if available
	if len(snapshot.AuditEntries) > 0 {
		lastAudit := snapshot.AuditEntries[len(snapshot.AuditEntries)-1]
		if strings.EqualFold(lastAudit.Status, "ERROR") {
			return domain.TaskStateInProgressPaused
		}
	}

	// 3. Check if the trailing turn from the agent is an open question waiting for user input
	if len(snapshot.Turns) > 0 {
		lastTurn := snapshot.Turns[len(snapshot.Turns)-1]
		text := strings.TrimSpace(lastTurn.Text)
		if strings.HasSuffix(text, "?") || strings.HasSuffix(text, "không?") || strings.HasSuffix(text, "chứ?") {
			// If the user's last message is an open question, it's normal.
			// If the snapshot flag says agent is waiting for clarification, pause it.
			if snapshot.HasOpenQuestion {
				return domain.TaskStateInProgressPaused
			}
		}
	}

	if snapshot.HasOpenQuestion {
		return domain.TaskStateInProgressPaused
	}

	return domain.TaskStateCompleted
}
