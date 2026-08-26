package security

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agyent/internal/adapters/security/network"
	"agyent/internal/adapters/security/pathjail"
	"agyent/internal/adapters/security/sanitizer"
	"agyent/internal/adapters/security/subagents"
	"agyent/internal/config"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// Default sensitive command patterns for balanced mode
var sensitiveCommandRegex = regexp.MustCompile(`(?i)\b(rm|del|erase|rmdir|rd|git\s+(push|reset|clean)|chmod|chown|sudo|icacls|takeown|curl|wget|nc|ncat|scp|ssh|docker\s+(run|exec|stop|rm)|npm\s+(publish|install\s+-g)|pip\s+install|cargo\s+install|go\s+install|python[0-9.]*\s+-[a-zA-Z]*c|node\s+-[a-zA-Z]*e|powershell|pwsh|cmd\.exe|taskkill|kill)\b`)

type sessionGrant struct {
	pattern   string
	expiresAt time.Time
}

// Manager coordinates all security guardrail evaluators and HITL approval states.
type Manager struct {
	mu                sync.RWMutex
	cfg               config.SecurityConfig
	pathjailEval      *pathjail.Evaluator
	networkEval       *network.Evaluator
	subagentEval      *subagents.Evaluator
	sanitizerEval     *sanitizer.Evaluator
	hitlPort          ports.HITLApprovalPort
	blacklistPatterns []*regexp.Regexp
	whitelistPatterns []*regexp.Regexp
	sessionGrants     map[string][]sessionGrant // sessionKey -> grants
	activeTurns       map[string]string         // convID -> sessionKey
	activeWorkspaces  map[string]string         // workspaceDir -> sessionKey
	logger            *slog.Logger

	// Metrics (Lock-free atomic counters)
	totalEvaluations atomic.Int64
	blockedToday     atomic.Int64
	approvedToday    atomic.Int64
}

// NewManager constructs a new Security Manager.
func NewManager(cfg config.SecurityConfig, hitlPort ports.HITLApprovalPort, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}

	m := &Manager{
		cfg:              cfg,
		hitlPort:         hitlPort,
		sessionGrants:    make(map[string][]sessionGrant),
		activeTurns:      make(map[string]string),
		activeWorkspaces: make(map[string]string),
		logger:           logger,
	}

	m.rebuildEvaluators()
	return m
}

func (m *Manager) rebuildEvaluators() {
	m.pathjailEval = pathjail.NewEvaluator(m.cfg.Filesystem, m.cfg.AgentConfigManagement.ManageableFiles)
	m.networkEval = network.NewEvaluator(m.cfg.Network)
	m.subagentEval = subagents.NewEvaluator(m.cfg.Subagents)
	m.sanitizerEval = sanitizer.NewEvaluator(m.cfg.DLP)

	m.blacklistPatterns = make([]*regexp.Regexp, 0, len(m.cfg.Commands.CustomBlacklist))
	for _, p := range m.cfg.Commands.CustomBlacklist {
		if re, err := regexp.Compile(p); err == nil {
			m.blacklistPatterns = append(m.blacklistPatterns, re)
		}
	}

	m.whitelistPatterns = make([]*regexp.Regexp, 0, len(m.cfg.Commands.CustomWhitelist))
	for _, p := range m.cfg.Commands.CustomWhitelist {
		if re, err := regexp.Compile(p); err == nil {
			m.whitelistPatterns = append(m.whitelistPatterns, re)
		}
	}
}

