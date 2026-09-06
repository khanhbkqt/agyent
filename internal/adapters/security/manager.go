package security

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"runtime"
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

// Self-escalation and gateway tampering command patterns (forbidden across all managed presets)
var selfEscalationRegex = regexp.MustCompile(`(?i)(agyent(\.exe)?\s+(agent|agents|a|security|sec|guardrail|init|config)\b|\.agyent[/\\](agyent\.db|config\.yaml)|\bagyent\.db\b|\b(pkill|killall|taskkill)\s+.*agyent\b)`)

func isSelfEscalationCommand(cmd string) bool {
	return selfEscalationRegex.MatchString(cmd)
}

type sessionGrant struct {
	pattern   string
	grantedAt time.Time
}

// ExtractBaseCommand extracts the primary binary/executable name from a command line string.
func ExtractBaseCommand(rawCmd string) string {
	return domain.ExtractBaseCommand(rawCmd)
}

func isBaseCommandMatch(cmd string, pattern string) bool {
	if pattern == "" || pattern == "*" {
		return false
	}
	cmdBase := domain.ExtractBaseCommand(cmd)
	if cmdBase != "" && strings.EqualFold(cmdBase, pattern) {
		return true
	}
	return false
}

type presetEvaluators struct {
	cfg               config.SecurityConfig
	pathjailEval      *pathjail.Evaluator
	networkEval       *network.Evaluator
	subagentEval      *subagents.Evaluator
	sanitizerEval     *sanitizer.Evaluator
	blacklistPatterns []*regexp.Regexp
	whitelistPatterns []*regexp.Regexp
}

func buildEvaluatorBundle(cfg config.SecurityConfig) *presetEvaluators {
	pe := &presetEvaluators{
		cfg:           cfg,
		pathjailEval:  pathjail.NewEvaluator(cfg.Filesystem, cfg.AgentConfigManagement.ManageableFiles),
		networkEval:   network.NewEvaluator(cfg.Network),
		subagentEval:  subagents.NewEvaluator(cfg.Subagents),
		sanitizerEval: sanitizer.NewEvaluator(cfg.DLP),
	}

	pe.blacklistPatterns = make([]*regexp.Regexp, 0, len(cfg.Commands.CustomBlacklist))
	for _, p := range cfg.Commands.CustomBlacklist {
		if re, err := regexp.Compile(p); err == nil {
			pe.blacklistPatterns = append(pe.blacklistPatterns, re)
		}
	}

	pe.whitelistPatterns = make([]*regexp.Regexp, 0, len(cfg.Commands.CustomWhitelist))
	for _, p := range cfg.Commands.CustomWhitelist {
		if re, err := regexp.Compile(p); err == nil {
			pe.whitelistPatterns = append(pe.whitelistPatterns, re)
		}
	}

	return pe
}

// Manager coordinates all security guardrail evaluators and HITL approval states.
type Manager struct {
	mu               sync.RWMutex
	defaultCfg       config.SecurityConfig
	defaultPreset    domain.SecurityPreset
	evaluators       map[domain.SecurityPreset]*presetEvaluators
	hitlPort         ports.HITLApprovalPort
	sessionGrants    map[string][]sessionGrant             // sessionKey -> grants
	activeTurns      map[string]domain.TurnSecurityContext // convID -> TurnSecurityContext
	activeWorkspaces map[string]domain.TurnSecurityContext // workspaceDir -> TurnSecurityContext
	eventBus         ports.EventBusPort
	logger           *slog.Logger

	// Metrics (Lock-free atomic counters)
	totalEvaluations atomic.Int64
	blockedToday     atomic.Int64
	approvedToday    atomic.Int64
}

// SetEventBus sets the EventBusPort on the security manager.
func (m *Manager) SetEventBus(bus ports.EventBusPort) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventBus = bus
}

