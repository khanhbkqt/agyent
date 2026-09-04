package doctor

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/config"
	_ "modernc.org/sqlite"
)

// CheckStorage inspects the SQLite database, WAL mode, schema integrity, and session health.
func (d *DoctorRunner) CheckStorage(ctx context.Context) []CheckResult {
	var results []CheckResult

	dbPath := d.cfg.Storage.DBPath
	expandedDBPath, err := config.ExpandPath(dbPath)
	if err != nil {
		results = append(results, CheckResult{
			Name:        "SQLite DB Path",
			Category:    CategoryStorage,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to expand SQLite DB path: %v", err),
			Remediation: "Check storage.db_path in configuration.",
		})
		return results
	}

	// 1. Check DB directory & file existence
	dbDir := filepath.Dir(expandedDBPath)
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		results = append(results, CheckResult{
			Name:        "SQLite DB Directory",
			Category:    CategoryStorage,
			Status:      StatusWarn,
			Message:     fmt.Sprintf("DB directory %s does not exist yet", dbDir),
			Remediation: "Run 'agyent init' or 'agyent doctor --fix' to create storage directories.",
			CanAutoFix:  true,
		})
	}

	if _, err := os.Stat(expandedDBPath); os.IsNotExist(err) {
		results = append(results, CheckResult{
			Name:        "SQLite Database Existence",
			Category:    CategoryStorage,
			Status:      StatusWarn,
			Message:     fmt.Sprintf("Database file not found at %s (will be created on first start)", expandedDBPath),
			Remediation: "Run 'agyent init' or start daemon with 'agyent run'.",
			CanAutoFix:  true,
		})
		return results
	}

	results = append(results, CheckResult{
		Name:     "SQLite Database Existence",
		Category: CategoryStorage,
		Status:   StatusPass,
		Message:  fmt.Sprintf("Found database file at: %s", expandedDBPath),
	})

	// 2. Open SQLite Connection with timeout
	db, err := sql.Open("sqlite", expandedDBPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		results = append(results, CheckResult{
			Name:        "SQLite Connection",
			Category:    CategoryStorage,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to open SQLite database: %v", err),
			Remediation: "Ensure modernc.org/sqlite pure-Go driver is compatible and DB is not locked by another process.",
		})
		return results
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		results = append(results, CheckResult{
			Name:        "SQLite Ping",
			Category:    CategoryStorage,
			Status:      StatusFail,
			Message:     fmt.Sprintf("Failed to ping SQLite database: %v", err),
			Remediation: "Check if the database file is corrupted or locked exclusively by another application.",
		})
		return results
	}

	results = append(results, CheckResult{
		Name:     "SQLite Connection",
		Category: CategoryStorage,
		Status:   StatusPass,
		Message:  "Successfully connected to SQLite database",
	})

	// 3. Check WAL Journal Mode
	var journalMode string
	_ = db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode)
	if strings.ToLower(journalMode) != "wal" {
		results = append(results, CheckResult{
			Name:        "SQLite WAL Journal Mode",
			Category:    CategoryStorage,
			Status:      StatusWarn,
			Message:     fmt.Sprintf("Current journal mode is %q (expected: WAL)", journalMode),
			Remediation: "Ensure database URI specifies '_pragma=journal_mode(WAL)' for concurrent read performance.",
		})
	} else {
		results = append(results, CheckResult{
			Name:     "SQLite WAL Journal Mode",
			Category: CategoryStorage,
			Status:   StatusPass,
			Message:  "WAL (Write-Ahead Logging) mode is enabled",
		})
	}

	// 4. Inspect Table Counts
	type tableCount struct {
		name  string
		query string
	}
	counts := []tableCount{
		{"Sessions", "SELECT COUNT(*) FROM sessions"},
		{"Audit Logs", "SELECT COUNT(*) FROM audit_logs"},
		{"Agents", "SELECT COUNT(*) FROM agents"},
		{"Whitelisted Users", "SELECT COUNT(*) FROM users"},
		{"Subagent Tasks", "SELECT COUNT(*) FROM subagent_tasks"},
	}

	var tableSummary []string
	for _, tc := range counts {
		var cnt int
		if err := db.QueryRowContext(ctx, tc.query).Scan(&cnt); err == nil {
			tableSummary = append(tableSummary, fmt.Sprintf("%s: %d", tc.name, cnt))
		}
	}

	results = append(results, CheckResult{
		Name:     "Database Schema & Tables",
		Category: CategoryStorage,
		Status:   StatusPass,
		Message:  strings.Join(tableSummary, " | "),
	})

	// 5. Audit Log Health & Quota/Rate-limit History Check
	auditRows, err := db.QueryContext(ctx, "SELECT status, error_message, total_tokens, duration_seconds, created_at FROM audit_logs ORDER BY id DESC LIMIT 20")
	if err == nil {
		defer auditRows.Close()

		recentErrors := 0
		rateLimitHits := 0
		highContextHits := 0
		timeoutHits := 0

		for auditRows.Next() {
			var status, errMsg string
			var tokens int
			var dur float64
			var createdAt int64
			_ = auditRows.Scan(&status, &errMsg, &tokens, &dur, &createdAt)

			if status == "ERROR" {
				recentErrors++
			}
			lowerErr := strings.ToLower(errMsg)
			if strings.Contains(lowerErr, "429") || strings.Contains(lowerErr, "quota") || strings.Contains(lowerErr, "rate limit") || strings.Contains(lowerErr, "resource_exhausted") {
				rateLimitHits++
			}
			if tokens > 1000000 {
				highContextHits++
			}
			if dur >= 1800 || strings.Contains(lowerErr, "timed out") || strings.Contains(lowerErr, "deadline exceeded") {
				timeoutHits++
			}
		}

		if rateLimitHits > 0 {
			results = append(results, CheckResult{
				Name:        "Audit Log Quota & Rate Limit Triage",
				Category:    CategoryStorage,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Detected %d recent 429 Rate Limit / Quota Exceeded occurrence(s) in audit logs", rateLimitHits),
				Remediation: "Google Gemini quota exhausted during recent turns. Ensure Auto-Cooldown or fresh session is used.",
			})
		}

		if highContextHits > 0 {
			results = append(results, CheckResult{
				Name:        "Session Context Length Triage",
				Category:    CategoryStorage,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Detected %d turn(s) with token context > 1,000,000 tokens", highContextHits),
				Remediation: "Send '/new' or '/reset' in Telegram chat to start a clean conversation session and reset prompt tokens.",
			})
		}

		if timeoutHits > 0 {
			results = append(results, CheckResult{
				Name:        "Watchdog Timeout Triage",
				Category:    CategoryStorage,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Detected %d turn timeout watchdog violation(s) (>= 1800s)", timeoutHits),
				Remediation: "Run '/new' to reduce prompt context or check network latency to Google Antigravity servers.",
			})
		}

		if recentErrors == 0 {
			results = append(results, CheckResult{
				Name:     "Recent Turn Execution Health",
				Category: CategoryStorage,
				Status:   StatusPass,
				Message:  "All recent turns completed with status SUCCESS (0 errors in last 20 turns)",
			})
		} else if recentErrors > 0 && rateLimitHits == 0 && timeoutHits == 0 {
			results = append(results, CheckResult{
				Name:        "Recent Turn Execution Health",
				Category:    CategoryStorage,
				Status:      StatusWarn,
				Message:     fmt.Sprintf("Detected %d recent turn error(s) in audit logs", recentErrors),
				Remediation: "Inspect recent audit logs or run 'agyent doctor --session <key>' to diagnose root cause.",
			})
		}
	}

	// 6. Target Session Diagnostics (if specified)
	if d.opts.TargetSession != "" {
		results = append(results, d.diagnoseSpecificSession(ctx, db, d.opts.TargetSession)...)
	}

	return results
}