// EvaluateToolCall intercepts any tool call synchronously before substrate execution.
func (m *Manager) EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
	start := time.Now()

	m.totalEvaluations.Add(1)

	m.mu.RLock()
	preset := m.cfg.Preset
	m.mu.RUnlock()

	// If Preset is Unrestricted -> Allow 100% of tool calls immediately with full autonomy
	if preset == string(domain.PresetUnrestricted) {
		m.recordApproved()
		return domain.SecurityDecision{
			Decision:  domain.DecisionAllow,
			Reason:    "🛡️ [Security Preset: Unrestricted]: Tool call permitted with full autonomy",
			LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
		}, nil
	}

	var decision domain.SecurityDecision
	var err error

	switch req.ToolName {
	case "run_command":
		cmd, _ := req.Args["CommandLine"].(string)
		decision, err = m.EvaluateCommand(ctx, req.SessionKey, req.Role, cmd)

	case "view_file", "write_to_file", "replace_file_content":
		targetPath, _ := req.Args["TargetFile"].(string)
		if targetPath == "" {
			targetPath, _ = req.Args["AbsolutePath"].(string)
		}
		isWrite := req.ToolName == "write_to_file" || req.ToolName == "replace_file_content"
		m.mu.RLock()
		preset := m.cfg.Preset
		m.mu.RUnlock()
		if isWrite && preset == "read_only" {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: File modification is strictly forbidden under Read Only security preset",
			}
			break
		}
		decision, err = m.EvaluatePath(ctx, req.SessionKey, req.WorkspaceDir, targetPath, isWrite)

	case "read_url_content", "web_search":
		urlStr, _ := req.Args["Url"].(string)
		decision, err = m.EvaluateURL(ctx, urlStr)

	case "invoke_subagent", "define_subagent":
		m.mu.RLock()
		evaluator := m.subagentEval
		m.mu.RUnlock()
		decision, err = evaluator.EvaluateSubagent(req.IsSubagent, req.CascadeDepth, req.ToolName, req.Role, 0)

	default:
		decision = domain.SecurityDecision{
			Decision: domain.DecisionAllow,
			Reason:   "Standard substrate tool permitted",
		}
	}

	if err != nil {
		m.recordBlocked()
		return domain.SecurityDecision{
			Decision:  domain.DecisionDeny,
			Reason:    fmt.Sprintf("Internal security evaluation error: %v", err),
			LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
		}, nil
	}

	m.mu.RLock()
	timeoutSec := m.cfg.ApprovalTimeoutSeconds
	hitlPort := m.hitlPort
	m.mu.RUnlock()

	// If decision requires HITL and an approval port is available -> Request interactive confirmation
	if decision.Decision == domain.DecisionAsk && hitlPort != nil {
		if timeoutSec <= 0 {
			timeoutSec = 60
		}
		appReq := domain.ApprovalRequest{
			RequestID:    fmt.Sprintf("hitl-%d", time.Now().UnixNano()),
			SessionKey:   req.SessionKey,
			ToolName:     req.ToolName,
			DiffPreview:  decision.Reason,
			IsConfigEdit: strings.Contains(decision.Reason, "configuration file"),
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Duration(timeoutSec) * time.Second),
		}

		if cmd, ok := req.Args["CommandLine"].(string); ok {
			appReq.CommandLine = cmd
		}
		if target, ok := req.Args["TargetFile"].(string); ok {
			appReq.TargetFile = target
		}

		m.logger.Info("Requesting HITL approval", "tool", req.ToolName, "reason", decision.Reason)
		appDecision, appErr := hitlPort.RequestApproval(ctx, appReq)
		if appErr != nil || !appDecision.Approved {
			m.recordBlocked()
			return domain.SecurityDecision{
				Decision:  domain.DecisionDeny,
				Reason:    fmt.Sprintf("🛡️ [Security Gate]: Action rejected by user or approval timed out (%s)", appDecision.Action),
				LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
			}, nil
		}

		// If user selected "Allow for Session" -> Grant temporary permission
		if appDecision.Action == "allow_session" && appReq.CommandLine != "" {
			m.GrantSessionPermission(req.SessionKey, appReq.CommandLine)
		}

		m.recordApproved()
		return domain.SecurityDecision{
			Decision:  domain.DecisionAllow,
			Reason:    "Approved by administrator",
			LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
		}, nil
	}

	if decision.Decision == domain.DecisionDeny {
		m.recordBlocked()
	} else if decision.Decision == domain.DecisionAllow {
		m.recordApproved()
	}

	decision.LatencyMs = float64(time.Since(start).Microseconds()) / 1000.0
	return decision, nil
}

