package domain_test

import (
	"testing"
	"time"

	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
)

func TestDomainEvents_ConstructorsAndPayloads(t *testing.T) {
	// StreamInitPayload
	initPayload := domain.StreamInitPayload{
		SessionKey:     "telegram:123456",
		ConversationID: "conv-123",
		CWD:            "/workspace",
		Tools:          []string{"view_file", "run_command"},
		Timestamp:      time.Now(),
	}
	evtInit := domain.NewEvent(domain.EventStreamInit, initPayload)
	assert.Equal(t, domain.EventStreamInit, evtInit.Type)
	assert.Equal(t, "telegram", evtInit.Channel())
	assert.Equal(t, "telegram:123456", initPayload.GetSessionKey())

	// StreamDeltaPayload
	deltaPayload := domain.StreamDeltaPayload{
		SessionKey:     "telegram:123456:10",
		ConversationID: "conv-123",
		StepIndex:      3,
		TextDelta:      "Hello world",
	}
	evtDelta := domain.NewEvent(domain.EventStreamDelta, deltaPayload)
	assert.Equal(t, domain.EventStreamDelta, evtDelta.Type)
	assert.Equal(t, "telegram", evtDelta.Channel())
	assert.Equal(t, "telegram:123456:10", deltaPayload.GetSessionKey())

	// StreamToolPayload
	toolPayload := domain.StreamToolPayload{
		SessionKey:      "discord:999",
		ConversationID:  "conv-456",
		StepIndex:       4,
		State:           "ACTIVE",
		ToolName:        "generate_image",
		DurationSeconds: 1.5,
	}
	evtTool := domain.NewEvent(domain.EventStreamTool, toolPayload)
	assert.Equal(t, domain.EventStreamTool, evtTool.Type)
	assert.Equal(t, "discord", evtTool.Channel())

	// StreamResultPayload
	resultPayload := domain.StreamResultPayload{
		SessionKey:      "telegram:123",
		ConversationID:  "conv-123",
		Status:          "SUCCESS",
		Response:        "Done!",
		DurationSeconds: 2.0,
		Usage: domain.TokenUsage{
			TotalTokens: 150,
		},
	}
	evtResult := domain.NewEvent(domain.EventStreamResult, resultPayload)
	assert.Equal(t, domain.EventStreamResult, evtResult.Type)
	assert.Equal(t, "telegram", evtResult.Channel())

	// StreamErrorPayload
	errPayload := domain.StreamErrorPayload{
		SessionKey:     "telegram:123",
		ConversationID: "conv-123",
		Error:          "process terminated unexpectedly",
	}
	evtErr := domain.NewEvent(domain.EventStreamError, errPayload)
	assert.Equal(t, domain.EventStreamError, evtErr.Type)
	assert.Equal(t, "telegram", evtErr.Channel())

	// Non-session scoped event
	genericEvt := domain.NewEvent(domain.EventErrorOccurred, "generic error string")
	assert.Equal(t, "", genericEvt.Channel())
}