// NewManager constructs a new Security Manager with isolated evaluator pools per preset.
func NewManager(cfg config.SecurityConfig, hitlPort ports.HITLApprovalPort, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}

	initialPreset := domain.SecurityPreset(cfg.Preset)
	if initialPreset == "" {
		initialPreset = domain.PresetBalanced
	}

	m := &Manager{
		defaultCfg:       cfg,
		defaultPreset:    initialPreset,
		evaluators:       make(map[domain.SecurityPreset]*presetEvaluators),
		hitlPort:         hitlPort,
		sessionGrants:    make(map[string][]sessionGrant),
		activeTurns:      make(map[string]domain.TurnSecurityContext),
		activeWorkspaces: make(map[string]domain.TurnSecurityContext),
		logger:           logger,
	}

	// Pre-build evaluator bundles for all standard presets
	for _, p := range domain.AllSecurityPresets {
		presetCfg := config.GetEffectiveSecurityPreset(string(p))
		if len(cfg.Commands.CustomBlacklist) > 0 {
			presetCfg.Commands.CustomBlacklist = append(presetCfg.Commands.CustomBlacklist, cfg.Commands.CustomBlacklist...)
		}
		if len(cfg.Commands.CustomWhitelist) > 0 {
			presetCfg.Commands.CustomWhitelist = append(presetCfg.Commands.CustomWhitelist, cfg.Commands.CustomWhitelist...)
		}
		if len(cfg.Filesystem.AllowedPaths) > 0 {
			presetCfg.Filesystem.AllowedPaths = append(presetCfg.Filesystem.AllowedPaths, cfg.Filesystem.AllowedPaths...)
		}
		if len(cfg.Filesystem.ForbiddenPaths) > 0 {
			presetCfg.Filesystem.ForbiddenPaths = append(presetCfg.Filesystem.ForbiddenPaths, cfg.Filesystem.ForbiddenPaths...)
		}
		m.evaluators[p] = buildEvaluatorBundle(presetCfg)
	}

	return m
}

func (m *Manager) getEvaluatorBundle(preset domain.SecurityPreset) *presetEvaluators {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if eb, ok := m.evaluators[preset]; ok {
		return eb
	}
	if eb, ok := m.evaluators[m.defaultPreset]; ok {
		return eb
	}
	if eb, ok := m.evaluators[domain.PresetBalanced]; ok {
		return eb
	}
	return buildEvaluatorBundle(m.defaultCfg)
}