// EvaluateCommand validates shell commands against active rules and session grants.
func (m *Manager) EvaluateCommand(ctx context.Context, sessionKey string, role string, cmd string) (domain.SecurityDecision, error) {
	if cmd == "" {
		return domain.SecurityDecision{Decision: domain.DecisionDeny, Reason: "Empty command"}, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// 0. Check Unrestricted Preset (Full Autonomy)
	if m.cfg.Preset == string(domain.PresetUnrestricted) {
		return domain.SecurityDecision{
			Decision: domain.DecisionAllow,
			Reason:   "🛡️ [Security Preset: Unrestricted]: Command permitted with full autonomy",
		}, nil
	}

	// 1. Check Read-Only Preset (Highest Precedence)
	if m.cfg.Preset == string(domain.PresetReadOnly) {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   "🛡️ [Security Preset: Read Only]: Shell command execution is completely disabled",
		}, nil
	}

	// 2. Check Blacklist Patterns (Precedes Session Grants)
	for _, bl := range m.blacklistPatterns {
		if bl.MatchString(cmd) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Command Guardrail]: Command matches forbidden pattern '%s'", bl.String()),
			}, nil
		}
	}

	// 3. Check in-memory session grants (Exact / Token Prefix match only)
	if grants, ok := m.sessionGrants[sessionKey]; ok {
		now := time.Now()
		for _, g := range grants {
			if g.expiresAt.After(now) {
				if g.pattern == "*" || cmd == g.pattern || strings.HasPrefix(cmd, g.pattern+" ") {
					return domain.SecurityDecision{
						Decision: domain.DecisionAllow,
						Reason:   "Allowed by active session permission grant",
					}, nil
				}
			}
		}
	}

	// 4. Check Whitelist Patterns
	for _, wl := range m.whitelistPatterns {
		if wl.MatchString(cmd) || strings.HasPrefix(cmd, wl.String()) {
			return domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Command matched custom whitelist rule",
			}, nil
		}
	}

	// 5. Preset Strict mode -> auto block if not in whitelist
	if m.cfg.Preset == string(domain.PresetStrict) {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Security Preset: Strict]: Command '%s' is not explicitly whitelisted", cmd),
		}, nil
	}

	// 6. Preset Balanced mode -> Ask HITL for sensitive actions
	if m.cfg.Preset == string(domain.PresetBalanced) && sensitiveCommandRegex.MatchString(cmd) {
		return domain.SecurityDecision{
			Decision: domain.DecisionAsk,
			Reason:   fmt.Sprintf("Sensitive shell execution: `%s`", cmd),
		}, nil
	}

	return domain.SecurityDecision{
		Decision: domain.DecisionAllow,
		Reason:   "Command permitted under active security profile",
	}, nil
}

// EvaluatePath validates file access within workspace boundaries.
func (m *Manager) EvaluatePath(ctx context.Context, sessionKey string, workspaceDir string, targetPath string, isWrite bool) (domain.SecurityDecision, error) {
	m.mu.RLock()
	evaluator := m.pathjailEval
	allowDelegated := m.cfg.AgentConfigManagement.Enabled
	m.mu.RUnlock()

	if workspaceDir == "" {
		workspaceDir = "."
	}

	return evaluator.EvaluatePath(workspaceDir, targetPath, isWrite, allowDelegated)
}

// EvaluateURL validates outbound network URLs.
func (m *Manager) EvaluateURL(ctx context.Context, urlStr string) (domain.SecurityDecision, error) {
	m.mu.RLock()
	evaluator := m.networkEval
	m.mu.RUnlock()

	return evaluator.EvaluateURL(urlStr)
}

// SanitizeToolOutput masks secrets within tool output.
func (m *Manager) SanitizeToolOutput(ctx context.Context, toolName string, output string) (string, error) {
	m.mu.RLock()
	evaluator := m.sanitizerEval
	m.mu.RUnlock()

	return evaluator.RedactSecrets(output), nil
}

