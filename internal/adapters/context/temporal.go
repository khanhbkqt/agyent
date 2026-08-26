package context

import (
	"fmt"
	"time"

	"agyent/internal/core/ports"
)

var _ ports.TemporalContextPort = (*TemporalContext)(nil)

// TemporalContext provides lightweight temporal gap formatting.
type TemporalContext struct{}

// NewTemporalContext creates a new TemporalContext instance.
func NewTemporalContext() *TemporalContext {
	return &TemporalContext{}
}

// FormatTemporalTag generates an ultra-compact, token-efficient 1-line gap marker (~6 tokens).
// Returns empty string if elapsed duration < 30 minutes or lastTime is zero.
func (t *TemporalContext) FormatTemporalTag(lastTime, currTime time.Time, loc *time.Location) string {
	if lastTime.IsZero() || currTime.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}

	lastLocal := lastTime.In(loc)
	currLocal := currTime.In(loc)

	diff := currLocal.Sub(lastLocal)
	if diff < 30*time.Minute {
		return ""
	}

	// Check if on the same calendar day in local timezone
	lastYear, lastMonth, lastDay := lastLocal.Date()
	currYear, currMonth, currDay := currLocal.Date()
	isSameDay := lastYear == currYear && lastMonth == currMonth && lastDay == currDay

	if isSameDay {
		if diff < 2*time.Hour {
			mins := int(diff.Minutes())
			return fmt.Sprintf("[GAP: ~%dm later]", mins)
		}
		hours := int(diff.Hours())
		period := "afternoon"
		if currLocal.Hour() >= 18 {
			period = "evening"
		} else if currLocal.Hour() < 12 {
			period = "morning"
		}
		return fmt.Sprintf("[GAP: ~%d hours later (%s)]", hours, period)
	}

	// Check if overnight (yesterday)
	yesterday := currLocal.AddDate(0, 0, -1)
	yYear, yMonth, yDay := yesterday.Date()
	if lastYear == yYear && lastMonth == yMonth && lastDay == yDay {
		return fmt.Sprintf("[GAP: Next morning, %s]", currLocal.Format("15:04"))
	}

	// Multi-day absence: Compute calendar day difference
	lastMidnight := time.Date(lastYear, lastMonth, lastDay, 0, 0, 0, 0, loc)
	currMidnight := time.Date(currYear, currMonth, currDay, 0, 0, 0, 0, loc)
	calDays := int(currMidnight.Sub(lastMidnight).Hours() / 24)
	if calDays < 2 {
		calDays = 2
	}
	return fmt.Sprintf("[GAP: %d days later, %s]", calDays, currLocal.Format("2006-01-02"))
}