func (d *DoctorRunner) diagnoseSpecificSession(ctx context.Context, db *sql.DB, sessionKey string) []CheckResult {
	var results []CheckResult

	var agent, proj, convID string
	var updated int64
	err := db.QueryRowContext(ctx, "SELECT active_agent, active_project, global_conversation_id, updated_at FROM sessions WHERE session_key = ?", sessionKey).Scan(&agent, &proj, &convID, &updated)
	if err != nil {
		results = append(results, CheckResult{
			Name:        fmt.Sprintf("Session Triage [%s]", sessionKey),
			Category:    CategoryStorage,
			Status:      StatusWarn,
			Message:     fmt.Sprintf("Session key %q not found in database", sessionKey),
			Remediation: "Verify the session key format (e.g. 'telegram:<bot_id>:<chat_id>').",
		})
		return results
	}

	t := time.UnixMilli(updated).Format("2006-01-02 15:04:05")
	results = append(results, CheckResult{
		Name:     fmt.Sprintf("Session Triage [%s]", sessionKey),
		Category: CategoryStorage,
		Status:   StatusPass,
		Message:  fmt.Sprintf("Agent: %s | Project: %q | Conv ID: %s | Last Active: %s", agent, proj, convID, t),
	})

	// Check transcript file for target session
	if convID != "" {
		home, _ := os.UserHomeDir()
		transcriptPath := filepath.Join(home, ".gemini", "antigravity", "brain", convID, ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(transcriptPath); err == nil {
			results = append(results, CheckResult{
				Name:     "Session Brain Transcript",
				Category: CategoryStorage,
				Status:   StatusPass,
				Message:  fmt.Sprintf("Transcript log verified: %s", transcriptPath),
			})
		} else {
			results = append(results, CheckResult{
				Name:     "Session Brain Transcript",
				Category: CategoryStorage,
				Status:   StatusInfo,
				Message:  fmt.Sprintf("Transcript log not yet generated on disk: %s", transcriptPath),
			})
		}
	}

	return results
}