// EvaluateToolCall intercepts any tool call synchronously before substrate execution.
func (m *Manager) EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
	start := time.Now()

	m.totalEvaluations.Add(1)

	// Resolve the active turn's security context (preset and session)
	turnCtx, hasTurn := m.ResolveTurnContext(req.ConversationID, req.WorkspaceDir)

	m.mu.RLock()
	preset := m.defaultPreset
	m.mu.RUnlock()

	if hasTurn && turnCtx.Preset != "" {
		preset = turnCtx.Preset
	}

	sessionKey := req.SessionKey
	if sessionKey == "" && hasTurn {
		sessionKey = turnCtx.SessionKey
	}

	// 0. If Preset is Unrestricted -> Allow 100% of tool calls immediately with full autonomy
	if preset == domain.PresetUnrestricted {
		m.recordApproved()
		return domain.SecurityDecision{
			Decision:  domain.DecisionAllow,
			Reason:    "🛡️ [Security Preset: Unrestricted]: Tool call permitted with full autonomy",
			LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
		}, nil
	}

	bundle := m.getEvaluatorBundle(preset)

	var decision domain.SecurityDecision
	var err error

	switch req.ToolName {
	case "run_command":
		cmd, _ := req.Args["CommandLine"].(string)
		decision, err = m.evaluateCommandWithBundle(ctx, sessionKey, req.Role, cmd, bundle)

	case "view_file", "write_to_file", "replace_file_content", "list_dir", "grep_search", "find_by_name":
		targetPath, _ := req.Args["TargetFile"].(string)
		if targetPath == "" {
			targetPath, _ = req.Args["AbsolutePath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["DirectoryPath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["SearchPath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["SearchDirectory"].(string)
		}
		isWrite := req.ToolName == "write_to_file" || req.ToolName == "replace_file_content"
		if isWrite && preset == domain.PresetReadOnly {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: File modification is strictly forbidden under Read Only security preset",
			}
			break
		}
		if isWrite && preset != domain.PresetUnrestricted {
			content, _ := req.Args["CodeContent"].(string)
			if content == "" {
				content, _ = req.Args["ReplacementContent"].(string)
			}
			if isSelfEscalationCommand(content) {
				decision = domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   "🛡️ [Security Gate - Staged Privilege Escalation Blocked]: Attempted to write code containing forbidden references to agyent gateway database, configuration, or administrative commands",
				}
				break
			}
		}
		ws := req.WorkspaceDir
		if ws == "" && hasTurn {
			ws = turnCtx.WorkspaceDir
		}
		if ws == "" {
			ws = "."
		}
		decision, err = bundle.pathjailEval.EvaluatePath(ws, targetPath, isWrite, bundle.cfg.AgentConfigManagement.Enabled)

	case "read_url_content":
		urlStr, _ := req.Args["Url"].(string)
		decision, err = bundle.networkEval.EvaluateURL(urlStr)

	case "web_search":
		if domainStr, ok := req.Args["domain"].(string); ok && domainStr != "" {
			decision, err = bundle.networkEval.EvaluateURL("https://" + domainStr)
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Web search query permitted",
			}
		}

	case "invoke_subagent", "define_subagent":
		decision, err = bundle.subagentEval.EvaluateSubagent(req.IsSubagent, req.CascadeDepth, req.ToolName, req.Role, 0)

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
	timeoutSec := bundle.cfg.ApprovalTimeoutSeconds
	hitlPort := m.hitlPort
	m.mu.RUnlock()

	// If decision requires HITL and an approval port is available -> Request interactive confirmation
	if decision.Decision == domain.DecisionAsk && hitlPort != nil {
		if timeoutSec <= 0 {
			timeoutSec = 60
		}
		agentName := "agent"
		if hasTurn && turnCtx.AgentName != "" {
			agentName = turnCtx.AgentName
		}
		appReq := domain.ApprovalRequest{
			RequestID:    fmt.Sprintf("hitl-%d", time.Now().UnixNano()),
			SessionKey:   sessionKey,
			AgentName:    agentName,
			ToolName:     req.ToolName,
			Reason:       decision.Reason,
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
		if repl, ok := req.Args["ReplacementContent"].(string); ok && repl != "" {
			if targetContent, ok := req.Args["TargetContent"].(string); ok && targetContent != "" {
				appReq.DiffPreview = fmt.Sprintf("--- Target Content ---\n%s\n+++ Replacement Content +++\n%s", targetContent, repl)
			} else {
				appReq.DiffPreview = repl
			}
		} else if code, ok := req.Args["CodeContent"].(string); ok && code != "" {
			appReq.DiffPreview = code
		}

		m.logger.Info("Requesting HITL approval", "tool", req.ToolName, "reason", decision.Reason, "agent", agentName)
		appDecision, appErr := hitlPort.RequestApproval(ctx, appReq)
		if appErr != nil || !appDecision.Approved {
			m.recordBlocked()

			if appDecision.Action == "force_kill" {
				m.mu.RLock()
				eb := m.eventBus
				m.mu.RUnlock()
				if eb != nil {
					_ = eb.SyncEmit(ctx, domain.NewEvent(domain.EventForceKillRequested, domain.ForceKillPayload{
						SessionKey:     sessionKey,
						ConversationID: req.ConversationID,
						Reason:         "User clicked Force Kill Agent on security approval card",
						Timestamp:      time.Now(),
					}))
				}
			}

			return domain.SecurityDecision{
				Decision:  domain.DecisionDeny,
				Reason:    fmt.Sprintf("🛡️ [Security Gate]: Action rejected by user or approval timed out (%s)", appDecision.Action),
				LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
			}, nil
		}

		// Multi-tier Session Grant Handling:
		if appDecision.Action == domain.ActionAllowAllSession {
			m.GrantSessionPermission(sessionKey, "*")
		} else if appDecision.Action == domain.ActionAllowSession && appReq.CommandLine != "" {
			baseCmd := ExtractBaseCommand(appReq.CommandLine)
			if baseCmd != "" {
				m.GrantSessionPermission(sessionKey, baseCmd)
			} else {
				m.GrantSessionPermission(sessionKey, appReq.CommandLine)
			}
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

func (m *Manager) evaluateCommandWithBundle(ctx context.Context, sessionKey string, role string, cmd string, bundle *presetEvaluators) (domain.SecurityDecision, error) {
	if cmd == "" {
		return domain.SecurityDecision{Decision: domain.DecisionDeny, Reason: "Empty command"}, nil
	}

	preset := domain.SecurityPreset(bundle.cfg.Preset)

	// 0. Check Unrestricted Preset
	if preset == domain.PresetUnrestricted {
		return domain.SecurityDecision{
			Decision: domain.DecisionAllow,
			Reason:   "🛡️ [Security Preset: Unrestricted]: Command permitted with full autonomy",
		}, nil
	}

	// 1. Check Read-Only Preset
	if preset == domain.PresetReadOnly {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   "🛡️ [Security Preset: Read Only]: Shell command execution is completely disabled",
		}, nil
	}

	// 1.5. Check Anti-Self-Escalation & Gateway Tampering (Forbidden across all managed presets - Inviolable Hard Guardrail)
	if isSelfEscalationCommand(cmd) {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Security Gate - Privilege Escalation Blocked]: Execution of administrative command '%s' to alter agyent configuration or security presets is strictly forbidden from an AI agent session", cmd),
		}, nil
	}

	// 2. Check Blacklist Patterns (Inviolable Hard Guardrail)
	for _, bl := range bundle.blacklistPatterns {
		if bl.MatchString(cmd) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Command Guardrail]: Command matches forbidden pattern '%s'", bl.String()),
			}, nil
		}
	}

	// 3. Check in-memory session grants (Valid for the entire duration of the active session)
	m.mu.RLock()
	grants, hasGrants := m.sessionGrants[sessionKey]
	m.mu.RUnlock()
	if hasGrants {
		for _, g := range grants {
			if g.pattern == "*" || cmd == g.pattern || strings.HasPrefix(cmd, g.pattern+" ") || isBaseCommandMatch(cmd, g.pattern) {
				return domain.SecurityDecision{
					Decision: domain.DecisionAllow,
					Reason:   fmt.Sprintf("Allowed by active session permission grant (%s)", g.pattern),
				}, nil
			}
		}
	}

	// 4. Check Whitelist Patterns
	m.mu.RLock()
	wlPatterns := make([]*regexp.Regexp, len(bundle.whitelistPatterns))
	copy(wlPatterns, bundle.whitelistPatterns)
	m.mu.RUnlock()

	for _, wl := range wlPatterns {
		if wl.MatchString(cmd) || strings.HasPrefix(cmd, wl.String()) {
			return domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Command matched custom whitelist rule",
			}, nil
		}
	}

	// 5. Preset Strict mode -> auto block if not in whitelist
	if preset == domain.PresetStrict {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Security Preset: Strict]: Command '%s' is not explicitly whitelisted", cmd),
		}, nil
	}

	// 6. Preset Balanced mode -> Ask HITL for sensitive actions
	if preset == domain.PresetBalanced && sensitiveCommandRegex.MatchString(cmd) {
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

// EvaluateCommand validates shell commands against active rules and session grants.
func (m *Manager) EvaluateCommand(ctx context.Context, sessionKey string, role string, cmd string) (domain.SecurityDecision, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	return m.evaluateCommandWithBundle(ctx, sessionKey, role, cmd, bundle)
}

// EvaluatePath validates file access within workspace boundaries.
func (m *Manager) EvaluatePath(ctx context.Context, sessionKey string, workspaceDir string, targetPath string, isWrite bool) (domain.SecurityDecision, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	if workspaceDir == "" {
		workspaceDir = "."
	}
	return bundle.pathjailEval.EvaluatePath(workspaceDir, targetPath, isWrite, bundle.cfg.AgentConfigManagement.Enabled)
}

// EvaluateURL validates outbound network URLs.
func (m *Manager) EvaluateURL(ctx context.Context, urlStr string) (domain.SecurityDecision, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	return bundle.networkEval.EvaluateURL(urlStr)
}

// SanitizeToolOutput masks secrets within tool output.
func (m *Manager) SanitizeToolOutput(ctx context.Context, toolName string, output string) (string, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	return bundle.sanitizerEval.RedactSecrets(output), nil
}

// GrantSessionPermission adds a permission grant to the session cache for the active session.
func (m *Manager) GrantSessionPermission(sessionKey string, pattern string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessionGrants[sessionKey] = append(m.sessionGrants[sessionKey], sessionGrant{
		pattern:   pattern,
		grantedAt: time.Now(),
	})
	m.logger.Info("Granted session permission", "session", sessionKey, "pattern", pattern)
}

// ClearSessionGrants removes all active session grants for the given sessionKey upon session invalidation/reset.
func (m *Manager) ClearSessionGrants(sessionKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessionGrants, sessionKey)
	m.logger.Info("Cleared all session grants", "session", sessionKey)
}

