package evolution

import (
	"testing"

	"agyent/internal/core/domain"
)

func TestTaskStateGuard_EvaluateTaskState(t *testing.T) {
	guard := NewTaskStateGuard()

	tests := []struct {
		name     string
		snapshot domain.ConversationSnapshot
		expected domain.TaskProgressState
	}{
		{
			name: "Ephemeral /ask command",
			snapshot: domain.ConversationSnapshot{
				Turns: []domain.CanonicalMessage{
					{Text: "/ask how does sqlite wal work"},
				},
			},
			expected: domain.TaskStateEphemeral,
		},
		{
			name: "Error status in last turn",
			snapshot: domain.ConversationSnapshot{
				LastTurnStatus: "ERROR",
				Turns: []domain.CanonicalMessage{
					{Text: "fix the bug"},
				},
			},
			expected: domain.TaskStateInProgressPaused,
		},
		{
			name: "Error in audit entry",
			snapshot: domain.ConversationSnapshot{
				AuditEntries: []domain.AuditLog{
					{Status: "ERROR", ErrorMessage: "exit status 1"},
				},
			},
			expected: domain.TaskStateInProgressPaused,
		},
		{
			name: "Agent has open question pending user reply",
			snapshot: domain.ConversationSnapshot{
				HasOpenQuestion: true,
				Turns: []domain.CanonicalMessage{
					{Text: "what do you think?"},
				},
			},
			expected: domain.TaskStateInProgressPaused,
		},
		{
			name: "Completed normal session",
			snapshot: domain.ConversationSnapshot{
				LastTurnStatus: "SUCCESS",
				AuditEntries: []domain.AuditLog{
					{Status: "SUCCESS"},
				},
				Turns: []domain.CanonicalMessage{
					{Text: "Done with refactor."},
				},
			},
			expected: domain.TaskStateCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := guard.EvaluateTaskState(tt.snapshot)
			if res != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, res)
			}
		})
	}
}
