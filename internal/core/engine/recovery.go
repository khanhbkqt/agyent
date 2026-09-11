package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agyent/internal/config"
	"agyent/internal/core/concurrency"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// startRecoveryWorker initiates the asynchronous turn recovery process.
func (e *Engine) startRecoveryWorker(ctx context.Context) {
	e.wg.Add(1)
	concurrency.SafeGo(func() {
		defer e.wg.Done()
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
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, "exceeded maximum recovery attempts")
			if e.channel != nil {
				replyID := ""
				if turn.InboundMessageID > 0 {
					replyID = strconv.FormatInt(turn.InboundMessageID, 10)
				}
				promptSnippet := truncatePromptSnippet(turn.Prompt, 100)
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
		e.wg.Add(1)
		concurrency.SafeGo(func() {
			defer e.wg.Done()
			defer func() { <-sem }()
			e.recoverSingleTurn(ctx, currentTurn)
		})
	}

	return nil
}

func truncatePromptSnippet(prompt string, maxLen int) string {
	runes := []rune(prompt)
	if len(runes) <= maxLen {
		return prompt
	}
	if maxLen > 3 {
		return string(runes[:maxLen-3]) + "..."
	}
	return string(runes[:maxLen])
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
		Sender:    domain.SenderUser{ID: turn.UserID, Username: turn.UserName, Provider: turn.Channel},
		Text:      turn.Prompt,
		BindAgent: turn.AgentName,
	}

	// 2. Handle notify_only mode
	if e.cfg != nil && strings.ToLower(e.cfg.Recovery.Mode) == "notify_only" {
		if e.channel != nil {
			promptSnippet := truncatePromptSnippet(turn.Prompt, 100)
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

	turnCtx, turnCancel := context.WithCancel(ctx)
	defer turnCancel()

	e.registerActiveTurn(turn.SessionKey, turn.TurnID, turnCancel)
	defer e.unregisterActiveTurn(turn.SessionKey, turn.TurnID)

	// 4. Send proactive notification to user
	if e.channel != nil {
		_ = e.channel.Send(turnCtx, domain.OutboundMessage{
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
	agent, _ := e.storage.GetAgent(turnCtx, agentName)
	workspaceDir := ""
	if agent != nil && agent.WorkspacePath != "" {
		workspaceDir = agent.WorkspacePath
	} else if e.cfg != nil {
		workspaceDir = config.ResolveAgentWorkspace(e.cfg.Storage.AgentsDir, agentName)
	}

	if turn.ProjectName != "" {
		projID := domain.FormatProjectID(agentName, turn.ProjectName)
		if proj, err := e.storage.GetProject(turnCtx, projID); err == nil && proj != nil && proj.ProjectPath != "" {
			workspaceDir = proj.ProjectPath
		}
	}

	if workspaceDir != "" {
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			slog.ErrorContext(turnCtx, "Failed to create agent workspace during recovery",
				"error", err,
				"workspace", workspaceDir,
				"turn_id", turn.TurnID,
			)
			_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, fmt.Sprintf("workspace creation failed: %v", err))
			return
		}
		if e.securityManager != nil {
			if err := e.securityManager.EnsureWorkspaceHooks(workspaceDir); err != nil {
				slog.ErrorContext(turnCtx, "Failed to ensure workspace security hooks during recovery",
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

	session, _ := e.storage.GetSession(turnCtx, turn.SessionKey)
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
			_ = e.storage.SaveAgent(turnCtx, agent)
		}

		if isBootstrap && agent != nil {
			promptText = BuildBootstrapPrompt(agent, canonicalMsg.Sender, recoveryNotice)
		} else if agent != nil {
			var resolved *domain.ResolvedContext
			if e.contextResolver != nil {
				resolved, _ = e.contextResolver.Resolve(turnCtx, agent.WorkspacePath, workspaceDir)
			}
			if e.pluginManager != nil {
				if pluginResolved, _ := e.pluginManager.AssembleActivePlugins(turnCtx, agent.WorkspacePath, workspaceDir); pluginResolved != nil {
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

			if resolved != nil {
				sort.SliceStable(resolved.SkillHeaders, func(i, j int) bool {
					if resolved.SkillHeaders[i].Scope != resolved.SkillHeaders[j].Scope {
						return resolved.SkillHeaders[i].Scope < resolved.SkillHeaders[j].Scope
					}
					if resolved.SkillHeaders[i].Name != resolved.SkillHeaders[j].Name {
						return resolved.SkillHeaders[i].Name < resolved.SkillHeaders[j].Name
					}
					return resolved.SkillHeaders[i].FilePath < resolved.SkillHeaders[j].FilePath
				})
				sort.SliceStable(resolved.ActiveMCPServers, func(i, j int) bool {
					return resolved.ActiveMCPServers[i].ServerName < resolved.ActiveMCPServers[j].ServerName
				})
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

	mode := "accept-edits"
	skipPerms := false
	if e.cfg != nil {
		if e.cfg.AGY.DefaultMode != "" {
			mode = e.cfg.AGY.DefaultMode
		}
		skipPerms = e.cfg.AGY.DangerouslySkipPermissions
	}

	req := domain.ExecutionRequest{
		Prompt:                     promptText,
		ConversationID:             turn.ConversationID,
		TurnID:                     turn.TurnID,
		WorkspaceDir:               workspaceDir,
		Timeout:                    timeout,
		Model:                      resolvedModel,
		Effort:                     resolvedEffort,
		Mode:                       mode,
		DangerouslySkipPermissions: skipPerms,
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

	var activeMCPServers []domain.MCPServerConfig
	if e.pluginManager != nil && agent != nil {
		pluginResolved, _ := e.pluginManager.AssembleActivePlugins(turnCtx, agent.WorkspacePath, workspaceDir)
		if pluginResolved != nil {
			activeMCPServers = pluginResolved.ActiveMCPServers
		}
	}
	if len(activeMCPServers) > 0 && e.mcpRegistry != nil {
		releaseMCPLease, leaseErr := e.mcpRegistry.AcquireExclusiveTurn(turnCtx, turn.SessionKey)
		if leaseErr == nil {
			defer releaseMCPLease()
			activeServers := make([]domain.MCPServerConfig, len(activeMCPServers))
			copy(activeServers, activeMCPServers)
			for i := range activeServers {
				envCopy := make(map[string]string, len(activeServers[i].Env)+5)
				for k, v := range activeServers[i].Env {
					envCopy[k] = v
				}
				envCopy["AGYENT_AGENT_NAME"] = agentName
				envCopy["AGYENT_AGENT_WORKSPACE"] = workspaceDir
				if turn.SessionKey != "" {
					envCopy["AGYENT_SESSION_KEY"] = turn.SessionKey
				}
				if turn.UserID != "" {
					envCopy["AGYENT_USER_ID"] = turn.UserID
				}
				if turn.TurnID != "" {
					envCopy["AGYENT_TURN_ID"] = turn.TurnID
				}
				activeServers[i].Env = envCopy
			}
			if mountErr := e.mcpRegistry.MountServers(turnCtx, turn.SessionKey, activeServers); mountErr == nil {
				defer func() {
					_ = e.mcpRegistry.UnmountServers(context.Background(), turn.SessionKey, activeServers)
				}()
			}
		}
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
			_ = e.channel.SendTyping(turnCtx, canonicalMsg.TargetContext())
			ticker := time.NewTicker(4 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-typingStop:
					return
				case <-turnCtx.Done():
					return
				case <-ticker.C:
					_ = e.channel.SendTyping(turnCtx, canonicalMsg.TargetContext())
				}
			}
		})
	}

	if e.executionService == nil && e.securityManager != nil {
		preset := domain.PresetBalanced
		if agent != nil && agent.SecurityPreset != "" {
			preset = agent.SecurityPreset
		}
		isAdmin := e.isSenderSuperAdmin(canonicalMsg.Sender)
		e.securityManager.RegisterActiveTurn(domain.TurnSecurityContext{
			TurnID:         turn.TurnID,
			ConversationID: turn.ConversationID,
			SessionKey:     turn.SessionKey,
			Principal:      principal,
			IsAdmin:        isAdmin,
			WorkspaceDir:   workspaceDir,
			Preset:         preset,
			AgentName:      agentName,
			CreatedAt:      time.Now(),
		})
		defer e.securityManager.UnregisterTurnByID(turn.TurnID)
	}

	if e.executionService != nil {
		res, execErr = e.executionService.ExecuteTurn(turnCtx, principal, req, turn.SessionKey, isStream)
	} else if e.runner != nil {
		if isStream {
			res, execErr = e.runner.ExecuteStream(turnCtx, req, turn.SessionKey)
		} else {
			res, execErr = e.runner.Execute(turnCtx, req)
		}
	} else {
		execErr = fmt.Errorf("no execution service or runner available")
	}

	if errors.Is(execErr, ports.ErrConversationNotFound) {
		req.ConversationID = ""
		if e.executionService != nil {
			res, execErr = e.executionService.ExecuteTurn(turnCtx, principal, req, turn.SessionKey, isStream)
		} else if e.runner != nil {
			if isStream {
				res, execErr = e.runner.ExecuteStream(turnCtx, req, turn.SessionKey)
			} else {
				res, execErr = e.runner.Execute(turnCtx, req)
			}
		}
	}

	// 8. Handle execution outcome
	isInterrupted := errors.Is(turnCtx.Err(), context.Canceled) || (execErr != nil && strings.Contains(strings.ToLower(execErr.Error()), "cancelled"))

	if execErr != nil || (res != nil && !res.Success) {
		errMsg := "turn recovery execution failed"
		if isInterrupted {
			errMsg = "recovery cancelled or interrupted by user"
		} else if execErr != nil {
			errMsg = execErr.Error()
		} else if res != nil && res.Error != "" {
			errMsg = res.Error
		}

		slog.ErrorContext(ctx, "Turn auto-recovery execution failed",
			"turn_id", turn.TurnID,
			"session_key", turn.SessionKey,
			"error", errMsg,
			"interrupted", isInterrupted,
		)
		_ = e.storage.UpdateInFlightTurnStatus(context.WithoutCancel(ctx), turn.TurnID, domain.TurnStatusFailed, errMsg)

		if e.channel != nil && !isInterrupted {
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

	finalConvID := req.ConversationID
	if res != nil && res.ConversationID != "" {
		finalConvID = res.ConversationID
	}

	if currentSession, err := e.storage.GetSession(turnCtx, turn.SessionKey); err == nil && currentSession != nil {
		if finalConvID != "" {
			currentSession.SetActiveConversationID(finalConvID)
			_ = e.storage.SaveSession(context.WithoutCancel(ctx), currentSession)
			_ = e.storage.TouchConversation(context.WithoutCancel(ctx), turn.SessionKey, agentName, turn.ProjectName, finalConvID, turn.Prompt)
		}
	}

	// In non-streaming mode (or for auto-recovery where throttler did not deliver stream), deliver final message with detached context
	if res != nil && e.channel != nil {
		sendCtx, sendCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer sendCancel()

		_ = e.channel.Send(sendCtx, domain.OutboundMessage{
			Channel:          turn.Channel,
			BotID:            turn.BotID,
			ChatID:           turn.ChatID,
			ThreadID:         threadID,
			AgentName:        turn.AgentName,
			SessionKey:       turn.SessionKey,
			Text:             res.ResponseText,
			ParseMode:        "Markdown",
			ReplyToMessageID: replyID,
			WorkspaceDir:     workspaceDir,
			ConversationID:   finalConvID,
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
