package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// DefaultIPCAddress is the hook-only local IPC endpoint.
const DefaultIPCAddress = "127.0.0.1:49215"

// DefaultActionIPCAddress is a separate local endpoint for state-changing
// plugin actions. Keeping hooks and actions on different listeners prevents a
// hook client/protocol error from being interpreted as an administrative RPC.
const DefaultActionIPCAddress = "127.0.0.1:49216"

// Server coordinates IPC communication to receive and evaluate Antigravity hook requests
// and process management IPC actions from local plugins.
type Server struct {
	addr       string
	actionAddr string
	manager    ports.SecurityManagerPort
	policy     ports.PolicyEngine
	scheduler  ports.SchedulerPort
	scheduleDB ports.ScheduleRepository
	subagents  ports.SubagentDispatcherPort
	listener   net.Listener
	actionLn   net.Listener
	logger     *slog.Logger
	mu         sync.RWMutex
	running    bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewServer constructs a new IPC server instance.
func NewServer(manager ports.SecurityManagerPort, addr string, logger *slog.Logger) *Server {
	actionAddr := addr
	if addr == "" {
		addr = DefaultIPCAddress
		actionAddr = DefaultActionIPCAddress
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		addr:       addr,
		actionAddr: actionAddr,
		manager:    manager,
		logger:     logger,
	}
}

// SetPolicyEngine configures the centralized policy evaluator for IPC actions.
// IPC requests are capabilities of an active turn, never administrative tokens.
func (s *Server) SetPolicyEngine(policy ports.PolicyEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policy = policy
}

// SetScheduler sets the scheduler port for handling schedule/heartbeat IPC actions.
func (s *Server) SetScheduler(sched ports.SchedulerPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduler = sched
}

// SetScheduleStore supplies the authoritative schedule lookup used to scope
// destructive schedule actions before they reach the scheduler.
func (s *Server) SetScheduleStore(store ports.ScheduleRepository) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduleDB = store
}

// SetSubagents sets the subagent dispatcher port for handling background subagent IPC actions.
func (s *Server) SetSubagents(sub ports.SubagentDispatcherPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subagents = sub
}

// Start opens the IPC listener and handles incoming hook requests in the background.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}

	listener, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to start hook IPC server on %s: %w", s.addr, err)
	}
	var actionLn net.Listener
	if s.actionAddr != s.addr {
		actionLn, err = net.Listen("tcp", s.actionAddr)
		if err != nil {
			_ = listener.Close()
			s.mu.Unlock()
			return fmt.Errorf("failed to start action IPC server on %s: %w", s.actionAddr, err)
		}
	}

	s.listener = listener
	s.actionLn = actionLn
	s.running = true
	serverCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	s.logger.Info("Hook IPC server listening", "address", s.addr)
	if actionLn != nil {
		s.logger.Info("Action IPC server listening", "address", s.actionAddr)
	}

	s.wg.Add(1)
	go s.acceptLoop(serverCtx, listener, false)
	if actionLn != nil {
		s.wg.Add(1)
		go s.acceptLoop(serverCtx, actionLn, true)
	}

	return nil
}

// Stop gracefully shuts down the IPC listener.
func (s *Server) Stop() error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.actionLn != nil {
		_ = s.actionLn.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	s.logger.Info("Hook IPC server stopped cleanly")
	return nil
}

