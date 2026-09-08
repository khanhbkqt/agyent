package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agyent/internal/core/domain"
)

var (
	ErrInvalidScheduleExpr = errors.New("scheduler: invalid schedule expression")
	ErrPastScheduleTime    = errors.New("scheduler: schedule time is in the past")
)

// ParseNextRun calculates the next execution time for a schedule task given its expression, type, and base time.
func ParseNextRun(schedType domain.ScheduleType, expr string, fromTime time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = time.Local
	}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return time.Time{}, fmt.Errorf("%w: expression cannot be empty", ErrInvalidScheduleExpr)
	}

	fromTime = fromTime.In(loc)

	switch schedType {
	case domain.ScheduleTypeOnce:
		return parseOnceTime(expr, fromTime, loc)
	case domain.ScheduleTypeCron:
		return parseCronNext(expr, fromTime, loc)
	default:
		return time.Time{}, fmt.Errorf("%w: unsupported schedule type %s", ErrInvalidScheduleExpr, schedType)
	}
}

func parseOnceTime(expr string, fromTime time.Time, loc *time.Location) (time.Time, error) {
	cleaned := strings.ToLower(expr)
	cleaned = strings.TrimPrefix(cleaned, "in ")
	cleaned = strings.TrimPrefix(cleaned, "after ")
	cleaned = strings.TrimSpace(cleaned)

	// 1. Check relative duration (e.g. "45m", "2h", "30s", "1h30m")
	if d, err := time.ParseDuration(cleaned); err == nil && d > 0 {
		return fromTime.Add(d), nil
	}

	// 2. Check RFC3339 / ISO formats
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
		"15:04",
	}

	for _, format := range formats {
		if format == "15:04" {
			// Time of day today or tomorrow
			if t, err := time.ParseInLocation("15:04", expr, loc); err == nil {
				target := time.Date(fromTime.Year(), fromTime.Month(), fromTime.Day(), t.Hour(), t.Minute(), 0, 0, loc)
				if !target.After(fromTime) {
					target = target.AddDate(0, 0, 1) // Tomorrow
				}
				return target, nil
			}
			continue
		}

		if t, err := time.ParseInLocation(format, expr, loc); err == nil {
			if t.Before(fromTime) {
				return time.Time{}, fmt.Errorf("%w: %s is in the past", ErrPastScheduleTime, expr)
			}
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("%w: cannot parse time %q", ErrInvalidScheduleExpr, expr)
}

// cronSchedule represents a parsed standard 5-part cron expression.
type cronSchedule struct {
	minutes []bool // 0-59
	hours   []bool // 0-23
	doms    []bool // 1-31
	months  []bool // 1-12
	dows    []bool // 0-6 (0 = Sunday)
}

func parseCronNext(expr string, fromTime time.Time, loc *time.Location) (time.Time, error) {
	cs, err := parseCronExpr(expr)
	if err != nil {
		return time.Time{}, err
	}

	// Start searching at the next minute after fromTime to ensure forward progress
	// for 5-field standard cron expressions with minute resolution.
	t := fromTime.Truncate(time.Minute).Add(1 * time.Minute)

	// Search up to 5 years into the future
	limit := t.AddDate(5, 0, 0)

	for t.Before(limit) {
		month := int(t.Month())
		if !cs.months[month] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}

		dom := t.Day()
		dow := int(t.Weekday())
		if !cs.doms[dom] || !cs.dows[dow] {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}

		hour := t.Hour()
		if !cs.hours[hour] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}

		min := t.Minute()
		if !cs.minutes[min] {
			t = t.Add(1 * time.Minute).Truncate(time.Minute)
			continue
		}

		return t, nil
	}

	return time.Time{}, fmt.Errorf("%w: no matching next run within 5 years for %s", ErrInvalidScheduleExpr, expr)
}

func parseCronExpr(expr string) (*cronSchedule, error) {
	expr = strings.TrimSpace(expr)

	// Standard shortcuts
	switch strings.ToLower(expr) {
	case "@yearly", "@annually":
		expr = "0 0 1 1 *"
	case "@monthly":
		expr = "0 0 1 * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@hourly":
		expr = "0 * * * *"
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("%w: cron expression must have 5 fields, got %d: %s", ErrInvalidScheduleExpr, len(fields), expr)
	}

	minutes, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("invalid minute field: %w", err)
	}

	hours, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("invalid hour field: %w", err)
	}

	doms, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("invalid day of month field: %w", err)
	}

	months, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("invalid month field: %w", err)
	}

	dows, err := parseCronField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("invalid day of week field: %w", err)
	}

	return &cronSchedule{
		minutes: minutes,
		hours:   hours,
		doms:    doms,
		months:  months,
		dows:    dows,
	}, nil
}

func parseCronField(field string, min, max int) ([]bool, error) {
	bits := make([]bool, max+1)

	parts := strings.Split(field, ",")
	for _, part := range parts {
		step := 1
		rangeStr := part

		if strings.Contains(part, "/") {
			subParts := strings.SplitN(part, "/", 2)
			rangeStr = subParts[0]
			var err error
			step, err = strconv.Atoi(subParts[1])
			if err != nil || step <= 0 {
				return nil, fmt.Errorf("invalid step value %s", part)
			}
		}

		var start, end int
		if rangeStr == "*" {
			start = min
			end = max
		} else if strings.Contains(rangeStr, "-") {
			subParts := strings.SplitN(rangeStr, "-", 2)
			var err error
			start, err = strconv.Atoi(subParts[0])
			if err != nil {
				return nil, fmt.Errorf("invalid range start %s", subParts[0])
			}
			if min == 0 && max == 6 && start == 7 {
				start = 0
			}
			end, err = strconv.Atoi(subParts[1])
			if err != nil {
				return nil, fmt.Errorf("invalid range end %s", subParts[1])
			}
			if min == 0 && max == 6 && end == 7 {
				end = 6
				bits[0] = true
			}
		} else {
			val, err := strconv.Atoi(rangeStr)
			if err != nil {
				return nil, fmt.Errorf("invalid integer value %s", rangeStr)
			}
			if min == 0 && max == 6 && val == 7 {
				val = 0
			}
			start = val
			if strings.Contains(part, "/") {
				end = max
			} else {
				end = val
			}
		}

		if start < min || end > max || start > end {
			return nil, fmt.Errorf("range %d-%d out of bounds [%d-%d]", start, end, min, max)
		}

		for i := start; i <= end; i += step {
			bits[i] = true
		}
	}

	return bits, nil
}
