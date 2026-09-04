package scheduler

import (
	"testing"
	"time"

	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNextRun_Once(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 9, 5, 8, 0, 0, 0, loc)

	// Relative durations
	t1, err := ParseNextRun(domain.ScheduleTypeOnce, "in 45m", base, loc)
	require.NoError(t, err)
	assert.Equal(t, base.Add(45*time.Minute), t1)

	t2, err := ParseNextRun(domain.ScheduleTypeOnce, "after 2h", base, loc)
	require.NoError(t, err)
	assert.Equal(t, base.Add(2*time.Hour), t2)

	t3, err := ParseNextRun(domain.ScheduleTypeOnce, "30s", base, loc)
	require.NoError(t, err)
	assert.Equal(t, base.Add(30*time.Second), t3)

	// Absolute RFC3339
	t4, err := ParseNextRun(domain.ScheduleTypeOnce, "2026-09-05T10:00:00Z", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 5, 10, 0, 0, 0, loc), t4)

	// Time of day (e.g. 15:04)
	t5, err := ParseNextRun(domain.ScheduleTypeOnce, "09:30", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 5, 9, 30, 0, 0, loc), t5)

	// Past time of day rolls over to tomorrow
	t6, err := ParseNextRun(domain.ScheduleTypeOnce, "07:00", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 6, 7, 0, 0, 0, loc), t6)
}

func TestParseNextRun_Cron(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 9, 5, 8, 14, 0, 0, loc) // 8:14 AM

	// Every 15 minutes: next should be 8:15
	t1, err := ParseNextRun(domain.ScheduleTypeCron, "*/15 * * * *", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 5, 8, 15, 0, 0, loc), t1)

	// Daily at 9:00 AM
	t2, err := ParseNextRun(domain.ScheduleTypeCron, "0 9 * * *", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 5, 9, 0, 0, 0, loc), t2)

	// Shortcut @hourly -> 9:00 AM
	t3, err := ParseNextRun(domain.ScheduleTypeCron, "@hourly", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 5, 9, 0, 0, 0, loc), t3)

	// Invalid expressions
	_, err = ParseNextRun(domain.ScheduleTypeCron, "invalid cron", base, loc)
	assert.Error(t, err)

	_, err = ParseNextRun(domain.ScheduleTypeCron, "70 * * * *", base, loc) // minute > 59
	assert.Error(t, err)
}