func (s *Server) acceptLoop(ctx context.Context, listener net.Listener, actionOnly bool) {
	defer s.wg.Done()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				s.mu.RLock()
				running := s.running
				s.mu.RUnlock()
				if !running {
					return
				}
				s.logger.Warn("IPC accept error", "error", err)
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			s.handleConnection(ctx, c, actionOnly)
		}(conn)
	}
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn, actionOnly bool) {
	defer conn.Close()

	if err := verifyPeerCredentials(conn); err != nil {
		s.logger.Warn("Rejected unauthorized IPC connection", "error", err)
		return
	}

	_ = conn.SetDeadline(time.Now().Add(65 * time.Second)) // Support 60s HITL timeout + buffer

	scanner := bufio.NewScanner(conn)
	// Buffer limit support for large hook payloads (e.g. diffs or code)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	if !scanner.Scan() {
		return
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
		s.logger.Error("Failed to unmarshal IPC payload", "error", err)
		resp := HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   "Malformed JSON payload",
		}
		respBytes, _ := json.Marshal(resp)
		_, _ = conn.Write(append(respBytes, '\n'))
		return
	}

	// 1. Check if this is a custom IPC action request (e.g. from scheduler plugin)
	if action, ok := raw["action"].(string); ok && action != "" {
		if !actionOnly && s.actionAddr != s.addr {
			s.writeActionError(conn, action, fmt.Errorf("forbidden: IPC actions are accepted only on the action endpoint"))
			return
		}
		turnID, _ := raw["turn_id"].(string)
		if turnID == "" {
			if p, ok := raw["params"].(map[string]interface{}); ok {
				turnID, _ = p["turn_id"].(string)
			}
		}
		callerTurn, err := s.resolveActionTurn(turnID)
		if err != nil {
			s.writeActionError(conn, action, err)
			return
		}
		params, _ := raw["params"].(map[string]interface{})
		if params == nil {
			params = raw
		}
		params, err = scopeActionParams(params, callerTurn)
		if err != nil {
			s.logger.Warn("IPC action scope denied", "action", action, "turn_id", callerTurn.TurnID, "error", err)
			s.writeActionError(conn, action, err)
			return
		}
		if err := s.authorizeAction(ctx, action, callerTurn); err != nil {
			s.logger.Warn("IPC action policy denied", "action", action, "turn_id", callerTurn.TurnID, "error", err)
			s.writeActionError(conn, action, err)
			return
		}
		raw["params"] = params

		s.handleAction(ctx, conn, action, raw)
		return
	}
	if actionOnly && s.actionAddr != s.addr {
		respBytes, _ := json.Marshal(HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   "forbidden: hook requests are accepted only on the hook endpoint",
		})
		_, _ = conn.Write(append(respBytes, '\n'))
		return
	}

	// 2. Otherwise process as standard HookRequest
	var req HookRequest
	_ = json.Unmarshal(scanner.Bytes(), &req)

	resp, err := s.HandleHookRequest(ctx, req)
	if err != nil {
		s.logger.Error("Error handling hook request", "error", err)
		resp = HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Internal security evaluation error: %v", err),
		}
	}

	respBytes, _ := json.Marshal(resp)
	_, _ = conn.Write(append(respBytes, '\n'))
}

func (s *Server) resolveActionTurn(turnID string) (domain.TurnSecurityContext, error) {
	if turnID == "" {
		return domain.TurnSecurityContext{}, fmt.Errorf("unauthorized: an active turn_id is required")
	}
	s.mu.RLock()
	manager := s.manager
	s.mu.RUnlock()
	if manager == nil {
		return domain.TurnSecurityContext{}, fmt.Errorf("unauthorized: security manager is not initialized")
	}
	turn, ok := manager.ResolveTurnByID(turnID)
	if !ok {
		return domain.TurnSecurityContext{}, fmt.Errorf("unauthorized: invalid or expired turn_id")
	}
	if turn.SessionKey == "" || turn.AgentName == "" {
		return domain.TurnSecurityContext{}, fmt.Errorf("unauthorized: turn lacks immutable session/agent scope")
	}
	return turn, nil
}

func (s *Server) authorizeAction(ctx context.Context, action string, turn domain.TurnSecurityContext) error {
	policyAction, kind, err := ipcPolicyAction(action)
	if err != nil {
		return err
	}
	s.mu.RLock()
	policy := s.policy
	s.mu.RUnlock()
	if policy == nil {
		return fmt.Errorf("authorization unavailable: policy engine is not initialized")
	}
	resource := turn.Resource
	resource.Kind = kind
	resource.SessionKey = turn.SessionKey
	resource.AgentName = turn.AgentName
	if resource.ID == "" {
		resource.ID = turn.AgentName
	}
	return policy.Authorize(ctx, turn.Principal, policyAction, resource)
}