// ClearAllSessionGrants flushes all cached session grants.
func (m *Manager) ClearAllSessionGrants() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessionGrants = make(map[string][]sessionGrant)
	m.logger.Info("Cleared all cached session grants across all sessions")
}

// SetPreset dynamically updates the default fallback preset.
func (m *Manager) SetPreset(preset domain.SecurityPreset) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.defaultPreset = preset
	m.defaultCfg.Preset = string(preset)
	m.logger.Info("Updated default security preset fallback", "preset", preset)
}

// SetRedactionMode dynamically updates the redaction mode across all evaluators.
func (m *Manager) SetRedactionMode(mode domain.RedactionMode) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, bundle := range m.evaluators {
		bundle.cfg.DLP.RedactionMode = string(mode)
		if bundle.sanitizerEval != nil {
			bundle.sanitizerEval.SetRedactionMode(mode)
		}
	}
	m.logger.Info("Switched DLP redaction mode", "mode", mode)
}

// AddWhitelistEntry appends a new command to the whitelist across all evaluators.
func (m *Manager) AddWhitelistEntry(entry string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	re, err := regexp.Compile(entry)
	for _, bundle := range m.evaluators {
		bundle.cfg.Commands.CustomWhitelist = append(bundle.cfg.Commands.CustomWhitelist, entry)
		if err == nil {
			newPatterns := make([]*regexp.Regexp, len(bundle.whitelistPatterns), len(bundle.whitelistPatterns)+1)
			copy(newPatterns, bundle.whitelistPatterns)
			bundle.whitelistPatterns = append(newPatterns, re)
		}
	}
	m.logger.Info("Added custom whitelist entry", "entry", entry)
}