// GrantSessionPermission adds a temporary permission grant.
func (m *Manager) GrantSessionPermission(sessionKey string, pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessionGrants[sessionKey] = append(m.sessionGrants[sessionKey], sessionGrant{
		pattern:   pattern,
		expiresAt: time.Now().Add(15 * time.Minute),
	})
	m.logger.Info("Granted temporary session permission", "session", sessionKey, "pattern", pattern)
}

// SetPreset dynamically updates the active preset.
func (m *Manager) SetPreset(preset domain.SecurityPreset) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = config.GetEffectiveSecurityPreset(string(preset))
	m.rebuildEvaluators()
	m.logger.Info("Switched security preset dynamically", "preset", preset)
}

// SetRedactionMode dynamically updates the redaction mode.
func (m *Manager) SetRedactionMode(mode domain.RedactionMode) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg.DLP.RedactionMode = string(mode)
	if m.sanitizerEval != nil {
		m.sanitizerEval.SetRedactionMode(mode)
	}
	m.logger.Info("Switched DLP redaction mode", "mode", mode)
}

// AddWhitelistEntry appends a new command to the whitelist.
func (m *Manager) AddWhitelistEntry(entry string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg.Commands.CustomWhitelist = append(m.cfg.Commands.CustomWhitelist, entry)
	if re, err := regexp.Compile(entry); err == nil {
		m.whitelistPatterns = append(m.whitelistPatterns, re)
	}
	m.logger.Info("Added custom whitelist entry", "entry", entry)
}

// GetDashboardSummary returns statistics for the /security dashboard.
func (m *Manager) GetDashboardSummary(sessionKey string) domain.SecurityDashboard {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return domain.SecurityDashboard{
		Preset:           domain.SecurityPreset(m.cfg.Preset),
		ActiveJail:       "Workspace Jailed",
		AllowedCommands:  m.cfg.Commands.CustomWhitelist,
		AllowedPaths:     m.cfg.Filesystem.AllowedPaths,
		TotalEvaluations: m.totalEvaluations.Load(),
		BlockedToday:     m.blockedToday.Load(),
		ApprovedToday:    m.approvedToday.Load(),
		RedactionMode:    domain.RedactionMode(m.cfg.DLP.RedactionMode),
		ConfigDelegated:  m.cfg.AgentConfigManagement.Enabled,
	}
}

func (m *Manager) recordBlocked() {
	m.blockedToday.Add(1)
}

func (m *Manager) recordApproved() {
	m.approvedToday.Add(1)
}

// EnsureWorkspaceHooks guarantees that .agents/hooks.json is provisioned in the given workspace.
func (m *Manager) EnsureWorkspaceHooks(workspaceDir string) error {
	_, err := EnsureWorkspaceHooksProvisioned(workspaceDir, "", m.logger)
	return err
}

// RegisterActiveTurn registers the active sessionKey associated with a running turn.
func (m *Manager) RegisterActiveTurn(convID string, sessionKey string, workspaceDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if convID != "" {
		m.activeTurns[convID] = sessionKey
	}
	if workspaceDir != "" {
		m.activeWorkspaces[filepath.Clean(workspaceDir)] = sessionKey
	}
}

// UnregisterActiveTurn removes the active turn association when execution concludes.
func (m *Manager) UnregisterActiveTurn(convID string, workspaceDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if convID != "" {
		delete(m.activeTurns, convID)
	}
	if workspaceDir != "" {
		delete(m.activeWorkspaces, filepath.Clean(workspaceDir))
	}
}

// ResolveSessionKey retrieves the active sessionKey for a given conversationID or workspace.
func (m *Manager) ResolveSessionKey(convID string, workspaceDir string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if convID != "" {
		if s, ok := m.activeTurns[convID]; ok && s != "" {
			return s
		}
	}
	if workspaceDir != "" {
		if s, ok := m.activeWorkspaces[filepath.Clean(workspaceDir)]; ok && s != "" {
			return s
		}
	}
	return ""
}
