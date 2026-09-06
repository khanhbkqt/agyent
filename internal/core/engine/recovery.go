package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
)

// startRecoveryWorker initiates the asynchronous turn recovery process.
func (e *Engine) startRecoveryWorker(ctx context.Context) {
	concurrency.SafeGo(func() {
		// Brief delay to allow channel adapters and lock managers to complete initialization
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}

		if err := e.RecoverInterruptedTurns(ctx); err != nil {
			slog.WarnContext(ctx, "Turn recovery sweep encountered errors", "error", err)
		}
	})
}

// RecoverInterruptedTurns scans the storage for turns that were interrupted by a daemon restart/crash,
// and executes auto-recovery with bounded concurrency and session lock synchronization.
func (e *Engine) RecoverInterruptedTurns(ctx context.Context) error {
	if e.storage == nil {
		return nil
	}

	interruptedTurns, err := e.storage.ListInterruptedTurns(ctx)
	if err != nil {
		return fmt.Errorf("failed to list interrupted turns: %w", err)
	}

	if len(interruptedTurns) == 0 {
		return nil
	}

	slog.InfoContext(ctx, "Discovered interrupted turns from previous daemon run", "count", len(interruptedTurns))

	// Check if recovery is disabled
	if e.cfg != nil && (!e.cfg.Recovery.Enabled || strings.ToLower(e.cfg.Recovery.Mode) == "disabled") {
		for _, t := range interruptedTurns {
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), t.TurnID, domain.TurnStatusFailed, "auto-recovery disabled by configuration")
		}
		return nil
	}

	maxConcurrent := 2
	if e.cfg != nil && e.cfg.Recovery.MaxConcurrentRecoveries > 0 {
		maxConcurrent = e.cfg.Recovery.MaxConcurrentRecoveries
	}

	sem := make(chan struct{}, maxConcurrent)

	for _, turn := range interruptedTurns {
		// Discard ephemeral queries (/ask) without auto-recovery
		if turn.IsEphemeral {
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, "ephemeral turn discarded on daemon restart")
			continue
		}

		// Circuit breaker check
		if turn.RetryCount >= turn.MaxRetries {
			_ = e.storage.UpdateInFlightTurnStatus(ctx, turn.TurnID, domain.TurnStatusFailed, "exceeded maximum recovery attempts")
			if e.channel != nil {
				replyID := ""
				if turn.InboundMessageID > 0 {
					replyID = strconv.FormatInt(turn.InboundMessageID, 10)
				}
				promptSnippet := turn.Prompt
				if len(promptSnippet) > 100 {
					promptSnippet = promptSnippet[:97] + "..."
				}
				_ = e.channel.Send(ctx, domain.OutboundMessage{
					Channel:          turn.Channel,
					BotID:            turn.BotID,
					ChatID:           turn.ChatID,
					ThreadID:         parseThreadID(turn.ThreadID),
					Text:             fmt.Sprintf("⚠️ **Yêu cầu không thể tự động khôi phục:** Lần chạy trước bị gián đoạn do hệ thống restart.\n*Prompt:* `%s`", promptSnippet),
					ParseMode:        "Markdown",
					ReplyToMessageID: replyID,
				})
			}
			continue
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}

		currentTurn := turn
		concurrency.SafeGo(func() {
			defer func() { <-sem }()
			e.recoverSingleTurn(ctx, currentTurn)
		})
	}

	return nil
}

