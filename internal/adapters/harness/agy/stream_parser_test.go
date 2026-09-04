package agy_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"agyent/internal/adapters/harness/agy"
	"agyent/internal/core/domain"
	"agyent/internal/core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestStreamParser_TC_BRG_01_To_04(t *testing.T) {
	defer goleak.VerifyNone(t)

	bus := eventbus.NewEventBus(100, 2)
	defer bus.Close()

	parser := agy.NewStreamParser(bus)

	t.Run("TC-BRG-01_StreamInitAndDeltaAndToolAndResult", func(t *testing.T) {
		var (
			mu        sync.Mutex
			initEvt   *domain.StreamInitPayload
			deltas    []string
			toolEvts  []domain.StreamToolPayload
			resultEvt *domain.StreamResultPayload
		)

		bus.SubscribeSync(domain.EventStreamInit, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			p := evt.Payload.(domain.StreamInitPayload)
			initEvt = &p
			mu.Unlock()
			return nil
		})

		bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			p := evt.Payload.(domain.StreamDeltaPayload)
			deltas = append(deltas, p.TextDelta)
			mu.Unlock()
			return nil
		})

		bus.SubscribeSync(domain.EventStreamTool, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			p := evt.Payload.(domain.StreamToolPayload)
			toolEvts = append(toolEvts, p)
			mu.Unlock()
			return nil
		})

		bus.SubscribeSync(domain.EventStreamResult, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			p := evt.Payload.(domain.StreamResultPayload)
			resultEvt = &p
			mu.Unlock()
			return nil
		})

		sampleNDJSON := `
{"event":"init","conversation_id":"c-100","init":{"cwd":"/app","tools":["list_dir","generate_image"]}}
{"event":"step_update","step_update":{"conversation_id":"c-100","step_index":1,"step_type":"tool","state":"ACTIVE","tool_name":"list_dir","tool_info":{"parameters":{"DirectoryPath":"/app"}}}}
{"event":"step_update","step_update":{"conversation_id":"c-100","step_index":1,"step_type":"tool","state":"DONE","tool_name":"list_dir","duration_seconds":0.05,"tool_info":{"output":"main.go\n"}}}
{"event":"step_update","step_update":{"conversation_id":"c-100","step_index":2,"step_type":"agent_response","text_delta":"Dưới đây "}}
{"event":"step_update","step_update":{"conversation_id":"c-100","step_index":2,"step_type":"agent_response","text_delta":"là kết quả:"}}
{"event":"result","result":{"conversation_id":"c-100","status":"SUCCESS","response":"Dưới đây là kết quả:","duration_seconds":1.2,"usage":{"input_tokens":100,"output_tokens":40,"thinking_tokens":10,"cache_read_tokens":80,"total_tokens":150}}}
`

		res, err := parser.ParseAndEmitStream(context.Background(), "telegram:12345", strings.NewReader(sampleNDJSON))
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "SUCCESS", res.Status)
		assert.Equal(t, "c-100", res.ConversationID)

		mu.Lock()
		defer mu.Unlock()
		require.NotNil(t, initEvt)
		assert.Equal(t, "c-100", initEvt.ConversationID)
		assert.Equal(t, []string{"list_dir", "generate_image"}, initEvt.Tools)

		assert.Equal(t, []string{"Dưới đây ", "là kết quả:"}, deltas)
		require.Len(t, toolEvts, 2)
		assert.Equal(t, "ACTIVE", toolEvts[0].State)
		assert.Equal(t, "DONE", toolEvts[1].State)

		require.NotNil(t, resultEvt)
		assert.Equal(t, 150, resultEvt.Usage.TotalTokens)
		assert.Equal(t, 80, resultEvt.Usage.CacheReadTokens)
		assert.Equal(t, 80.0, resultEvt.Usage.CacheHitRatio())
	})

	t.Run("TC-BRG-04_AbruptSubprocessTerminationWithoutResult", func(t *testing.T) {
		var errEvt *domain.StreamErrorPayload
		var mu sync.Mutex

		bus.SubscribeSync(domain.EventStreamError, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			p := evt.Payload.(domain.StreamErrorPayload)
			errEvt = &p
			mu.Unlock()
			return nil
		})

		// Stream cut abruptly after delta
		cutNDJSON := `
{"event":"init","conversation_id":"c-cut"}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"writing code..."}}
`
		res, err := parser.ParseAndEmitStream(context.Background(), "telegram:999", strings.NewReader(cutNDJSON))
		assert.Nil(t, res)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "incomplete stream")

		mu.Lock()
		require.NotNil(t, errEvt)
		assert.Equal(t, "telegram:999", errEvt.SessionKey)
		assert.Contains(t, errEvt.Error, "terminated abruptly")
		mu.Unlock()
	})

	t.Run("TC-BRG-02_LargeNDJSONLineSupportUpTo2MB", func(t *testing.T) {
		hugeText := strings.Repeat("A", 1024*1024) // 1MB text
		ndjson := fmt.Sprintf(`{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"%s"}}
{"event":"result","result":{"status":"SUCCESS","response":"done"}}
`, hugeText)

		res, err := parser.ParseAndEmitStream(context.Background(), "telegram:large", strings.NewReader(ndjson))
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "SUCCESS", res.Status)
	})

	t.Run("TC-BRG-05_MilestoneWatchdogTriggering", func(t *testing.T) {
		milestoneCount := 0
		var mu sync.Mutex

		p := agy.NewStreamParser(bus)
		p.SetOnMilestone(func() {
			mu.Lock()
			milestoneCount++
			mu.Unlock()
		})

		ndjson := `
{"event":"init","conversation_id":"c-watchdog"}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"delta 1"}}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"delta 2"}}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"delta 3"}}
{"event":"step_update","step_update":{"step_type":"tool","state":"ACTIVE","tool_name":"run_command"}}
{"event":"step_update","step_update":{"step_type":"tool","state":"DONE","tool_name":"run_command"}}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"delta 4"}}
{"event":"result","result":{"status":"SUCCESS","response":"all done"}}
`
		res, err := p.ParseAndEmitStream(context.Background(), "telegram:watchdog", strings.NewReader(ndjson))
		require.NoError(t, err)
		require.NotNil(t, res)

		mu.Lock()
		count := milestoneCount
		mu.Unlock()

		// Milestones:
		// 1. "init" (1)
		// 2. "tool" ACTIVE (1)
		// 3. "tool" DONE (1)
		// 4. "result" (1)
		// Notice 4 deltas were ignored and did NOT trigger milestone!
		assert.Equal(t, 4, count, "should only trigger milestone on init, tools, and result (ignoring text deltas)")
	})

	t.Run("TC-BRG-06_StreamInterruptedStatusHandling", func(t *testing.T) {
		var interruptedEvt *domain.StreamInterruptedPayload
		var mu sync.Mutex

		bus.SubscribeSync(domain.EventStreamInterrupted, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			p := evt.Payload.(domain.StreamInterruptedPayload)
			interruptedEvt = &p
			mu.Unlock()
			return nil
		})

		ndjson := `
{"event":"init","conversation_id":"c-interrupted"}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"starting..."}}
{"event":"result","result":{"conversation_id":"c-interrupted","status":"INTERRUPTED","response":"partial"}}
`
		res, err := parser.ParseAndEmitStream(context.Background(), "telegram:interrupted", strings.NewReader(ndjson))
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "INTERRUPTED", res.Status)

		mu.Lock()
		defer mu.Unlock()
		require.NotNil(t, interruptedEvt)
		assert.Equal(t, "telegram:interrupted", interruptedEvt.SessionKey)
		assert.Equal(t, "c-interrupted", interruptedEvt.ConversationID)
	})

	t.Run("TC-BRG-07_ReturnsImmediatelyOnResultWithoutWaitingForEOF", func(t *testing.T) {
		pr, pw := io.Pipe()
		defer pr.Close()

		go func() {
			_, _ = pw.Write([]byte("{\"event\":\"init\",\"conversation_id\":\"c-immediate\"}\n"))
			_, _ = pw.Write([]byte("{\"event\":\"result\",\"result\":{\"conversation_id\":\"c-immediate\",\"status\":\"SUCCESS\",\"response\":\"done\"}}\n"))
			// Deliberately keep pw OPEN to simulate open pipe from running subprocess
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		res, err := parser.ParseAndEmitStream(ctx, "telegram:imm", pr)
		require.NoError(t, err)
		require.NotNil(t, res)
		assert.Equal(t, "SUCCESS", res.Status)
		assert.Equal(t, "c-immediate", res.ConversationID)
		assert.Equal(t, "done", res.Response)
		_ = pw.Close()
	})

	t.Run("TC-BRG-08_TurnIDPropagationToAllEvents", func(t *testing.T) {
		var (
			mu           sync.Mutex
			initTurnID   string
			deltaTurnID  string
			toolTurnID   string
			resultTurnID string
		)

		bus.SubscribeSync(domain.EventStreamInit, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			initTurnID = evt.Payload.(domain.StreamInitPayload).TurnID
			mu.Unlock()
			return nil
		})
		bus.SubscribeSync(domain.EventStreamDelta, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			deltaTurnID = evt.Payload.(domain.StreamDeltaPayload).TurnID
			mu.Unlock()
			return nil
		})
		bus.SubscribeSync(domain.EventStreamTool, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			toolTurnID = evt.Payload.(domain.StreamToolPayload).TurnID
			mu.Unlock()
			return nil
		})
		bus.SubscribeSync(domain.EventStreamResult, func(ctx context.Context, evt domain.Event) error {
			mu.Lock()
			resultTurnID = evt.Payload.(domain.StreamResultPayload).TurnID
			mu.Unlock()
			return nil
		})

		turnParser := agy.NewStreamParser(bus)
		turnParser.SetTurnID("turn-test-xyz")

		ndjson := `
{"event":"init","conversation_id":"c-turnid"}
{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"hello"}}
{"event":"step_update","step_update":{"step_type":"tool","state":"DONE","tool_name":"cmd"}}
{"event":"result","result":{"status":"SUCCESS","response":"done"}}
`
		res, err := turnParser.ParseAndEmitStream(context.Background(), "telegram:turnid", strings.NewReader(ndjson))
		require.NoError(t, err)
		require.NotNil(t, res)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, "turn-test-xyz", initTurnID)
		assert.Equal(t, "turn-test-xyz", deltaTurnID)
		assert.Equal(t, "turn-test-xyz", toolTurnID)
		assert.Equal(t, "turn-test-xyz", resultTurnID)
		assert.Equal(t, "turn-test-xyz", res.TurnID)
	})
}
