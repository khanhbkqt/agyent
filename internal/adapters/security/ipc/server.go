package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// DefaultIPCAddress is the standard local IPC endpoint.
const DefaultIPCAddress = "127.0.0.1:49215"

// Server coordinates IPC communication to receive and evaluate Antigravity hook requests
// and process management IPC actions from local plugins.
type Server struct {
	addr      string
	manager   ports.SecurityManagerPort
	scheduler ports.SchedulerPort
	listener  net.Listener
	logger    *slog.Logger
	mu        sync.RWMutex
	running   bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewServer constructs a new IPC server instance.
func NewServer(manager ports.SecurityManagerPort, addr string, logger *slog.Logger) *Server {
	if addr == "" {
		addr = DefaultIPCAddress
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		addr:    addr,
		manager: manager,
		logger:  logger,
	}
}

// SetScheduler sets the scheduler port for handling schedule/heartbeat IPC actions.
func (s *Server) SetScheduler(sched ports.SchedulerPort) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduler = sched
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

	s.listener = listener
	s.running = true
	serverCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	s.logger.Info("Hook IPC server listening", "address", s.addr)

	s.wg.Add(1)
	go s.acceptLoop(serverCtx)

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
	s.mu.Unlock()

	s.wg.Wait()
	s.logger.Info("Hook IPC server stopped cleanly")
	return nil
}

func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()

	for {
		conn, err := s.listener.Accept()
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
			s.handleConnection(ctx, c)
		}(conn)
	}
}

func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

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
		s.handleAction(ctx, conn, action, raw)
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
	s.mu.RUnlock()
	if sched == nil {
		return nil, fmt.Errorf("scheduler is not initialized")
	}

	taskID, _ := p["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task_id is required")
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
		var ws string
		if len(req.WorkspacePaths) > 0 {
			ws = req.WorkspacePaths[0]
		}
		sessionKey := s.manager.ResolveSessionKey(req.ConversationID, ws)
		evalReq := domain.ToolEvaluationRequest{
			ToolName:       req.ToolCall.Name,
			Args:           req.ToolCall.Args,
			ConversationID: req.ConversationID,
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
			Decision: string(domain.DecisionAllow),
		}, nil
	}
}