// GetDashboardSummary returns statistics for the /security dashboard.
func (m *Manager) GetDashboardSummary(sessionKey string) domain.SecurityDashboard {
	m.mu.RLock()
	defer m.mu.RUnlock()

	activePreset := m.defaultPreset
	for _, t := range m.activeTurns {
		if t.SessionKey == sessionKey && t.Preset != "" {
			activePreset = t.Preset
			break
		}
	}

	bundle := m.evaluators[activePreset]
	if bundle == nil {
		bundle = m.evaluators[domain.PresetBalanced]
	}

	var allowedCmds []string
	var allowedPaths []string
	var redMode domain.RedactionMode
	var configDelegated bool
	if bundle != nil {
		allowedCmds = bundle.cfg.Commands.CustomWhitelist
		allowedPaths = bundle.cfg.Filesystem.AllowedPaths
		redMode = domain.RedactionMode(bundle.cfg.DLP.RedactionMode)
		configDelegated = bundle.cfg.AgentConfigManagement.Enabled
	}

	return domain.SecurityDashboard{
		Preset:           activePreset,
		ActiveJail:       "Workspace Jailed",
		AllowedCommands:  allowedCmds,
		AllowedPaths:     allowedPaths,
		TotalEvaluations: m.totalEvaluations.Load(),
		BlockedToday:     m.blockedToday.Load(),
		ApprovedToday:    m.approvedToday.Load(),
		RedactionMode:    redMode,
		ConfigDelegated:  configDelegated,
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

func canonicalizeWorkspacePath(p string) string {
	if p == "" {
		return ""
	}
	cleaned := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		cleaned = strings.ToLower(cleaned)
		cleaned = strings.ReplaceAll(cleaned, "/", "\\")
	}
	return cleaned
}

// RegisterActiveTurn registers the active sessionKey, preset, and workspace associated with a running turn.
func (m *Manager) RegisterActiveTurn(turn domain.TurnSecurityContext) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now()
	}
	if turn.TurnID != "" {
		m.activeTurns[turn.TurnID] = turn
	}
	if turn.ConversationID != "" {
		m.activeTurns[turn.ConversationID] = turn
	}
	if turn.WorkspaceDir != "" {
		m.activeWorkspaces[canonicalizeWorkspacePath(turn.WorkspaceDir)] = turn
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
		delete(m.activeWorkspaces, canonicalizeWorkspacePath(workspaceDir))
	}
}

