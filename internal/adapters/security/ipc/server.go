package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// DefaultIPCAddress is the standard local IPC endpoint.
const DefaultIPCAddress = "127.0.0.1:49215"

// Server implements ports.HookIPCPort to receive and evaluate Antigravity hook requests.
type Server struct {
	addr     string
	manager  ports.SecurityManagerPort
	listener net.Listener
	logger   *slog.Logger
	mu       sync.RWMutex
	running  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
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

	var req domain.HookRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		s.logger.Error("Failed to unmarshal hook request", "error", err)
		resp := domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   "Malformed hook JSON payload",
		}
		respBytes, _ := json.Marshal(resp)
		_, _ = conn.Write(append(respBytes, '\n'))
		return
	}

	resp, err := s.HandleHookRequest(ctx, req)
	if err != nil {
		s.logger.Error("Error handling hook request", "error", err)
		resp = domain.HookResponse{
			Decision: string(domain.DecisionDeny),
			Reason:   fmt.Sprintf("Internal security evaluation error: %v", err),
		}
	}

	respBytes, _ := json.Marshal(resp)
	_, _ = conn.Write(append(respBytes, '\n'))
}

// HandleHookRequest processes a domain.HookRequest and returns a domain.HookResponse.
func (s *Server) HandleHookRequest(ctx context.Context, req domain.HookRequest) (domain.HookResponse, error) {
	if s.manager == nil {
		return domain.HookResponse{
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
			return domain.HookResponse{
				Decision: string(domain.DecisionDeny),
				Reason:   fmt.Sprintf("Evaluation error: %v", err),
			}, nil
		}

		return domain.HookResponse{
			Decision:  string(decision.Decision),
			Reason:    decision.Reason,
			Overwrite: decision.Overwrite,
		}, nil

	case "post":
		// Handle PostToolUse output sanitization and return overwritten output
		if req.ToolCall.Name != "" && req.ToolCall.Args != nil {
			if out, ok := req.ToolCall.Args["output"].(string); ok && out != "" {
				sanitized, _ := s.manager.SanitizeToolOutput(ctx, req.ToolCall.Name, out)
				return domain.HookResponse{
					Overwrite: map[string]interface{}{
						"output": sanitized,
					},
				}, nil
			}
		}
		return domain.HookResponse{}, nil

	default:
		return domain.HookResponse{
			Decision: string(domain.DecisionAllow),
		}, nil
	}
}
