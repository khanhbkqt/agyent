package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agyent/internal/core/domain"
)

// LogAudit persists an execution audit log entry and populates log.ID with the generated ID.
func (s *SQLiteStore) LogAudit(ctx context.Context, log *domain.AuditLog) error {
	if log == nil {
		return errors.New("cannot log nil audit entry")
	}

	query := `
		INSERT INTO audit_logs (
			session_key, agent_name, project_name, conversation_id,
			model, effort,
			prompt_length, response_length, duration_seconds,
			input_tokens, output_tokens, thinking_tokens, cache_read_tokens, total_tokens,
			status, error_message, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	createdAt := log.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	status := log.Status
	if status == "" {
		status = "SUCCESS"
	}

	res, err := s.writer().ExecContext(ctx, query,
		log.SessionKey,
		log.AgentName,
		log.ProjectName,
		log.ConversationID,
		log.Model,
		log.Effort,
		log.PromptLength,
		log.ResponseLength,
		log.DurationSeconds,
		log.Usage.InputTokens,
		log.Usage.OutputTokens,
		log.Usage.ThinkingTokens,
		log.Usage.CacheReadTokens,
		log.Usage.TotalTokens,
		status,
		log.ErrorMessage,
		timeToMilli(createdAt),
	)
	if err != nil {
		return fmt.Errorf("failed to insert audit log: %w", err)
	}

	id, err := res.LastInsertId()
	if err == nil {
		log.ID = id
	}

	return nil
}

// ListAuditLogs retrieves recent audit logs for a given session key, ordered by created_at DESC.
func (s *SQLiteStore) ListAuditLogs(ctx context.Context, sessionKey string, limit int) ([]domain.AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, session_key, agent_name, project_name, conversation_id,
		       COALESCE(model, '') AS model, COALESCE(effort, '') AS effort,
		       prompt_length, response_length, duration_seconds,
		       input_tokens, output_tokens, thinking_tokens, cache_read_tokens, total_tokens,
		       status, error_message, created_at
		FROM audit_logs
		WHERE session_key = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?
	`
	rows, err := s.reader().QueryContext(ctx, query, sessionKey, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs for session %s: %w", sessionKey, err)
	}
	defer rows.Close()

	var logs = make([]domain.AuditLog, 0)
	for rows.Next() {
		var (
			id              int64
			sessKey         string
			agentName       string
			projName        string
			convID          string
			model           string
			effort          string
			promptLen       int
			respLen         int
			durationSec     float64
			inputTokens     int
			outputTokens    int
			thinkingTokens  int
			cacheReadTokens int
			totalTokens     int
			status          string
			errorMsg        string
			createdAt       FlexTime
		)

		err := rows.Scan(
			&id, &sessKey, &agentName, &projName, &convID,
			&model, &effort,
			&promptLen, &respLen, &durationSec,
			&inputTokens, &outputTokens, &thinkingTokens, &cacheReadTokens, &totalTokens,
			&status, &errorMsg, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan audit log row: %w", err)
		}

		logs = append(logs, domain.AuditLog{
			ID:              id,
			SessionKey:      sessKey,
			AgentName:       agentName,
			ProjectName:     projName,
			ConversationID:  convID,
			Model:           model,
			Effort:          effort,
			PromptLength:    promptLen,
			ResponseLength:  respLen,
			DurationSeconds: durationSec,
			Usage: domain.TokenUsage{
				InputTokens:     inputTokens,
				OutputTokens:    outputTokens,
				ThinkingTokens:  thinkingTokens,
				CacheReadTokens: cacheReadTokens,
				TotalTokens:     totalTokens,
			},
			Status:       status,
			ErrorMessage: errorMsg,
			CreatedAt:    createdAt.Time,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating audit logs: %w", err)
	}

	return logs, nil
}

// GetTokenStats computes aggregated token metrics for a session and optional conversation ID.
func (s *SQLiteStore) GetTokenStats(ctx context.Context, sessionKey string, convID string) (*domain.TokenUsage, error) {
	var query string
	var args []any

	if convID != "" {
		query = `
			SELECT COALESCE(SUM(input_tokens), 0),
			       COALESCE(SUM(output_tokens), 0),
			       COALESCE(SUM(thinking_tokens), 0),
			       COALESCE(SUM(cache_read_tokens), 0),
			       COALESCE(SUM(total_tokens), 0)
			FROM audit_logs
			WHERE session_key = ? AND conversation_id = ?
		`
		args = []any{sessionKey, convID}
	} else {
		query = `
			SELECT COALESCE(SUM(input_tokens), 0),
			       COALESCE(SUM(output_tokens), 0),
			       COALESCE(SUM(thinking_tokens), 0),
			       COALESCE(SUM(cache_read_tokens), 0),
			       COALESCE(SUM(total_tokens), 0)
			FROM audit_logs
			WHERE session_key = ?
		`
		args = []any{sessionKey}
	}

	var usage domain.TokenUsage
	err := s.reader().QueryRowContext(ctx, query, args...).Scan(
		&usage.InputTokens,
		&usage.OutputTokens,
		&usage.ThinkingTokens,
		&usage.CacheReadTokens,
		&usage.TotalTokens,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to compute token stats: %w", err)
	}

	return &usage, nil
}

// GetTokenEfficiencyReport aggregates session and global analytics over temporal windows, agent breakdown, and compaction metrics.
func (s *SQLiteStore) GetTokenEfficiencyReport(ctx context.Context, sessionKey string, agentName string) (*domain.TokenEfficiencyReport, error) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	midnightMilli := timeToMilli(midnight)
	sevenDaysAgoMilli := timeToMilli(now.AddDate(0, 0, -7))

	report := &domain.TokenEfficiencyReport{
		SessionKey:  sessionKey,
		AgentFilter: agentName,
		GeneratedAt: now,
	}

	// Base conditions
	var baseClauses []string
	var baseArgs []any

	if sessionKey != "" {
		baseClauses = append(baseClauses, "session_key = ?")
		baseArgs = append(baseArgs, sessionKey)
	}
	if agentName != "" {
		baseClauses = append(baseClauses, "agent_name = ?")
		baseArgs = append(baseArgs, agentName)
	}

	buildWhere := func(extraClause string, extraArgs ...any) (string, []any) {
		clauses := append([]string(nil), baseClauses...)
		args := append([]any(nil), baseArgs...)
		if extraClause != "" {
			clauses = append(clauses, extraClause)
			args = append(args, extraArgs...)
		}
		if len(clauses) == 0 {
			return "", args
		}
		return "WHERE " + strings.Join(clauses, " AND "), args
	}

	// 1. Today Usage & Turns
	todayWhere, todayArgs := buildWhere("created_at >= ?", midnightMilli)
	todayQuery := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(thinking_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(total_tokens), 0)
		FROM audit_logs
		%s
	`, todayWhere)
	_ = s.reader().QueryRowContext(ctx, todayQuery, todayArgs...).Scan(
		&report.TodayTurns,
		&report.TodayUsage.InputTokens,
		&report.TodayUsage.OutputTokens,
		&report.TodayUsage.ThinkingTokens,
		&report.TodayUsage.CacheReadTokens,
		&report.TodayUsage.TotalTokens,
	)

	// 2. Past 7 Days Usage & Turns
	past7Where, past7Args := buildWhere("created_at >= ?", sevenDaysAgoMilli)
	past7Query := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(thinking_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(total_tokens), 0)
		FROM audit_logs
		%s
	`, past7Where)
	_ = s.reader().QueryRowContext(ctx, past7Query, past7Args...).Scan(
		&report.Past7DaysTurns,
		&report.Past7DaysUsage.InputTokens,
		&report.Past7DaysUsage.OutputTokens,
		&report.Past7DaysUsage.ThinkingTokens,
		&report.Past7DaysUsage.CacheReadTokens,
		&report.Past7DaysUsage.TotalTokens,
	)

	// 3. All-Time Usage & Turns
	allWhere, allArgs := buildWhere("")
	allQuery := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(thinking_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(total_tokens), 0)
		FROM audit_logs
		%s
	`, allWhere)
	_ = s.reader().QueryRowContext(ctx, allQuery, allArgs...).Scan(
		&report.AllTimeTurns,
		&report.AllTimeUsage.InputTokens,
		&report.AllTimeUsage.OutputTokens,
		&report.AllTimeUsage.ThinkingTokens,
		&report.AllTimeUsage.CacheReadTokens,
		&report.AllTimeUsage.TotalTokens,
	)

	report.AvgCacheHitRatio = report.AllTimeUsage.CacheHitRatio()
	report.TotalCostSavedPct = report.AllTimeUsage.EffectiveCostSavingsRatio()

	// 4. Model Breakdown
	modelQuery := fmt.Sprintf(`
		SELECT COALESCE(NULLIF(model, ''), 'default'),
		       COUNT(*),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(thinking_tokens), 0),
		       COALESCE(SUM(cache_read_tokens), 0),
		       COALESCE(SUM(total_tokens), 0)
		FROM audit_logs
		%s
		GROUP BY COALESCE(NULLIF(model, ''), 'default')
		ORDER BY SUM(total_tokens) DESC
	`, allWhere)

	rows, err := s.reader().QueryContext(ctx, modelQuery, allArgs...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b domain.ModelTokenBreakdown
			if err := rows.Scan(
				&b.Model,
				&b.TurnCount,
				&b.Usage.InputTokens,
				&b.Usage.OutputTokens,
				&b.Usage.ThinkingTokens,
				&b.Usage.CacheReadTokens,
				&b.Usage.TotalTokens,
			); err == nil {
				cap, _, _ := domain.LookupModelCapability(b.Model, nil)
				b.DisplayName = cap.DisplayName
				if b.DisplayName == "" {
					b.DisplayName = b.Model
				}
				report.ModelBreakdown = append(report.ModelBreakdown, b)
			}
		}
	}

	// 5. Agent Breakdown (if not already filtered to a single agent)
	if agentName == "" {
		agentWhere, agentArgs := buildWhere("")
		agentQuery := fmt.Sprintf(`
			SELECT COALESCE(NULLIF(agent_name, ''), 'default'),
			       COUNT(*),
			       COALESCE(SUM(input_tokens), 0),
			       COALESCE(SUM(output_tokens), 0),
			       COALESCE(SUM(thinking_tokens), 0),
			       COALESCE(SUM(cache_read_tokens), 0),
			       COALESCE(SUM(total_tokens), 0)
			FROM audit_logs
			%s
			GROUP BY COALESCE(NULLIF(agent_name, ''), 'default')
			ORDER BY SUM(total_tokens) DESC
		`, agentWhere)

		if aRows, err := s.reader().QueryContext(ctx, agentQuery, agentArgs...); err == nil {
			defer aRows.Close()
			for aRows.Next() {
				var ab domain.AgentTokenBreakdown
				if err := aRows.Scan(
					&ab.AgentName,
					&ab.TurnCount,
					&ab.Usage.InputTokens,
					&ab.Usage.OutputTokens,
					&ab.Usage.ThinkingTokens,
					&ab.Usage.CacheReadTokens,
					&ab.Usage.TotalTokens,
				); err == nil {
					report.AgentBreakdown = append(report.AgentBreakdown, ab)
				}
			}
		}
	}

	// 6. Compaction Savings
	var compClauses []string
	var compArgs []any
	compClauses = append(compClauses, "is_archived = 1", "title LIKE '[Compacted]%'")
	if sessionKey != "" {
		compClauses = append(compClauses, "session_key = ?")
		compArgs = append(compArgs, sessionKey)
	}
	if agentName != "" {
		compClauses = append(compClauses, "agent_name = ?")
		compArgs = append(compArgs, agentName)
	}
	compQuery := fmt.Sprintf("SELECT COUNT(*) FROM conversations WHERE %s", strings.Join(compClauses, " AND "))
	_ = s.reader().QueryRowContext(ctx, compQuery, compArgs...).Scan(&report.TotalCompactions)
	if report.TotalCompactions > 0 {
		report.EstTokensSaved = int64(report.TotalCompactions) * 700000
	}

	return report, nil
}

// LogSecurityEvent persists a security event into the security_audit_events table.
func (s *SQLiteStore) LogSecurityEvent(ctx context.Context, evt *domain.AuditSecurityEvent) error {
	if evt == nil {
		return errors.New("cannot log nil security event")
	}

	query := `
		INSERT INTO security_audit_events (
			id, timestamp, event_type, actor_id, actor_provider,
			action, resource_kind, resource_id, decision, details
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	id := evt.EventID
	if id == "" {
		id = fmt.Sprintf("sec-%d", time.Now().UnixNano())
		evt.EventID = id
	}

	ts := evt.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	details := evt.Reason
	if evt.Metadata != nil {
		if metaBytes, err := json.Marshal(evt.Metadata); err == nil {
			details = fmt.Sprintf("%s | meta: %s", details, string(metaBytes))
		}
	}

	actorID := fmt.Sprintf("%d", evt.UserID)
	actorProvider := "system"
	if evt.SessionKey != "" {
		parts := strings.SplitN(evt.SessionKey, ":", 2)
		if len(parts) > 0 {
			actorProvider = parts[0]
		}
	}

	action := evt.ToolName
	if action == "" {
		action = evt.Checkpoint
	}

	resourceKind := "agent"
	if evt.ToolName != "" {
		resourceKind = "tool"
	}

	resourceID := evt.TargetResource
	if resourceID == "" {
		resourceID = evt.AgentName
	}

	_, err := s.writer().ExecContext(ctx, query,
		id,
		ts.Unix(),
		evt.Checkpoint,
		actorID,
		actorProvider,
		action,
		resourceKind,
		resourceID,
		string(evt.Decision),
		details,
	)
	if err != nil {
		return fmt.Errorf("failed to insert security audit event: %w", err)
	}

	return nil
}

