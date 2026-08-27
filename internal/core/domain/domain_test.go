package domain_test

import (
	"testing"
	"time"

	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
)

func TestFormatSessionKey(t *testing.T) {
	tests := []struct {
		name     string
		channel  string
		chatID   string
		threadID int64
		botID    int64
		expected string
	}{
		{
			name:     "Private Chat",
			channel:  "telegram",
			chatID:   "123456789",
			threadID: 0,
			botID:    0,
			expected: "telegram:123456789",
		},
		{
			name:     "Group Chat Without Thread",
			channel:  "telegram",
			chatID:   "-1001987654321",
			threadID: 0,
			botID:    0,
			expected: "telegram:-1001987654321",
		},
		{
			name:     "Supergroup Forum Topic",
			channel:  "telegram",
			chatID:   "-1001987654321",
			threadID: 42,
			botID:    0,
			expected: "telegram:-1001987654321:42",
		},
		{
			name:     "Namespaced with botID",
			channel:  "telegram",
			chatID:   "123456789",
			threadID: 0,
			botID:    987654321,
			expected: "telegram:987654321:123456789",
		},
		{
			name:     "Namespaced with botID and threadID",
			channel:  "telegram",
			chatID:   "-1001987654321",
			threadID: 42,
			botID:    987654321,
			expected: "telegram:987654321:-1001987654321:42",
		},
		{
			name:     "Trims and Normalizes",
			channel:  " TELEGRAM ",
			chatID:   " 987654 ",
			threadID: 10,
			botID:    0,
			expected: "telegram:987654:10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result string
			if tt.botID > 0 {
				result = domain.FormatSessionKey(tt.channel, tt.chatID, tt.threadID, tt.botID)
			} else {
				result = domain.FormatSessionKey(tt.channel, tt.chatID, tt.threadID)
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractChatIDFromSessionKey(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
		expected   string
	}{
		{
			name:       "Legacy 2-part key",
			sessionKey: "telegram:123456",
			expected:   "123456",
		},
		{
			name:       "Legacy 3-part key with negative group ID",
			sessionKey: "telegram:-100123456:42",
			expected:   "-100123456",
		},
		{
			name:       "Namespaced 3-part key",
			sessionKey: "telegram:987654321:123456",
			expected:   "123456",
		},
		{
			name:       "Namespaced 3-part key with negative group chat",
			sessionKey: "telegram:987654321:-100987654",
			expected:   "-100987654",
		},
		{
			name:       "Namespaced 4-part key with group topic",
			sessionKey: "telegram:987654321:-100987654:42",
			expected:   "-100987654",
		},
		{
			name:       "Invalid/single part fallback",
			sessionKey: "single_string",
			expected:   "single_string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chatID := domain.ExtractChatIDFromSessionKey(tc.sessionKey)
			assert.Equal(t, tc.expected, chatID)
		})
	}
}

func TestParseSessionKey(t *testing.T) {
	tests := []struct {
		name       string
		sessionKey string
		expected   domain.ParsedSessionKey
		wantErr    bool
	}{
		{
			name:       "Legacy 2-part key private chat",
			sessionKey: "telegram:8544450322",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    0,
				ChatID:   "8544450322",
				ThreadID: 0,
			},
		},
		{
			name:       "Legacy 2-part key group chat",
			sessionKey: "telegram:-100123456",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    0,
				ChatID:   "-100123456",
				ThreadID: 0,
			},
		},
		{
			name:       "Namespaced 3-part key private chat (User Bug Case)",
			sessionKey: "telegram:8718145628:8544450322",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    8718145628,
				ChatID:   "8544450322",
				ThreadID: 0,
			},
		},
		{
			name:       "Namespaced 3-part key group chat",
			sessionKey: "telegram:8718145628:-100123456",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    8718145628,
				ChatID:   "-100123456",
				ThreadID: 0,
			},
		},
		{
			name:       "Legacy 3-part key group topic",
			sessionKey: "telegram:-100123456:10042",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    0,
				ChatID:   "-100123456",
				ThreadID: 10042,
			},
		},
		{
			name:       "Legacy 3-part key with explicit 0 threadID",
			sessionKey: "telegram:123456:0",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    0,
				ChatID:   "123456",
				ThreadID: 0,
			},
		},
		{
			name:       "Namespaced 4-part key group topic",
			sessionKey: "telegram:8718145628:-100123456:10042",
			expected: domain.ParsedSessionKey{
				Channel:  "telegram",
				BotID:    8718145628,
				ChatID:   "-100123456",
				ThreadID: 10042,
			},
		},
		{
			name:       "Invalid single string key",
			sessionKey: "invalidkey",
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := domain.ParseSessionKey(tc.sessionKey)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, parsed)
			}
		})
	}
}

