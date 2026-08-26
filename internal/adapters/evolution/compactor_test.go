package evolution

import (
	"strings"
	"testing"
)

func TestMemoryCompactor_Compact(t *testing.T) {
	compactor := NewMemoryCompactor()

	var sb strings.Builder
	sb.WriteString("# MEMORY.md\n\n## 4. Evolved Behavioral Guardrails\n")
	for i := 1; i <= 250; i++ {
		sb.WriteString("- 2026-08-26 - [sqlite_tag]: Rule version " + string(rune(i)) + "\n")
	}

	rawDoc := sb.String()
	compacted, wasCompacted, err := compactor.Compact(rawDoc, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !wasCompacted {
		t.Errorf("expected document to be compacted")
	}

	// Should deduplicate identical tags to 1
	lines := strings.Split(compacted, "\n")
	if len(lines) > 200 {
		t.Errorf("expected compacted lines <= 200, got %d", len(lines))
	}

	tagCount := strings.Count(compacted, "[sqlite_tag]")
	if tagCount != 1 {
		t.Errorf("expected tag [sqlite_tag] to appear exactly once, got %d", tagCount)
	}
}