// ListSecurityEvents retrieves recent security audit events ordered by timestamp DESC.
func (s *SQLiteStore) ListSecurityEvents(ctx context.Context, limit int) ([]domain.AuditSecurityEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		SELECT id, timestamp, event_type, actor_id, action, resource_id, decision, details
		FROM security_audit_events
		ORDER BY timestamp DESC, id DESC
		LIMIT ?
	`

	rows, err := s.reader().QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list security audit events: %w", err)
	}
	defer rows.Close()

	var events []domain.AuditSecurityEvent
	for rows.Next() {
		var (
			id           string
			tsSec        int64
			checkpoint   string
			actorIDStr   string
			action       string
			resourceID   string
			decision     string
			details      string
		)
		if err := rows.Scan(&id, &tsSec, &checkpoint, &actorIDStr, &action, &resourceID, &decision, &details); err != nil {
			return nil, fmt.Errorf("failed to scan security audit event: %w", err)
		}

		var uid int64
		_, _ = fmt.Sscanf(actorIDStr, "%d", &uid)

		events = append(events, domain.AuditSecurityEvent{
			EventID:        id,
			Timestamp:      time.Unix(tsSec, 0),
			Checkpoint:     checkpoint,
			UserID:         uid,
			ToolName:       action,
			TargetResource: resourceID,
			Decision:       domain.SecurityDecisionType(decision),
			Reason:         details,
		})
	}

	return events, nil
}
