package evolution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agyent/internal/core/domain"
)

func TestDeltaExtractor_PruneOutput(t *testing.T) {
	short := "Hello world"
	if PruneOutput(short) != short {
		t.Errorf("expected short text to remain unchanged")
	}

	long := strings.Repeat("A", 3000)
	pruned := PruneOutput(long)
	if len(pruned) >= 3000 {
		t.Errorf("expected pruned text to be shorter than 3000 chars, got %d", len(pruned))
	}
	if !strings.Contains(pruned, "[TRUNCATED") {
		t.Errorf("expected pruned text to contain truncation notice")
	}
}

func TestDeltaExtractor_ExtractTranscriptDelta(t *testing.T) {
	tempDir := t.TempDir()
	transcriptPath := filepath.Join(tempDir, "transcript.jsonl")

	lines := []string{
		`{"step_index": 1, "source": "USER", "type": "USER_INPUT", "content": "hello"}`,
		`{"step_index": 2, "source": "MODEL", "type": "PLANNER_RESPONSE", "content": "hi there"}`,
		`{"step_index": 3, "source": "USER", "type": "USER_INPUT", "content": "fix the bug"}`,
	}
	_ = os.WriteFile(transcriptPath, []byte(strings.Join(lines, "\n")), 0600)

	extractor := NewDeltaExtractor()

	// Extract from step 1 (should return steps 2 and 3)
	delta, maxStep, err := extractor.ExtractTranscriptDelta(transcriptPath, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxStep != 3 {
		t.Errorf("expected maxStep 3, got %d", maxStep)
	}

	if strings.Contains(delta, "Step 1") {
		t.Errorf("expected Step 1 to be excluded, but was present")
	}

	if !strings.Contains(delta, "Step 2") || !strings.Contains(delta, "Step 3") {
		t.Errorf("expected Steps 2 and 3 in delta, got:\n%s", delta)
	}
}

func TestDeltaExtractor_FormatSnapshotTurns(t *testing.T) {
	extractor := NewDeltaExtractor()
	snapshot := domain.ConversationSnapshot{
		Turns: []domain.CanonicalMessage{
			{Sender: domain.SenderUser{Username: "khanhbkqt"}, Text: "First turn"},
			{Sender: domain.SenderUser{Username: "agyent"}, Text: "Second turn"},
		},
	}

	formatted := extractor.FormatSnapshotTurns(snapshot)
	if !strings.Contains(formatted, "khanhbkqt") || !strings.Contains(formatted, "First turn") {
		t.Errorf("unexpected formatted output: %s", formatted)
	}
}