func ipcPolicyAction(action string) (domain.Action, domain.ResourceKind, error) {
	switch action {
	case "dispatch_subagent":
		return domain.ActionTaskDispatch, domain.ResourceKindSubagentTask, nil
	case "check_subagent_progress", "get_subagent_task", "list_subagents":
		return domain.ActionTaskInspect, domain.ResourceKindSubagentTask, nil
	case "cancel_subagent_task":
		return domain.ActionTaskCancel, domain.ResourceKindSubagentTask, nil
	case "schedule_task", "create_schedule":
		return domain.ActionScheduleCreate, domain.ResourceKindSchedule, nil
	case "list_schedules":
		return domain.ActionScheduleList, domain.ResourceKindSchedule, nil
	case "cancel_schedule", "delete_schedule":
		return domain.ActionScheduleCancel, domain.ResourceKindSchedule, nil
	case "configure_heartbeat":
		return domain.ActionHeartbeatConfig, domain.ResourceKindAgent, nil
	case "get_heartbeat":
		return domain.ActionScheduleList, domain.ResourceKindAgent, nil
	case "trigger_heartbeat":
		return domain.ActionHeartbeatTrigger, domain.ResourceKindAgent, nil
	default:
		return "", "", fmt.Errorf("unsupported action %q", action)
	}
}

// scopeActionParams copies untrusted request parameters then fixes all
// identity-bearing values to the active turn. A turn capability can only act
// inside its own session and agent; callers cannot redirect a future schedule
// or task to another conversation by supplying a different target key.
func scopeActionParams(input map[string]interface{}, turn domain.TurnSecurityContext) (map[string]interface{}, error) {
	params := make(map[string]interface{}, len(input)+6)
	for key, value := range input {
		params[key] = value
	}

	for _, key := range []string{"parent_session_key", "session_key", "target_session_key"} {
		if value, ok := params[key].(string); ok && value != "" && value != turn.SessionKey {
			return nil, fmt.Errorf("forbidden: %s is outside the caller session scope", key)
		}
		params[key] = turn.SessionKey
	}
	if value, ok := params["agent_name"].(string); ok && value != "" && value != turn.AgentName {
		return nil, fmt.Errorf("forbidden: agent_name is outside the caller agent scope")
	}
	params["agent_name"] = turn.AgentName
	if value, ok := params["project_name"].(string); ok && value != "" && value != turn.ProjectName {
		return nil, fmt.Errorf("forbidden: project_name is outside the caller project scope")
	}
	params["project_name"] = turn.ProjectName
	if turn.Principal.Kind == domain.PrincipalUser && turn.Principal.SubjectID != "" {
		for _, key := range []string{"user_id", "created_by"} {
			if value, ok := params[key].(string); ok && value != "" && value != turn.Principal.SubjectID {
				return nil, fmt.Errorf("forbidden: %s does not match the caller identity", key)
			}
			params[key] = turn.Principal.SubjectID
		}
	}
	return params, nil
}

func (s *Server) writeActionError(conn net.Conn, action string, err error) {
	respBytes, _ := json.Marshal(map[string]interface{}{
		"success": false,
		"error":   err.Error(),
	})
	_, _ = conn.Write(append(respBytes, '\n'))
}