// UnregisterTurnByID removes the active turn association by its unique TurnID,
// cleaning up all associated conversationID and workspaceDir mappings.
func (m *Manager) UnregisterTurnByID(turnID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if turnID == "" {
		return
	}
	if turn, ok := m.activeTurns[turnID]; ok {
		delete(m.activeTurns, turnID)
		if turn.ConversationID != "" {
			delete(m.activeTurns, turn.ConversationID)
		}
		if turn.WorkspaceDir != "" {
			delete(m.activeWorkspaces, canonicalizeWorkspacePath(turn.WorkspaceDir))
		}
	}
}

// ResolveTurnByID retrieves the active TurnSecurityContext directly by TurnID.
func (m *Manager) ResolveTurnByID(turnID string) (domain.TurnSecurityContext, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if turnID != "" {
		if t, ok := m.activeTurns[turnID]; ok {
			return t, true
		}
	}
	return domain.TurnSecurityContext{}, false
}

// ResolveSessionKey retrieves the active sessionKey for a given conversationID or workspace.
func (m *Manager) ResolveSessionKey(convID string, workspaceDir string) string {
	if turn, ok := m.ResolveTurnContext(convID, workspaceDir); ok {
		return turn.SessionKey
	}
	return ""
}

// ResolveTurnContext retrieves the full active TurnSecurityContext for a given conversationID or workspace.
func (m *Manager) ResolveTurnContext(convID string, workspaceDir string) (domain.TurnSecurityContext, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if convID != "" {
		if t, ok := m.activeTurns[convID]; ok && t.SessionKey != "" {
			return t, true
		}
	}
	if workspaceDir != "" {
		if t, ok := m.activeWorkspaces[canonicalizeWorkspacePath(workspaceDir)]; ok && t.SessionKey != "" {
			return t, true
		}
	}
	return domain.TurnSecurityContext{}, false
}

// CancelSessionApprovals terminates all pending HITL approval requests for the given session.
func (m *Manager) CancelSessionApprovals(sessionKey string) {
	m.mu.RLock()
	hitl := m.hitlPort
	m.mu.RUnlock()
	if hitl != nil {
		hitl.CancelPendingRequestsForSession(sessionKey)
	}
}