func TestAgent_OwnershipAndPermissions(t *testing.T) {
	agent := domain.Agent{
		Name:          "dev_architect",
		Description:   "Senior Dev Architect",
		Status:        domain.StatusInitialized,
		WorkspacePath: "/home/user/.agyent/workspace-dev_architect",
		OwnerID:       "1001",
		IsPublic:      false,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	assert.Equal(t, "1001", agent.OwnerID)
	assert.False(t, agent.IsPublic)

	perm := domain.AgentPermission{
		AgentName: "dev_architect",
		UserID:    "2002",
		Role:      "operator",
		GrantedBy: "1001",
		GrantedAt: time.Now(),
	}

	assert.Equal(t, "operator", perm.Role)
	assert.Equal(t, "2002", perm.UserID)
}

func TestFormatProjectID(t *testing.T) {
	tests := []struct {
		name        string
		agentName   string
		projectName string
		expected    string
	}{
		{
			name:        "Standard Agent and Project",
			agentName:   "dev_expert",
			projectName: "backend-core",
			expected:    "dev_expert:backend-core",
		},
		{
			name:        "Whitespace Trimming",
			agentName:   "  dev_expert  ",
			projectName: "  my-app  ",
			expected:    "dev_expert:my-app",
		},
		{
			name:        "Internal Whitespaces and Colons Sanitized",
			agentName:   "dev expert:v1",
			projectName: "ecommerce backend @ 2026",
			expected:    "dev_expert_v1:ecommerce_backend_2026",
		},
		{
			name:        "Fallback for Empty Inputs",
			agentName:   "   ",
			projectName: " !!! ",
			expected:    "default_agent:default_project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domain.FormatProjectID(tt.agentName, tt.projectName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCanonicalMessage_Helpers(t *testing.T) {
	t.Run("CleanText", func(t *testing.T) {
		msg := domain.CanonicalMessage{
			Text: "  hello world!   ",
		}
		assert.Equal(t, "hello world!", msg.CleanText())
	})

	t.Run("IsCommand", func(t *testing.T) {
		tests := []struct {
			text      string
			isCommand bool
		}{
			{"/help", true},
			{"  /status  ", true},
			{"hello /help", false},
			{"", false},
			{"   ", false},
		}

		for _, tt := range tests {
			msg := domain.CanonicalMessage{Text: tt.text}
			assert.Equal(t, tt.isCommand, msg.IsCommand(), "text: %q", tt.text)
		}
	})

	t.Run("CommandArgs", func(t *testing.T) {
		tests := []struct {
			name         string
			text         string
			expectedCmd  string
			expectedArgs []string
		}{
			{
				name:         "Simple Command No Args",
				text:         "/help",
				expectedCmd:  "/help",
				expectedArgs: []string{},
			},
			{
				name:         "Command With Multiple Args",
				text:         "/create my_agent A helpful dev agent",
				expectedCmd:  "/create",
				expectedArgs: []string{"my_agent", "A", "helpful", "dev", "agent"},
			},
			{
				name:         "Command With Bot Suffix",
				text:         "/p@agyent_bot build the project",
				expectedCmd:  "/p",
				expectedArgs: []string{"build", "the", "project"},
			},
			{
				name:         "Non-Command Text",
				text:         "just regular message",
				expectedCmd:  "",
				expectedArgs: nil,
			},
			{
				name:         "Empty Text",
				text:         "",
				expectedCmd:  "",
				expectedArgs: nil,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				msg := domain.CanonicalMessage{Text: tt.text}
				cmd, args := msg.CommandArgs()
				assert.Equal(t, tt.expectedCmd, cmd)
				assert.Equal(t, tt.expectedArgs, args)
			})
		}
	})

	t.Run("SessionKey", func(t *testing.T) {
		msg := domain.CanonicalMessage{
			Channel: "telegram",
			Chat: domain.ChatContext{
				ID:       "-10011223344",
				ThreadID: 15,
			},
		}
		assert.Equal(t, "telegram:-10011223344:15", msg.SessionKey())
	})
}

func TestSession_ConversationIDs(t *testing.T) {
	session := &domain.Session{
		ActiveAgent:           "dev_expert",
		GlobalConversationID:  "conv-global-123",
		ProjectConversationID: "conv-proj-456",
		UpdatedAt:             time.Now(),
	}

	// In Global mode (no active project)
	assert.Equal(t, "conv-global-123", session.GetActiveConversationID())
	session.SetActiveConversationID("conv-global-new")
	assert.Equal(t, "conv-global-new", session.GlobalConversationID)

	// Switch to Project mode
	session.ActiveProject = "my-project"
	assert.Equal(t, "conv-proj-456", session.GetActiveConversationID())
	session.SetActiveConversationID("conv-proj-new")
	assert.Equal(t, "conv-proj-new", session.ProjectConversationID)

	// Reset in Project mode
	session.ResetActiveConversationID()
	assert.Equal(t, "", session.ProjectConversationID)
	assert.Equal(t, "conv-global-new", session.GlobalConversationID)

	// Reset in Global mode
	session.ActiveProject = ""
	session.ResetActiveConversationID()
	assert.Equal(t, "", session.GlobalConversationID)
}

func TestAgent_IsInitialized(t *testing.T) {
	uninit := domain.Agent{Status: domain.StatusUninitialized}
	assert.False(t, uninit.IsInitialized())

	initAgent := domain.Agent{Status: domain.StatusInitialized}
	assert.True(t, initAgent.IsInitialized())
}

func TestExecutionResult_Artifacts(t *testing.T) {
	res := domain.ExecutionResult{
		Success:        true,
		ConversationID: "conv-123",
		ResponseText:   "Analysis complete",
		DurationSec:    2.5,
		Usage: domain.TokenUsage{
			InputTokens:    100,
			OutputTokens:   50,
			ThinkingTokens: 20,
			TotalTokens:    170,
		},
		Artifacts: []domain.Attachment{
			{
				ID:       "exports/report.pdf",
				FileName: "report.pdf",
				FilePath: "/workspace/exports/report.pdf",
				MIMEType: "application/pdf",
				Size:     1024,
				Type:     "document",
			},
		},
	}

	assert.True(t, res.Success)
	assert.Equal(t, "conv-123", res.ConversationID)
	assert.Len(t, res.Artifacts, 1)
	assert.Equal(t, "report.pdf", res.Artifacts[0].FileName)
}

func TestEvent_NewEvent(t *testing.T) {
	event := domain.NewEvent(domain.EventArtifactDetected, "some payload")
	assert.Equal(t, domain.EventArtifactDetected, event.Type)
	assert.Equal(t, "some payload", event.Payload)
	assert.False(t, event.Timestamp.IsZero())
}