func (s *Server) handleAction(ctx context.Context, conn net.Conn, action string, raw map[string]interface{}) {
	params, _ := raw["params"].(map[string]interface{})
	if params == nil {
		params = raw
	}

	var (
		res any
		err error
	)

	switch action {
	case "dispatch_subagent":
		res, err = s.handleDispatchSubagent(ctx, params)
	case "check_subagent_progress", "get_subagent_task":
		res, err = s.handleGetSubagentTask(ctx, params)
	case "cancel_subagent_task":
		res, err = s.handleCancelSubagentTask(ctx, params)
	case "list_subagents":
		res, err = s.handleListSubagents(ctx, params)
	case "schedule_task", "create_schedule":
		res, err = s.handleScheduleTask(ctx, params)
	case "list_schedules":
		res, err = s.handleListSchedules(ctx, params)
	case "cancel_schedule", "delete_schedule":
		res, err = s.handleCancelSchedule(ctx, params)
	case "configure_heartbeat":
		res, err = s.handleConfigureHeartbeat(ctx, params)
	case "get_heartbeat":
		res, err = s.handleGetHeartbeat(ctx, params)
	case "trigger_heartbeat":
		res, err = s.handleTriggerHeartbeat(ctx, params)
	default:
		err = fmt.Errorf("unsupported action %q", action)
	}

	respMap := map[string]interface{}{
		"success": err == nil,
		"data":    res,
	}
	if err != nil {
		respMap["error"] = err.Error()
	}

	respBytes, _ := json.Marshal(respMap)
	_, _ = conn.Write(append(respBytes, '\n'))
}

