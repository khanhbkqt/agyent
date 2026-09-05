package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.ExecutionServicePort = (*Service)(nil)

// ConfigProvider provides optional administrative privilege resolution to ExecutionService.
type ConfigProvider interface {
	IsAdminForProvider(senderID, provider string) bool
	IsSuperAdmin(principal domain.Principal) bool
}

// Service is the unified, authorized execution chokepoint for all AGY CLI subprocesses.
type Service struct {
	runner          ports.RunnerPort
	policy          ports.PolicyEngine
	securityManager ports.SecurityManagerPort
	storage         ports.StoragePort
	configProvider  ConfigProvider
	secretToken     string
	logger          *slog.Logger
}

// NewService constructs a new ExecutionService instance.
func NewService(
	runner ports.RunnerPort,
	policy ports.PolicyEngine,
	securityManager ports.SecurityManagerPort,
	storage ports.StoragePort,
	cfgProvider ConfigProvider,
	secretToken string,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		runner:          runner,
		policy:          policy,
		securityManager: securityManager,
		storage:         storage,
		configProvider:  cfgProvider,
		secretToken:     secretToken,
		logger:          logger,
	}
}

// ExecuteTurn authorizes, generates TurnID, binds TurnSecurityContext, isolates MCP, and executes AGY turn.
func (s *Service) ExecuteTurn(
	ctx context.Context,
	principal domain.Principal,
	req domain.ExecutionRequest,
	sessionKey string,
	isStreaming bool,
) (*domain.ExecutionResult, error) {
	if s.runner == nil {
		return nil, errors.New("execution service: runner port is not initialized")
	}

	// 1. Resolve Action and Target Resource
	action := domain.ActionTurnExecute
	if req.Mode == "plan" {
		if strings.HasPrefix(req.AgentName, "compactor") || strings.Contains(req.Prompt, "CONVERSATION CONTINUITY & CONTEXT SNAPSHOT") || strings.Contains(req.Prompt, "Continuity Digest") {
			action = domain.ActionSessionCompact
		}
	}

	resource := domain.Resource{
		Kind:      domain.ResourceKindAgent,
		ID:        req.AgentName,
		AgentName: req.AgentName,
	}
	if resource.AgentName == "" {
		resource.AgentName = "agyent"
	}
	if s.storage != nil {
		if agent, err := s.storage.GetAgent(ctx, resource.AgentName); err == nil && agent != nil {
			resource.OwnerID = agent.OwnerID
			resource.IsPublic = agent.IsPublic
		}
	}

	// 2. Authorize Principal against Policy (Default-Deny)
	if s.policy == nil {
		return nil, errors.New("policy engine not configured: fail-closed")
	}
	if err := s.policy.Authorize(ctx, principal, action, resource); err != nil {
		s.logger.WarnContext(ctx, "Execution turn denied by policy",
			"principal", principal.SubjectID,
			"action", action,
			"resource", resource.AgentName,
			"error", err,
		)
		if s.storage != nil {
			_ = s.storage.LogAudit(ctx, &domain.AuditLog{
				SessionKey:     sessionKey,
				AgentName:      req.AgentName,
				ConversationID: req.ConversationID,
				PromptLength:   len(req.Prompt),
				Status:         "ERROR",
				ErrorMessage:   fmt.Sprintf("authorization denied: %v", err),
				CreatedAt:      time.Now(),
			})
		}
		return nil, fmt.Errorf("authorization denied: %w", err)
	}

	// 3. Enforce Safety Overrides on Privileged Flags and Viewer Role
	if role, err := s.policy.ResolveAgentRole(ctx, principal, resource.AgentName); err == nil {
		if role == domain.AgentRoleViewer {
			req.Mode = "plan"
		}
	}

	isSuperAdmin := false
	if s.configProvider != nil {
		if s.configProvider.IsSuperAdmin(principal) || s.configProvider.IsAdminForProvider(principal.SubjectID, principal.Provider) {
			isSuperAdmin = true
		}
	}
	if req.DangerouslySkipPermissions && !isSuperAdmin {
		s.logger.WarnContext(ctx, "Stripping dangerously_skip_permissions flag for non-admin principal",
			"principal", principal.SubjectID,
		)
		req.DangerouslySkipPermissions = false
	}
	// For background safety tasks (compact / reflect / subagent / cron) or plan mode, always disable dangerously_skip_permissions
	if action == domain.ActionSessionCompact || req.Mode == "plan" || strings.HasPrefix(principal.SubjectID, "system:") {
		req.DangerouslySkipPermissions = false
	}

	// 4. Provision End-to-End Turn Identity
	turnID := req.TurnID
	if turnID == "" {
		turnID = "turn-" + generateUUIDHex()
		req.TurnID = turnID
	}

	// 5. Setup Environment & Isolated Turn Directory
	if req.Env == nil {
		req.Env = make(map[string]string)
	}
	req.Env["AGYENT_TURN_ID"] = turnID
	if s.secretToken != "" {
		req.Env["AGYENT_IPC_TOKEN"] = s.secretToken
	}

	// Per-process isolated MCP directory
	mcpDir := filepath.Join(os.TempDir(), "agyent-mcp", turnID)
	if err := os.MkdirAll(mcpDir, 0700); err == nil {
		defer func() {
			_ = os.RemoveAll(mcpDir)
		}()
		req.Env["AGYENT_MCP_DIR"] = mcpDir
	}

	// 6. Register Turn in Security Manager
	if s.securityManager != nil {
		s.securityManager.RegisterActiveTurn(domain.TurnSecurityContext{
			TurnID:         turnID,
			ConversationID: req.ConversationID,
			SessionKey:     sessionKey,
			Principal:      principal,
			Action:         action,
			Resource:       resource,
			WorkspaceDir:   req.WorkspaceDir,
			AgentName:      req.AgentName,
			CreatedAt:      time.Now(),
		})
		defer s.securityManager.UnregisterTurnByID(turnID)
	}

	// 7. Execute via Runner with Effort Fallback Handling
	start := time.Now()
	var (
		res     *domain.ExecutionResult
		execErr error
	)

	if isStreaming {
		res, execErr = s.runner.ExecuteStream(ctx, req, sessionKey)
		if domain.IsEffortError(execErr, res) && req.Effort != "" {
			s.logger.WarnContext(ctx, "Effort flag rejected by model/CLI in stream mode, retrying without --effort",
				"turn_id", turnID,
				"model", req.Model,
				"effort", req.Effort,
			)
			req.Effort = domain.EffortNone
			req.DisableEffort = true
			res, execErr = s.runner.ExecuteStream(ctx, req, sessionKey)
		}
	} else {
		res, execErr = s.runner.Execute(ctx, req)
		if domain.IsEffortError(execErr, res) && req.Effort != "" {
			s.logger.WarnContext(ctx, "Effort flag rejected by model/CLI in batch mode, retrying without --effort",
				"turn_id", turnID,
				"model", req.Model,
				"effort", req.Effort,
			)
			req.Effort = domain.EffortNone
			req.DisableEffort = true
			res, execErr = s.runner.Execute(ctx, req)
		}
	}

	duration := time.Since(start).Seconds()
	if res != nil && res.DurationSec == 0 {
		res.DurationSec = duration
	}

	// 8. Audit Logging & Telemetry
	if s.storage != nil {
		errMsg := ""
		if execErr != nil {
			errMsg = execErr.Error()
		} else if res != nil && res.Error != "" {
			errMsg = res.Error
		}
		status := "SUCCESS"
		if execErr != nil || (res != nil && !res.Success) {
			status = "ERROR"
		}
		var usage domain.TokenUsage
		respLen := 0
		if res != nil {
			usage = res.Usage
			respLen = len(res.ResponseText)
		}

		_ = s.storage.LogAudit(ctx, &domain.AuditLog{
			SessionKey:      sessionKey,
			AgentName:       req.AgentName,
			ConversationID:  req.ConversationID,
			Model:           req.Model,
			Effort:          req.Effort,
			PromptLength:    len(req.Prompt),
			ResponseLength:  respLen,
			DurationSeconds: duration,
			Usage:           usage,
			Status:          status,
			ErrorMessage:    errMsg,
			CreatedAt:       time.Now(),
		})
	}

	return res, execErr
}

// InterruptTurn signals an active turn identified by sessionKey to interrupt gracefully.
func (s *Service) InterruptTurn(ctx context.Context, sessionKey string) error {
	if s.runner == nil {
		return errors.New("execution service: runner port is not initialized")
	}
	return s.runner.InterruptStream(ctx, sessionKey)
}

func generateUUIDHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
