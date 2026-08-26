package sqlite

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FlexTime is a custom sql.Scanner type that can parse timestamps stored as
// int64 (Unix milliseconds or seconds), string (RFC3339, ISO-8601, numeric string, etc.), or time.Time.
type FlexTime struct {
	time.Time
}

// Scan implements the database/sql.Scanner interface.
func (ft *FlexTime) Scan(value any) error {
	if value == nil {
		ft.Time = time.Time{}
		return nil
	}

	switch v := value.(type) {
	case int64:
		ft.Time = timeFromMilli(v)
		return nil
	case int:
		ft.Time = timeFromMilli(int64(v))
		return nil
	case float64:
		ft.Time = timeFromMilli(int64(v))
		return nil
	case []byte:
		return ft.parseString(string(v))
	case string:
		return ft.parseString(v)
	case time.Time:
		ft.Time = v.UTC()
		return nil
	default:
		return fmt.Errorf("cannot scan %T into FlexTime: %v", value, value)
	}
}

func (ft *FlexTime) parseString(s string) error {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		ft.Time = time.Time{}
		return nil
	}

	// 1. Numeric timestamp in string format (e.g. "1724580000000" or "1724580000")
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		ft.Time = timeFromMilli(ms)
		return nil
	}

	// 2. RFC3339Nano (e.g. "2026-08-25T07:21:11.172945+00:00")
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		ft.Time = t.UTC()
		return nil
	}

	// 3. RFC3339 (e.g. "2026-08-25T07:21:11Z")
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		ft.Time = t.UTC()
		return nil
	}

	// 4. Standard SQL date/time formats
	layouts := []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			ft.Time = t.UTC()
			return nil
		}
	}

	return fmt.Errorf("unable to parse timestamp string %q", s)
}

// timeToMilli converts a time.Time to Unix milliseconds (int64).
// If t is zero, returns 0.
func timeToMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// timeFromMilli converts Unix milliseconds or seconds (int64) to time.Time in UTC.
// If ms <= 0, returns a zero time.Time.
func timeFromMilli(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	if ms < 10000000000 { // less than 10 billion -> Unix seconds
		return time.Unix(ms, 0).UTC()
	}
	return time.UnixMilli(ms).UTC()
}

// toNullString converts a string to sql.NullString.
func toNullString(s string) sql.NullString {
	return sql.NullString{
		String: s,
		Valid:  s != "",
	}
}

// fromNullString extracts the string value or empty string if NULL.
func fromNullString(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}