func (s *Server) handleScheduleTask(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sched := s.scheduler
	s.mu.RUnlock()
	if sched == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	prompt, _ := p["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	title, _ := p["title"].(string)
	if strings.TrimSpace(title) == "" {
		title = prompt
		if len(title) > 40 {
			title = title[:37] + "..."
		}
	}
	agentName, _ := p["agent_name"].(string)
	if strings.TrimSpace(agentName) == "" {
		agentName = "agyent"
	}

	schedExpr, _ := p["schedule_expr"].(string)
	if schedExpr == "" {
		schedExpr, _ = p["time_expression"].(string)
	}
	if schedExpr == "" {
		schedExpr, _ = p["expression"].(string)
	}
	schedExpr = strings.TrimSpace(schedExpr)
	if schedExpr == "" {
		return nil, fmt.Errorf("time_expression or schedule_expr is required")
	}

	schedTypeStr, _ := p["schedule_type"].(string)
	schedTypeStr = strings.ToLower(strings.TrimSpace(schedTypeStr))
	var schedType domain.ScheduleType
	switch schedTypeStr {
	case "cron":
		schedType = domain.ScheduleTypeCron
	case "once", "one_off", "one-off":
		schedType = domain.ScheduleTypeOnce
	default:
		if strings.HasPrefix(schedExpr, "@") || len(strings.Fields(schedExpr)) == 5 {
			schedType = domain.ScheduleTypeCron
		} else {
			schedType = domain.ScheduleTypeOnce
		}
	}

	sessionKey, _ := p["session_key"].(string)
	targetSessionKey, _ := p["target_session_key"].(string)
	if targetSessionKey == "" {
		targetSessionKey = sessionKey
	}
	overlap, _ := p["overlap_policy"].(string)
	if overlap == "" {
		overlap = string(domain.OverlapPolicySkip)
	}
	channel, _ := p["channel"].(string)
	chatID, _ := p["chat_id"].(string)
	threadID, _ := p["thread_id"].(string)

	if targetSessionKey != "" {
		if parsed, err := domain.ParseSessionKey(targetSessionKey); err == nil {
			if chatID == "" {
				chatID = parsed.ChatID
			}
			if threadID == "" && parsed.ThreadID > 0 {
				threadID = strconv.FormatInt(parsed.ThreadID, 10)
			}
			if channel == "" {
				channel = parsed.Channel
			}
		}
	}

	createdBy, _ := p["user_id"].(string)
	if createdBy == "" {
		createdBy, _ = p["created_by"].(string)
	}
	if createdBy == "" {
		createdBy = "agent"
	}

	task := domain.ScheduleTask{
		AgentName:        agentName,
		Title:            title,
		Prompt:           prompt,
		ScheduleType:     schedType,
		ScheduleExpr:     schedExpr,
		TargetSessionKey: targetSessionKey,
		Channel:          channel,
		ChatID:           chatID,
		ThreadID:         threadID,
		OverlapPolicy:    domain.OverlapPolicy(overlap),
		CreatedBy:        createdBy,
	}

	return sched.CreateSchedule(ctx, task)
}

func (s *Server) handleListSchedules(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sched := s.scheduler
	s.mu.RUnlock()
	if sched == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	agentName, _ := p["agent_name"].(string)
	statusStr, _ := p["status"].(string)
	return sched.ListSchedules(ctx, agentName, domain.ScheduleStatus(statusStr))
}

func (s *Server) handleCancelSchedule(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sched := s.scheduler
	scheduleDB := s.scheduleDB
	s.mu.RUnlock()
	if sched == nil || scheduleDB == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	taskID, _ := p["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	stored, err := scheduleDB.GetSchedule(ctx, taskID)
	if err != nil {
		return nil, err
	}
	agentName, _ := p["agent_name"].(string)
	targetSessionKey, _ := p["target_session_key"].(string)
	if stored.AgentName != agentName || stored.TargetSessionKey != targetSessionKey {
		return nil, fmt.Errorf("forbidden: schedule is outside caller scope")
	}
	if err := sched.CancelSchedule(ctx, taskID); err != nil {
		return nil, err
	}
	return map[string]string{"task_id": taskID, "status": "CANCELLED"}, nil
}

func (s *Server) handleConfigureHeartbeat(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sched := s.scheduler
	s.mu.RUnlock()
	if sched == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	agentName, _ := p["agent_name"].(string)
	if strings.TrimSpace(agentName) == "" {
		agentName = "agyent"
	}

	existingCfg, existingPrompt, _ := sched.GetHeartbeat(ctx, agentName)
	cfg := domain.HeartbeatConfig{
		AgentName:       agentName,
		Enabled:         true,
		IntervalSeconds: 3600,
	}
	prompt := existingPrompt
	if existingCfg != nil {
		cfg = *existingCfg
	}

	if enabledVal, ok := p["enabled"]; ok {
		if b, ok := enabledVal.(bool); ok {
			cfg.Enabled = b
		}
	}

	if promptVal, ok := p["prompt"].(string); ok && strings.TrimSpace(promptVal) != "" {
		prompt = promptVal
	}

	if intervalVal, ok := p["interval"].(string); ok && strings.TrimSpace(intervalVal) != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(intervalVal)); err == nil && d > 0 {
			cfg.IntervalSeconds = int(d.Seconds())
		}
	} else if secVal, ok := p["interval_seconds"].(float64); ok && secVal > 0 {
		cfg.IntervalSeconds = int(secVal)
	}

	if targetKey, ok := p["target_session_key"].(string); ok && targetKey != "" {
		cfg.TargetSessionKey = targetKey
	} else if sessionKey, ok := p["session_key"].(string); ok && sessionKey != "" && cfg.TargetSessionKey == "" {
		cfg.TargetSessionKey = sessionKey
	}

	if cfg.TargetSessionKey != "" {
		if parsed, err := domain.ParseSessionKey(cfg.TargetSessionKey); err == nil {
			if cfg.ChatID == "" {
				cfg.ChatID = parsed.ChatID
			}
			if cfg.ThreadID == "" && parsed.ThreadID > 0 {
				cfg.ThreadID = strconv.FormatInt(parsed.ThreadID, 10)
			}
			if cfg.Channel == "" {
				cfg.Channel = parsed.Channel
			}
		}
	}

	if err := sched.ConfigureHeartbeat(ctx, cfg, prompt); err != nil {
		return nil, err
	}
	return map[string]any{
		"agent_name":       cfg.AgentName,
		"enabled":          cfg.Enabled,
		"interval_seconds": cfg.IntervalSeconds,
	}, nil
}

func (s *Server) handleGetHeartbeat(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sched := s.scheduler
	s.mu.RUnlock()
	if sched == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	agentName, _ := p["agent_name"].(string)
	if strings.TrimSpace(agentName) == "" {
		agentName = "agyent"
	}

	cfg, prompt, err := sched.GetHeartbeat(ctx, agentName)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"config": cfg,
		"prompt": prompt,
	}, nil
}