func (e *Engine) recoverSingleTurn(ctx context.Context, turn domain.InFlightTurn) {
	// 1. Transition status to RECOVERING and increment retry count
	turn.RetryCount++
	turn.Status = domain.TurnStatusRecovering
	turn.UpdatedAt = time.Now()
	_ = e.storage.SaveInFlightTurn(context.WithoutCancel(ctx), &turn)

	replyID := ""
	if turn.InboundMessageID > 0 {
		replyID = strconv.FormatInt(turn.InboundMessageID, 10)
	}

	// 2. Handle notify_only mode
	if e.cfg != nil && strings.ToLower(e.cfg.Recovery.Mode) == "notify_only" {
		if e.channel != nil {
			promptSnippet := turn.Prompt
			if len(promptSnippet) > 100 {
				promptSnippet = promptSnippet[:97] + "..."
			}
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				Channel:          turn.Channel,
				BotID:            turn.BotID,
				ChatID:           turn.ChatID,
				ThreadID:         parseThreadID(turn.ThreadID),
				Text:             fmt.Sprintf("⚠️ **Yêu cầu bị gián đoạn do hệ thống khởi động lại.**\n*Prompt:* `%s`\nVui lòng gửi lại nếu bạn muốn tiếp tục.", promptSnippet),
				ParseMode:        "Markdown",
				ReplyToMessageID: replyID,
			})
		}
		_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, "recovery mode notify_only")
		return
	}

	// 3. Acquire session FIFO lock
	timeout := 1800 * time.Second
	if e.cfg != nil && e.cfg.AGY.DefaultTimeoutSeconds > 0 {
		timeout = time.Duration(e.cfg.AGY.DefaultTimeoutSeconds) * time.Second
	}

	unlock, err := e.lockManager.Acquire(ctx, turn.SessionKey, timeout)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acquire session lock during turn recovery",
			"session_key", turn.SessionKey,
			"turn_id", turn.TurnID,
			"error", err,
		)
		_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, fmt.Sprintf("lock acquisition failed: %v", err))
		return
	}
	defer unlock()

	// 4. Send proactive notification to user
	if e.channel != nil {
		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:          turn.Channel,
			BotID:            turn.BotID,
			ChatID:           turn.ChatID,
			ThreadID:         parseThreadID(turn.ThreadID),
			Text:             "🔄 **Hệ thống vừa khởi động lại.** Đang tự động khôi phục và tiếp tục tác vụ trước đó của bạn...",
			ParseMode:        "Markdown",
			ReplyToMessageID: replyID,
		})
	}

	// 5. Setup execution parameters
	agentName := turn.AgentName
	if agentName == "" {
		agentName = "agyent"
	}
	agent, _ := e.storage.GetAgent(ctx, agentName)
	workspaceDir := ""
	if agent != nil && agent.WorkspacePath != "" {
		workspaceDir = agent.WorkspacePath
	} else if e.cfg != nil {
		workspaceDir = config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, agentName)
	}

	if turn.ProjectName != "" {
		projID := domain.FormatProjectID(agentName, turn.ProjectName)
		if proj, err := e.storage.GetProject(ctx, projID); err == nil && proj != nil && proj.ProjectPath != "" {
			workspaceDir = proj.ProjectPath
		}
	}

	if workspaceDir != "" {
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			slog.ErrorContext(ctx, "Failed to create agent workspace during recovery",
				"error", err,
				"workspace", workspaceDir,
				"turn_id", turn.TurnID,
			)
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, fmt.Sprintf("workspace creation failed: %v", err))
			return
		}
		if e.securityManager != nil {
			if err := e.securityManager.EnsureWorkspaceHooks(workspaceDir); err != nil {
				slog.ErrorContext(ctx, "Failed to ensure workspace security hooks during recovery",
					"error", err,
					"workspace", workspaceDir,
					"turn_id", turn.TurnID,
				)
				_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, fmt.Sprintf("security hook failure: %v", err))
				return
			}
		}
	}

	// 6. Build guarded continuation prompt or bootstrap recovery prompt
	var promptText string
	if turn.ConversationID != "" {
		promptText = fmt.Sprintf(
			"[SYSTEM AUTO-RECOVERY NOTIFICATION]\n"+
				"The system daemon was restarted while processing the previous turn.\n"+
				"1. Inspect the current workspace files and recent tool outputs to determine which actions were already completed.\n"+
				"2. Avoid re-executing non-idempotent side effects (e.g. git commits, external API calls, duplicate file appends) that have already succeeded.\n"+
				"3. Resume and complete the user's original request:\n"+
				"\"%s\"",
			turn.Prompt,
		)
	} else {
		promptText = fmt.Sprintf(
			"[SYSTEM AUTO-RECOVERY NOTIFICATION]\n"+
				"The system daemon was restarted during initial session bootstrap.\n"+
				"Proceed to initialize workspace directives if missing, and complete the user's original request:\n"+
				"\"%s\"",
			turn.Prompt,
		)
	}

	session, _ := e.storage.GetSession(ctx, turn.SessionKey)
	resolvedModel, resolvedEffort, _ := e.ResolveExecutionParams("", "", session, agent)

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             turn.ConversationID,
		TurnID:                     turn.TurnID,
		WorkspaceDir:               workspaceDir,
		Timeout:                    timeout,
		Model:                      resolvedModel,
		Effort:                     resolvedEffort,
		Mode:                       "accept-edits",
		DangerouslySkipPermissions: false,
		AgentName:                  agentName,
		ProjectName:                turn.ProjectName,
		SessionKey:                 turn.SessionKey,
		UserID:                     turn.UserID,
	}

	principal := domain.Principal{
		Kind:      domain.PrincipalUser,
		Provider:  turn.Channel,
		SubjectID: turn.UserID,
	}

	// 7. Execute turn
	var (
		res     *domain.ExecutionResult
		execErr error
	)

	if e.executionService != nil {
		res, execErr = e.executionService.ExecuteTurn(ctx, principal, req, turn.SessionKey, false)
	} else if e.runner != nil {
		res, execErr = e.runner.Execute(ctx, req)
	} else {
		execErr = fmt.Errorf("no execution service or runner available")
	}

	// 8. Handle execution outcome
	if execErr != nil || (res != nil && !res.Success) {
		errMsg := "turn recovery execution failed"
		if execErr != nil {
			errMsg = execErr.Error()
		} else if res != nil && res.Error != "" {
			errMsg = res.Error
		}

		slog.ErrorContext(ctx, "Turn auto-recovery execution failed",
			"turn_id", turn.TurnID,
			"session_key", turn.SessionKey,
			"error", errMsg,
		)
		_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, errMsg)

		if e.channel != nil {
			_ = e.channel.Send(ctx, domain.OutboundMessage{
				Channel:          turn.Channel,
				BotID:            turn.BotID,
				ChatID:           turn.ChatID,
				ThreadID:         parseThreadID(turn.ThreadID),
				Text:             fmt.Sprintf("⚠️ Khôi phục tác vụ thất bại: %s", errMsg),
				ParseMode:        "Markdown",
				ReplyToMessageID: replyID,
			})
		}
		return
	}

	// Succeeded
	_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusCompleted, "")

	if res != nil && res.ConversationID != "" && session != nil {
		session.SetActiveConversationID(res.ConversationID)
		_ = e.storage.SaveSession(context.WithoutCancel(ctx), session)
	}

	if res != nil && e.channel != nil {
		responseText := PruneToolOutput(res.ResponseText)
		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:          turn.Channel,
			BotID:            turn.BotID,
			ChatID:           turn.ChatID,
			ThreadID:         parseThreadID(turn.ThreadID),
			Text:             responseText,
			ParseMode:        "Markdown",
			ReplyToMessageID: replyID,
			WorkspaceDir:     workspaceDir,
			ConversationID:   req.ConversationID,
		})
	}
}

func parseThreadID(s string) int64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
