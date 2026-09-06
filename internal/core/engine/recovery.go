package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
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

	threadID := parseThreadID(turn.ThreadID)
	canonicalMsg := domain.CanonicalMessage{
		ID:        strconv.FormatInt(turn.InboundMessageID, 10),
		Channel:   turn.Channel,
		BotID:     turn.BotID,
		Chat:      domain.ChatContext{ID: turn.ChatID, ThreadID: threadID},
		Sender:    domain.SenderUser{ID: turn.UserID, Username: turn.UserName},
		Text:      turn.Prompt,
		BindAgent: turn.AgentName,
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
				ThreadID:         threadID,
				AgentName:        turn.AgentName,
				SessionKey:       turn.SessionKey,
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
			ThreadID:         threadID,
			AgentName:        turn.AgentName,
			SessionKey:       turn.SessionKey,
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

	// 6. Build guarded continuation prompt or full bootstrap prompt
	recoveryNotice := fmt.Sprintf(
		"[SYSTEM AUTO-RECOVERY NOTIFICATION]\n"+
			"The system daemon was restarted while processing the previous turn.\n"+
			"1. Inspect the current workspace files and recent tool outputs to determine which actions were already completed.\n"+
			"2. Avoid re-executing non-idempotent side effects (e.g. git commits, external API calls, duplicate file appends) that have already succeeded.\n"+
			"3. Resume and complete the user's original request:\n"+
			"\"%s\"",
		turn.Prompt,
	)

	session, _ := e.storage.GetSession(ctx, turn.SessionKey)
	var promptText string

	if turn.ConversationID != "" {
		// Subsequent turn: AGY CLI maintains context, history, and workspace files in its brain.
		promptText = recoveryNotice
	} else {
		// Fresh turn without prior ConversationID: inject full Level 0-4 foundation and directives.
		isBootstrap := agent == nil || !agent.IsInitialized()
		if isBootstrap && agent != nil && e.workspaceManager != nil && e.workspaceManager.HasDirectives(agent.WorkspacePath) {
			isBootstrap = false
			agent.Status = domain.StatusInitialized
			_ = e.storage.SaveAgent(ctx, agent)
		}

		if isBootstrap && agent != nil {
			promptText = BuildBootstrapPrompt(agent, canonicalMsg.Sender, recoveryNotice)
		} else if agent != nil {
			var resolved *domain.ResolvedContext
			if e.contextResolver != nil {
				resolved, _ = e.contextResolver.Resolve(ctx, agent.WorkspacePath, workspaceDir)
			}
			if e.pluginManager != nil {
				if pluginResolved, _ := e.pluginManager.AssembleActivePlugins(ctx, agent.WorkspacePath, workspaceDir); pluginResolved != nil {
					if resolved == nil {
						resolved = pluginResolved
					} else {
						if pluginResolved.WorkspaceDirectives != "" {
							resolved.CombinedDirectives += "\n\n" + pluginResolved.WorkspaceDirectives
						}
						resolved.SkillHeaders = append(resolved.SkillHeaders, pluginResolved.SkillHeaders...)
						resolved.ActiveMCPServers = append(resolved.ActiveMCPServers, pluginResolved.ActiveMCPServers...)
					}
				}
			}

			var temporalTag string
			if e.temporal != nil && session != nil && !session.UpdatedAt.IsZero() {
				loc := time.Local
				if resolved != nil && resolved.UserLocation != nil {
					loc = resolved.UserLocation
				}
				temporalTag = e.temporal.FormatTemporalTag(session.UpdatedAt, time.Now(), loc)
			}

			msgWithNotice := canonicalMsg
			msgWithNotice.Text = recoveryNotice
			if resolved != nil {
				promptText = ComposeResolvedTurnPrompt(resolved, msgWithNotice, temporalTag, "")
			} else {
				knowledgeDirectives := LoadAgentKnowledgeDirectives(agent.WorkspacePath)
				promptText = ComposeTurnPrompt(knowledgeDirectives, msgWithNotice)
			}
		} else {
			promptText = recoveryNotice
		}
	}

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

	isStream := e.IsStreamingEnabled()

	// 7. Execute turn with heartbeat typing when not streaming
	var (
		res     *domain.ExecutionResult
		execErr error
	)

	if !isStream && e.channel != nil {
		typingStop := make(chan struct{})
		var typingOnce sync.Once
		stopTyping := func() {
			typingOnce.Do(func() {
				close(typingStop)
			})
		}
		defer stopTyping()

		concurrency.SafeGo(func() {
			_ = e.channel.SendTyping(ctx, canonicalMsg.TargetContext())
			ticker := time.NewTicker(4 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-typingStop:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = e.channel.SendTyping(ctx, canonicalMsg.TargetContext())
				}
			}
		})
	}

	if e.executionService != nil {
		res, execErr = e.executionService.ExecuteTurn(ctx, principal, req, turn.SessionKey, isStream)
	} else if e.runner != nil {
		if isStream {
			res, execErr = e.runner.ExecuteStream(ctx, req, turn.SessionKey)
		} else {
			res, execErr = e.runner.Execute(ctx, req)
		}
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
				ThreadID:         threadID,
				AgentName:        turn.AgentName,
				SessionKey:       turn.SessionKey,
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

	// In non-streaming mode (or if channel is non-telegram where throttler didn't deliver stream), deliver final message
	if res != nil && e.channel != nil && (!isStream || turn.Channel != "telegram") {
		responseText := res.ResponseText
		_ = e.channel.Send(ctx, domain.OutboundMessage{
			Channel:          turn.Channel,
			BotID:            turn.BotID,
			ChatID:           turn.ChatID,
			ThreadID:         threadID,
			AgentName:        turn.AgentName,
			SessionKey:       turn.SessionKey,
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
