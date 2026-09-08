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

	// Stepped single offset: 0/15 * * * *
	t4, err := ParseNextRun(domain.ScheduleTypeCron, "0/15 * * * *", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 5, 8, 15, 0, 0, loc), t4)

	// Sunday as 7: 0 0 * * 7 (2026-09-05 is Saturday, Sunday is 2026-09-06)
	t5, err := ParseNextRun(domain.ScheduleTypeCron, "0 0 * * 7", base, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 6, 0, 0, 0, 0, loc), t5)

	// Invalid expressions
	_, err = ParseNextRun(domain.ScheduleTypeCron, "invalid cron", base, loc)
	assert.Error(t, err)

	_, err = ParseNextRun(domain.ScheduleTypeCron, "70 * * * *", base, loc) // minute > 59
	assert.Error(t, err)
}

func TestParseNextRun_PastTimeRejection(t *testing.T) {
	loc := time.UTC
	base := time.Date(2026, 9, 5, 12, 0, 0, 0, loc)

	// Past absolute timestamp should error with ErrPastScheduleTime
	_, err := ParseNextRun(domain.ScheduleTypeOnce, "2026-09-05T10:00:00Z", base, loc)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPastScheduleTime)
}

func TestParseNextRun_Cron_SameMinuteExecution(t *testing.T) {
	loc := time.UTC

	// Case 1: Daily 05:30 morning greeting when evaluated at exactly 05:30:00
	baseExact := time.Date(2026, 9, 8, 5, 30, 0, 0, loc)
	nextExact, err := ParseNextRun(domain.ScheduleTypeCron, "30 5 * * *", baseExact, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 9, 5, 30, 0, 0, loc), nextExact, "should advance to tomorrow 05:30:00 when evaluated at 05:30:00")

	// Case 2: Daily 05:30 morning greeting when evaluated mid-minute at 05:30:54 (the bug scenario)
	baseMidMinute := time.Date(2026, 9, 8, 5, 30, 54, 500000000, loc)
	nextMidMinute, err := ParseNextRun(domain.ScheduleTypeCron, "30 5 * * *", baseMidMinute, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 9, 5, 30, 0, 0, loc), nextMidMinute, "should advance to tomorrow 05:30:00 when evaluated at 05:30:54")

	// Case 3: Daily 05:30 morning greeting evaluated 1 second before at 05:29:59
	baseJustBefore := time.Date(2026, 9, 8, 5, 29, 59, 0, loc)
	nextJustBefore, err := ParseNextRun(domain.ScheduleTypeCron, "30 5 * * *", baseJustBefore, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 8, 5, 30, 0, 0, loc), nextJustBefore, "should yield today 05:30:00 when evaluated at 05:29:59")

	// Case 4: Step interval */15 evaluated mid-minute at 08:15:30
	baseStep := time.Date(2026, 9, 8, 8, 15, 30, 0, loc)
	nextStep, err := ParseNextRun(domain.ScheduleTypeCron, "*/15 * * * *", baseStep, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 8, 8, 30, 0, 0, loc), nextStep, "should advance to next 15-minute interval (08:30:00)")

	// Case 5: Timezone awareness (Vietnam ICT +07:00)
	locVN := time.FixedZone("ICT", 7*3600)
	baseVN := time.Date(2026, 9, 8, 5, 30, 54, 0, locVN)
	nextVN, err := ParseNextRun(domain.ScheduleTypeCron, "30 5 * * *", baseVN, locVN)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 9, 5, 30, 0, 0, locVN), nextVN, "should advance to tomorrow 05:30:00 ICT")

	// Case 6: Month rollover at end of month (e.g. Sept 30 23:59:00 -> Oct 1)
	baseMonthEnd := time.Date(2026, 9, 30, 23, 59, 30, 0, loc)
	nextMonthEnd, err := ParseNextRun(domain.ScheduleTypeCron, "0 0 * * *", baseMonthEnd, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), nextMonthEnd, "should advance across month boundary to Oct 1 00:00:00")

	// Case 7: Year rollover (e.g. Dec 31 23:59:30 -> Jan 1)
	baseYearEnd := time.Date(2026, 12, 31, 23, 59, 30, 0, loc)
	nextYearEnd, err := ParseNextRun(domain.ScheduleTypeCron, "0 0 * * *", baseYearEnd, loc)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, loc), nextYearEnd, "should advance across year boundary to Jan 1 00:00:00")
}


