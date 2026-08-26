package evolution

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agyent/internal/core/domain"
)

// TranscriptStep represents a single step line in transcript.jsonl.
type TranscriptStep struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	Content   string `json:"content"`
	ToolCalls []struct {
		ToolName string         `json:"tool_name"`
		Args     map[string]any `json:"args"`
		Output   string         `json:"output"`
	} `json:"tool_calls,omitempty"`
}

// DeltaExtractor extracts and prunes incremental transcript steps between last and current steps.
type DeltaExtractor struct{}

// NewDeltaExtractor creates a new DeltaExtractor.
func NewDeltaExtractor() *DeltaExtractor {
	return &DeltaExtractor{}
}

// PruneOutput trims long outputs to 800 chars head + 800 chars tail if exceeding 2000 chars.
func PruneOutput(text string) string {
	if len(text) <= 2000 {
		return text
	}
	head := text[:800]
	tail := text[len(text)-800:]
	return fmt.Sprintf("%s\n\n... [TRUNCATED %d CHARACTERS OF TOOL OUTPUT] ...\n\n%s", head, len(text)-1600, tail)
}

// ExtractTranscriptDelta reads transcript.jsonl from startStep to endStep and returns a formatted compact string.
func (e *DeltaExtractor) ExtractTranscriptDelta(transcriptPath string, startStep int) (string, int, error) {
	file, err := os.Open(transcriptPath)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	var sb strings.Builder
	scanner := bufio.NewScanner(file)
	// Allow large token buffer
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	stepCount := 0
	maxStep := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var step TranscriptStep
		if err := json.Unmarshal(line, &step); err != nil {
			continue
		}

		maxStep = step.StepIndex

		if step.StepIndex <= startStep {
			continue
		}

		stepCount++
		sb.WriteString(fmt.Sprintf("--- Step %d [%s | %s] ---\n", step.StepIndex, step.Source, step.Type))
		if step.Content != "" {
			sb.WriteString(PruneOutput(step.Content))
			sb.WriteString("\n")
		}

		for _, tc := range step.ToolCalls {
			sb.WriteString(fmt.Sprintf("Tool Call: %s\n", tc.ToolName))
			if tc.Output != "" {
				sb.WriteString(fmt.Sprintf("Tool Output: %s\n", PruneOutput(tc.Output)))
			}
		}
		sb.WriteString("\n")
	}

	if err := scanner.Err(); err != nil {
		return "", maxStep, err
	}

	return strings.TrimSpace(sb.String()), maxStep, nil
}

// FormatSnapshotTurns formats canonical messages in a snapshot into a compact dialogue block.
func (e *DeltaExtractor) FormatSnapshotTurns(snapshot domain.ConversationSnapshot) string {
	var sb strings.Builder
	for i, turn := range snapshot.Turns {
		sb.WriteString(fmt.Sprintf("[Turn %d | Sender: %s]\n", i+1, turn.Sender.Username))
		sb.WriteString(turn.Text)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}