func (s *Server) handleTriggerHeartbeat(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sched := s.scheduler
	s.mu.RUnlock()
	if sched == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	agentName, _ := p["agent_name"].(string)
	if strings.TrimSpace(agentName) == "" {
		agentName = "agyent"
	}

	if err := sched.TriggerHeartbeatNow(ctx, agentName); err != nil {
		return nil, err
	}
	return map[string]string{
		"agent_name": agentName,
		"status":     "TRIGGERED",
	}, nil
}

// HandleHookRequest processes a HookRequest and returns a HookResponse.
func (s *Server) HandleHookRequest(ctx context.Context, req HookRequest) (HookResponse, error) {
	if s.manager == nil {
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   "Security Manager is not initialized (Default-Deny)",
		}, nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	switch req.HookType {
	case "pre", "":
		if req.TurnID == "" {
			return HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   "Missing TurnID: unauthenticated hook execution",
			}, nil
		}
		turnCtx, ok := s.manager.ResolveTurnByID(req.TurnID)
		if !ok {
			return HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   "Invalid or expired TurnID",
			}, nil
		}
		// Workspace and conversation identifiers emitted by the child are
		// untrusted metadata. Bind security evaluation to the immutable values
		// captured at turn admission, otherwise a child can escape its path jail
		// by claiming a broader workspace.
		ws := turnCtx.WorkspaceDir
		sessionKey := turnCtx.SessionKey

		evalReq := domain.ToolEvaluationRequest{
			ToolName:       req.ToolCall.Name,
			Args:           req.ToolCall.Args,
			ConversationID: turnCtx.ConversationID,
			SessionKey:     sessionKey,
			StepIdx:        req.StepIdx,
			WorkspaceDir:   ws,
		}

		decision, err := s.manager.EvaluateToolCall(ctx, evalReq)
		if err != nil {
			return HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   fmt.Sprintf("Evaluation error: %v", err),
			}, nil
		}

		return HookResponse{
			Decision:  string(decision.Decision),
			Reason:    decision.Reason,
			Overwrite: decision.Overwrite,
		}, nil

	case "post":
		if req.TurnID == "" {
			return HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   "Missing TurnID: unauthenticated hook execution",
			}, nil
		}
		if _, ok := s.manager.ResolveTurnByID(req.TurnID); !ok {
			return HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   "Invalid or expired TurnID",
			}, nil
		}

		// Handle PostToolUse output sanitization and return overwritten output
		if req.ToolCall.Name != "" && req.ToolCall.Args != nil {
			if out, ok := req.ToolCall.Args["output"].(string); ok && out != "" {
				sanitized, _ := s.manager.SanitizeToolOutput(ctx, req.ToolCall.Name, out)
				return HookResponse{
					Overwrite: map[string]interface{}{
						"output": sanitized,
					},
				}, nil
			}
		}
		return HookResponse{}, nil

	default:
		return HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Unsupported or unauthorized hook type %q", req.HookType),
		}, nil
	}
}

