package context

import (
	"strings"
	"testing"
	"time"
)

func TestTemporalContext_FormatTemporalTag(t *testing.T) {
	tc := NewTemporalContext()
	loc, _ := time.LoadLocation("Asia/Ho_Chi_Minh")

	baseTime := time.Date(2026, 8, 26, 9, 0, 0, 0, loc)

	tests := []struct {
		name        string
		lastTime    time.Time
		currTime    time.Time
		expectedTag string
		contains    string
	}{
		{
			name:        "Zero timestamps",
			lastTime:    time.Time{},
			currTime:    baseTime,
			expectedTag: "",
		},
		{
			name:        "Short gap < 30m (e.g. 20m)",
			lastTime:    baseTime,
			currTime:    baseTime.Add(20 * time.Minute),
			expectedTag: "",
		},
		{
			name:        "Medium pause ~45m",
			lastTime:    baseTime,
			currTime:    baseTime.Add(45 * time.Minute),
			expectedTag: "[GAP: ~45m later]",
		},
		{
			name:        "Long pause ~3h in afternoon",
			lastTime:    baseTime,
			currTime:    baseTime.Add(5 * time.Hour), // 14:00
			contains:    "hours later",
		},
		{
			name:        "Overnight next morning",
			lastTime:    time.Date(2026, 8, 25, 22, 0, 0, 0, loc),
			currTime:    time.Date(2026, 8, 26, 8, 30, 0, 0, loc),
			expectedTag: "[GAP: Next morning, 08:30]",
		},
		{
			name:        "3 days absence",
			lastTime:    time.Date(2026, 8, 23, 10, 0, 0, 0, loc),
			currTime:    time.Date(2026, 8, 26, 10, 0, 0, 0, loc),
			contains:    "3 days later",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag := tc.FormatTemporalTag(tt.lastTime, tt.currTime, loc)
			if tt.expectedTag != "" && tag != tt.expectedTag {
				t.Errorf("expected tag %q, got %q", tt.expectedTag, tag)
			}
			if tt.contains != "" && !strings.Contains(tag, tt.contains) {
				t.Errorf("expected tag to contain %q, got %q", tt.contains, tag)
			}
		})
	}
}