func (s *Server) handleDispatchSubagent(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sub := s.subagents
	s.mu.RUnlock()
	if sub == nil {
		return nil, fmt.Errorf("subagent dispatcher is not initialized")
	}

	title, _ := p["title"].(string)
	prompt, _ := p["prompt"].(string)
	if strings.TrimSpace(title) == "" || strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("title and prompt are required")
	}

	agentName, _ := p["agent_name"].(string)
	if strings.TrimSpace(agentName) == "" {
		agentName = "agyent"
	}
	model, _ := p["model"].(string)
	if strings.TrimSpace(model) == "" {
		model = "flash"
	}
	effort, _ := p["effort"].(string)
	if strings.TrimSpace(effort) == "" {
		effort = "low"
	}
	wsMode, _ := p["workspace_mode"].(string)
	if strings.TrimSpace(wsMode) == "" {
		wsMode = "share"
	}
	cbModeStr, _ := p["callback_mode"].(string)
	if strings.TrimSpace(cbModeStr) == "" {
		cbModeStr = "notify_user"
	}
	parentSessionKey, _ := p["parent_session_key"].(string)
	projectName, _ := p["project_name"].(string)

	task := domain.SubagentTask{
		Title:            title,
		Prompt:           prompt,
		AgentName:        agentName,
		Model:            model,
		Effort:           effort,
		WorkspaceMode:    wsMode,
		CallbackMode:     domain.SubagentCallbackMode(cbModeStr),
		ParentSessionKey: parentSessionKey,
		ProjectName:      projectName,
	}

	taskID, err := sub.DispatchTask(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("failed to dispatch subagent task: %w", err)
	}

	return map[string]interface{}{
		"task_id":    taskID,
		"status":     "PENDING",
		"agent_name": agentName,
		"title":      title,
		"model":      model,
		"message":    fmt.Sprintf("🚀 Successfully dispatched background sub-agent task: %s (%s). The background worker pool has enqueued it. Inform the user and conclude your turn immediately without waiting.", taskID, title),
	}, nil
}

func (s *Server) handleGetSubagentTask(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sub := s.subagents
	s.mu.RUnlock()
	if sub == nil {
		return nil, fmt.Errorf("subagent dispatcher is not initialized")
	}

	taskID, _ := p["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	sessionKey, _ := p["parent_session_key"].(string)
	if sessionKey == "" {
		sessionKey, _ = p["session_key"].(string)
	}
	if sessionKey == "" {
		return nil, fmt.Errorf("forbidden: parent_session_key is required")
	}
	task, err := sub.GetTaskScoped(ctx, sessionKey, taskID)
	if err != nil {
		return nil, err
	}
	if agentName, _ := p["agent_name"].(string); agentName == "" || task.AgentName != agentName {
		return nil, fmt.Errorf("forbidden: task is outside caller agent scope")
	}
	return task, nil
}

func (s *Server) handleCancelSubagentTask(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sub := s.subagents
	s.mu.RUnlock()
	if sub == nil {
		return nil, fmt.Errorf("subagent dispatcher is not initialized")
	}

	taskID, _ := p["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	sessionKey, _ := p["parent_session_key"].(string)
	if sessionKey == "" {
		sessionKey, _ = p["session_key"].(string)
	}
	if sessionKey == "" {
		return nil, fmt.Errorf("forbidden: parent_session_key is required")
	}
	task, err := sub.GetTaskScoped(ctx, sessionKey, taskID)
	if err != nil {
		return nil, err
	}
	if agentName, _ := p["agent_name"].(string); agentName == "" || task.AgentName != agentName {
		return nil, fmt.Errorf("forbidden: task is outside caller agent scope")
	}
	if err := sub.CancelTaskScoped(ctx, sessionKey, taskID); err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"task_id": taskID,
		"status":  "CANCELLED",
		"message": fmt.Sprintf("🛑 Task %s has been marked as cancelled.", taskID),
	}, nil
}

func (s *Server) handleListSubagents(ctx context.Context, p map[string]interface{}) (any, error) {
	s.mu.RLock()
	sub := s.subagents
	s.mu.RUnlock()
	if sub == nil {
		return nil, fmt.Errorf("subagent dispatcher is not initialized")
	}

	sessionKey, _ := p["parent_session_key"].(string)
	if sessionKey == "" {
		sessionKey, _ = p["session_key"].(string)
	}
	limit := 10
	if l, ok := p["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}

	tasks, total, err := sub.ListTasks(ctx, sessionKey, limit, 0)
	if err != nil {
		return nil, err
	}
	_ = total // The repository count can include tasks from other active agents in this session.
	agentName, _ := p["agent_name"].(string)
	visible := make([]domain.SubagentTask, 0, len(tasks))
	for _, task := range tasks {
		if task.AgentName == agentName {
			visible = append(visible, task)
		}
	}

	return map[string]interface{}{
		"tasks": visible,
		"count": len(visible),
	}, nil
}
