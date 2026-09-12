package security

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
var selfEscalationRegex = regexp.MustCompile(`(?i)(agyent(\.exe)?\s+(agent|agents|a|security|sec|guardrail|init|config)\b|\b(rm|del|erase|rmdir|rd|mv|remove-item|move-item)\s+(?:.*[\s/\\~"'])?\.agyent\b|\.agyent[/\\](agyent\.db([.-].*)?|config\.ya?ml|config\.json)|\bagyent\.db([.-].*)?\b|\.agents[/\\]hooks\.json|\.gemini[/\\]config|\b(pkill|killall|taskkill)\s+.*agyent\b)`)

func isSelfEscalationCommand(cmd string) bool {
	return selfEscalationRegex.MatchString(cmd)
}

func isControlPlaneTarget(target string) bool {
	if target == "" {
		return false
	}
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.TrimSpace(target))))
	base := filepath.Base(norm)

	// Strip Windows Alternate Data Stream (ADS) suffix if present (e.g. "config.yaml::$DATA" -> "config.yaml")
	if colonIdx := strings.Index(base, ":"); colonIdx != -1 {
		base = base[:colonIdx]
	}

	// 1. Hook configuration files (.agents/hooks.json, .gemini/config/hooks.json, hooks.json)
	if strings.HasSuffix(norm, "/.agents/hooks.json") || norm == ".agents/hooks.json" || strings.Contains(norm, ".agents/hooks.json") ||
		strings.HasSuffix(norm, "/.gemini/config/hooks.json") || norm == ".gemini/config/hooks.json" || base == "hooks.json" {
		return true
	}

	// 2. Gateway database files (agyent.db, agyent.db-wal, agyent.db-shm, etc.)
	if base == "agyent.db" || strings.HasPrefix(base, "agyent.db-") || strings.HasPrefix(base, "agyent.db.") || strings.Contains(norm, "agyent.db") {
		return true
	}

	// 3. Daemon configuration paths (.agyent directory and daemon files/plugins within)
	// Differentiate control plane from agent cognitive memory and agent workspaces residing under ~/.agyent
	isAgentDataOrWorkspace := (strings.Contains(norm, "/.agyent/workspace") || strings.HasPrefix(norm, ".agyent/workspace") ||
		((strings.Contains(norm, "/.agyent/agents/") || strings.HasPrefix(norm, ".agyent/agents/")) && (strings.Contains(norm, "/memory/") || strings.Contains(norm, "/workspace/")))) &&
		!strings.Contains(norm, "/plugins/") && !strings.Contains(norm, "/hooks/")

	if !isAgentDataOrWorkspace {
		if strings.Contains(norm, "/.agyent") || strings.HasPrefix(norm, ".agyent") || strings.Contains(norm, "~/.agyent") {
			return true
		}
	}

	// 4. Gateway daemon configuration files (config.yaml, config.yml, or root/daemon config.json)
	if base == "config.yaml" || base == "config.yml" || norm == "config.json" ||
		strings.HasSuffix(norm, "/.agyent/config.json") || strings.HasSuffix(norm, "/config.yaml") || strings.HasSuffix(norm, "/config.yml") {
		return true
	}

	return false
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
	sessionGrants    map[string][]domain.SessionGrant      // sessionKey -> grants
	shortCodeSeq     uint32
	activeTurns      map[string]domain.TurnSecurityContext // convID -> TurnSecurityContext
	activeWorkspaces map[string]domain.TurnSecurityContext // workspaceDir -> TurnSecurityContext
	eventBus         ports.EventBusPort
	logger           *slog.Logger

	// Metrics (Lock-free atomic counters)
	totalEvaluations atomic.Int64
	blockedToday     atomic.Int64
	approvedToday    atomic.Int64
}

// generateShortCode produces a memorable 4-digit numeric code (1000-9999) for quick HITL reference.
func (m *Manager) generateShortCode() string {
	seq := atomic.AddUint32(&m.shortCodeSeq, 1)
	code := 1000 + (seq % 9000)
	return fmt.Sprintf("%04d", code)
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
		sessionGrants:    make(map[string][]domain.SessionGrant),
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

func (m *Manager) isTurnAdmin(turnCtx domain.TurnSecurityContext) bool {
	if turnCtx.IsAdmin {
		return true
	}
	if turnCtx.Role == domain.AgentRoleOwner || turnCtx.Role == domain.AgentRoleAdmin {
		return true
	}
	if turnCtx.Principal.Kind == domain.PrincipalSystem {
		if strings.EqualFold(turnCtx.Principal.SubjectID, "superadmin") || strings.EqualFold(turnCtx.Principal.SubjectID, "admin") {
			return true
		}
	}
	if id, err := strconv.ParseInt(turnCtx.Principal.SubjectID, 10, 64); err == nil {
		m.mu.RLock()
		defer m.mu.RUnlock()
		for _, adminID := range m.defaultCfg.AdminUserIDs {
			if adminID == id {
				return true
			}
		}
	}
	return false
}

// EvaluateToolCall intercepts any tool call synchronously before substrate execution.
func (m *Manager) EvaluateToolCall(ctx context.Context, req domain.ToolEvaluationRequest) (domain.SecurityDecision, error) {
	start := time.Now()

	m.totalEvaluations.Add(1)

	// Resolve the active turn's security context (prefer TurnID to prevent race conditions across turns)
	var turnCtx domain.TurnSecurityContext
	var hasTurn bool
	if req.TurnID != "" {
		turnCtx, hasTurn = m.ResolveTurnByID(req.TurnID)
	}
	if !hasTurn {
		turnCtx, hasTurn = m.ResolveTurnContext(req.ConversationID, req.WorkspaceDir)
	}

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
	role := req.Role
	if role == "" && hasTurn {
		role = string(turnCtx.Role)
	}

	// 0. If Preset is Unrestricted -> Allow 100% of tool calls only for authenticated SuperAdmin/System/Admin
	if preset == domain.PresetUnrestricted {
		isUntrackedRequest := (req.TurnID != "" || req.ConversationID != "" || req.WorkspaceDir != "" || req.SessionKey != "") && !hasTurn
		if (hasTurn && !m.isTurnAdmin(turnCtx)) || isUntrackedRequest {
			// Non-admin or untracked callers cannot run in unrestricted mode; downgrade to balanced preset
			preset = domain.PresetBalanced
		} else {
			// Inviolable Hard Guardrail: Anti-self-escalation is strictly enforced across all presets
			if req.ToolName == "run_command" {
				if cmd, ok := req.Args["CommandLine"].(string); ok && isSelfEscalationCommand(cmd) {
					return domain.SecurityDecision{
						Decision: domain.DecisionDeny,
						Reason:   fmt.Sprintf("🛡️ [Security Gate - Privilege Escalation Blocked]: Execution of administrative command '%s' to alter agyent configuration or security presets is strictly forbidden from an AI agent session", cmd),
					}, nil
				}
			} else if req.ToolName == "write_to_file" || req.ToolName == "replace_file_content" {
				targetFile, _ := req.Args["TargetFile"].(string)
				if targetFile == "" {
					targetFile, _ = req.Args["target_file"].(string)
				}
				if targetFile == "" {
					targetFile, _ = req.Args["targetFile"].(string)
				}
				if targetFile == "" {
					targetFile, _ = req.Args["FilePath"].(string)
				}
				if targetFile == "" {
					targetFile, _ = req.Args["file_path"].(string)
				}
				if targetFile == "" {
					targetFile, _ = req.Args["Path"].(string)
				}
				if targetFile == "" {
					targetFile, _ = req.Args["path"].(string)
				}

				ws := req.WorkspaceDir
				if ws == "" && hasTurn {
					ws = turnCtx.WorkspaceDir
				}
				expandedTarget, _ := config.ExpandPath(targetFile)
				if expandedTarget == "" {
					expandedTarget = targetFile
				}
				var absTarget string
				if filepath.IsAbs(expandedTarget) {
					absTarget = filepath.Clean(expandedTarget)
				} else if ws != "" {
					absTarget = filepath.Clean(filepath.Join(ws, expandedTarget))
				}
				var canonTarget string
				if absTarget != "" {
					canonTarget = pathjail.ResolveSymlinksAndCanonicalize(absTarget)
				}

				if isControlPlaneTarget(targetFile) || (canonTarget != "" && isControlPlaneTarget(canonTarget)) {
					return domain.SecurityDecision{
						Decision: domain.DecisionDeny,
						Reason:   fmt.Sprintf("🛡️ [Security Gate - Control-Plane Guardrail]: Modification of protected control-plane or configuration file '%s' is strictly forbidden across all presets", targetFile),
					}, nil
				}

				content, _ := req.Args["CodeContent"].(string)
				if content == "" {
					content, _ = req.Args["ReplacementContent"].(string)
				}
				if isSelfEscalationCommand(content) {
					return domain.SecurityDecision{
						Decision: domain.DecisionDeny,
						Reason:   "🛡️ [Security Gate - Staged Privilege Escalation Blocked]: Attempted to write code containing forbidden references to agyent gateway database, configuration, or administrative commands",
					}, nil
				}
			}

			m.recordApproved()
			return domain.SecurityDecision{
				Decision:  domain.DecisionAllow,
				Reason:    "🛡️ [Security Preset: Unrestricted]: Tool call permitted with full autonomy for administrator",
				LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
			}, nil
		}
	}

	bundle := m.getEvaluatorBundle(preset)

	var decision domain.SecurityDecision
	var err error

	switch req.ToolName {
	case "run_command":
		cmd, _ := req.Args["CommandLine"].(string)
		cwd, _ := req.Args["Cwd"].(string)
		ws := req.WorkspaceDir
		if ws == "" && hasTurn {
			ws = turnCtx.WorkspaceDir
		}
		var extraPaths []string
		if hasTurn {
			extraPaths = turnCtx.AllowedPaths
		}
		if cwd != "" {
			cwdWs := ws
			if cwdWs == "" {
				cwdWs = "."
			}
			cwdDec, cwdErr := bundle.pathjailEval.EvaluatePath(cwdWs, cwd, false, false, extraPaths...)
			if cwdErr != nil || cwdDec.Decision == domain.DecisionDeny {
				decision = domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Security Gate - Path Jail]: Working directory (Cwd) '%s' is outside active workspace", cwd),
				}
				break
			}
		}
		decision, err = m.evaluateCommandWithBundle(ctx, sessionKey, role, cmd, ws, cwd, bundle, extraPaths...)

	case "view_file", "write_to_file", "replace_file_content", "list_dir", "grep_search", "find_by_name":
		targetPath, _ := req.Args["TargetFile"].(string)
		if targetPath == "" {
			targetPath, _ = req.Args["target_file"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["targetFile"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["DirectoryPath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["directory_path"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["SearchDirectory"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["search_directory"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["SearchPath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["search_path"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["Path"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["path"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["AbsolutePath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["absolute_path"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["FilePath"].(string)
		}
		if targetPath == "" {
			targetPath, _ = req.Args["file_path"].(string)
		}
		if targetPath == "" {
			targetPath = "."
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
		var extraPaths []string
		if hasTurn {
			extraPaths = turnCtx.AllowedPaths
		}
		decision, err = bundle.pathjailEval.EvaluatePath(ws, targetPath, isWrite, bundle.cfg.AgentConfigManagement.Enabled, extraPaths...)

	case "read_url_content":
		urlStr, _ := req.Args["Url"].(string)
		if urlStr == "" {
			urlStr, _ = req.Args["url"].(string)
		}
		if urlStr == "" {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: Missing required URL parameter for read_url_content",
			}
		} else {
			decision, err = bundle.networkEval.EvaluateURL(urlStr)
		}

	case "read_browser_page":
		urlStr, _ := req.Args["Url"].(string)
		if urlStr == "" {
			urlStr, _ = req.Args["url"].(string)
		}
		if urlStr != "" {
			decision, err = bundle.networkEval.EvaluateURL(urlStr)
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Browser substrate page inspection permitted",
			}
		}

	case "web_search":
		query, _ := req.Args["query"].(string)
		if query == "" {
			query, _ = req.Args["Query"].(string)
		}
		if strings.TrimSpace(query) == "" {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: Empty search query rejected",
			}
		} else if domainStr, ok := req.Args["domain"].(string); ok && domainStr != "" {
			decision, err = bundle.networkEval.EvaluateURL("https://" + domainStr)
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Web search query permitted",
			}
		}

	case "manage_task":
		action, _ := req.Args["Action"].(string)
		if action == "" {
			action, _ = req.Args["action"].(string)
		}
		if preset == domain.PresetReadOnly && (action == "kill" || action == "send_input") {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate]: Task action %q is forbidden under Read Only security preset", action),
			}
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Task management substrate permitted",
			}
		}

	case "manage_subagents":
		action, _ := req.Args["Action"].(string)
		if action == "" {
			action, _ = req.Args["action"].(string)
		}
		if preset == domain.PresetReadOnly {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate]: Subagent management action %q is forbidden under Read Only security preset", action),
			}
		} else if preset == domain.PresetWorkspaceOnly && action == "dispatch" && bundle.cfg.Subagents.MaxCascadeDepth == 0 {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: Subagent cascading dispatch is disabled in workspace_only security preset",
			}
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Subagent lifecycle substrate permitted",
			}
		}

	case "generate_image":
		if preset == domain.PresetReadOnly {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: Image generation and file creation is forbidden under Read Only security preset",
			}
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Image generation substrate permitted",
			}
		}

	case "schedule":
		if preset == domain.PresetReadOnly {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   "🛡️ [Security Gate]: Schedule creation is forbidden under Read Only security preset",
			}
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Scheduler substrate permitted",
			}
		}

	case "send_message", "ask_question":
		decision = domain.SecurityDecision{
			Decision: domain.DecisionAllow,
			Reason:   "Substrate communication permitted",
		}

	case "invoke_subagent", "define_subagent":
		decision, err = bundle.subagentEval.EvaluateSubagent(req.IsSubagent, req.CascadeDepth, req.ToolName, role, 0)

	default:
		// Check if it's an MCP tool (starts with mcp__ or call_mcp_tool)
		if strings.HasPrefix(req.ToolName, "mcp__") || req.ToolName == "call_mcp_tool" {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Registered MCP tool permitted",
			}
		} else {
			decision = domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate]: Unknown or unregistered tool '%s' is denied fail-closed", req.ToolName),
			}
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

	// If decision requires HITL:
	if decision.Decision == domain.DecisionAsk {
		// If session has an active wildcard grant -> auto-approve this askable action
		if m.HasWildcardGrant(sessionKey) {
			m.recordApproved()
			return domain.SecurityDecision{
				Decision:  domain.DecisionAllow,
				Reason:    "Allowed by active wildcard session permission grant",
				LatencyMs: float64(time.Since(start).Microseconds()) / 1000.0,
			}, nil
		}

		if hitlPort != nil {
			if timeoutSec <= 0 {
				timeoutSec = 60
			}
			agentName := "agent"
			if hasTurn && turnCtx.AgentName != "" {
				agentName = turnCtx.AgentName
			}
			appReq := domain.ApprovalRequest{
				RequestID:    fmt.Sprintf("hitl-%d", time.Now().UnixNano()),
				ShortCode:    m.generateShortCode(),
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

			m.logger.Info("Requesting HITL approval", "tool", req.ToolName, "reason", decision.Reason, "agent", agentName, "short_code", appReq.ShortCode)
			appDecision, appErr := hitlPort.RequestApproval(ctx, appReq)
			canonicalAct, _ := domain.ParseApprovalAction(string(appDecision.Action))
			if appErr != nil || !appDecision.Approved {
				m.recordBlocked()

				if canonicalAct == domain.ActionForceKill {
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
			if canonicalAct == domain.ActionAllowAllSession {
				m.GrantSessionPermission(sessionKey, "*")
			} else if canonicalAct == domain.ActionAllowSession && appReq.CommandLine != "" {
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
	}
	if decision.Decision == domain.DecisionAsk {
		m.recordBlocked()
		return domain.SecurityDecision{
			Decision:  domain.DecisionDeny,
			Reason:    "🛡️ [Security Gate]: Interactive approval channel is unavailable; action denied",
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

var (
	base64JSBufferRegex    = regexp.MustCompile(`(?i)Buffer\.from\(\s*['"]([A-Za-z0-9+/=]{8,})['"]\s*,\s*['"]base64['"]\s*\)`)
	base64AtobRegex        = regexp.MustCompile(`(?i)atob\(\s*['"]([A-Za-z0-9+/=]{8,})['"]\s*\)`)
	base64PythonRegex      = regexp.MustCompile(`(?i)b64decode\(\s*(?:b)?['"]([A-Za-z0-9+/=]{8,})['"]\s*\)`)
	base64ShellPipeRegex   = regexp.MustCompile(`(?i)echo\s+['"]?([A-Za-z0-9+/=]{8,})['"]?\s*\|\s*base64\s+-(?:d|-decode)`)
	base64GenericBlobRegex = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={0,2}`)
)

func scanDeobfuscatedPayloads(content string, targetName string, bundle *presetEvaluators) (domain.SecurityDecision, bool) {
	var candidates []string

	for _, m := range base64JSBufferRegex.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			candidates = append(candidates, m[1])
		}
	}
	for _, m := range base64AtobRegex.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			candidates = append(candidates, m[1])
		}
	}
	for _, m := range base64PythonRegex.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			candidates = append(candidates, m[1])
		}
	}
	for _, m := range base64ShellPipeRegex.FindAllStringSubmatch(content, -1) {
		if len(m) > 1 {
			candidates = append(candidates, m[1])
		}
	}
	for _, blob := range base64GenericBlobRegex.FindAllString(content, 20) {
		candidates = append(candidates, blob)
	}

	forbiddenMarkers := []string{
		".ssh", ".aws", ".gnupg", ".kube", "config.yaml", "agyent.db", "hooks.json",
		"/etc/shadow", "/etc/passwd", "/etc/sudoers", "/private/etc",
	}

	for _, cand := range candidates {
		trimmed := strings.TrimSpace(cand)
		if len(trimmed)%4 != 0 {
			trimmed += strings.Repeat("=", 4-(len(trimmed)%4))
		}
		decodedBytes, err := base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			continue
		}
		decodedStr := string(decodedBytes)
		decodedLower := strings.ToLower(decodedStr)

		// 1. Check forbidden markers in decoded payload
		for _, marker := range forbiddenMarkers {
			if strings.Contains(decodedLower, marker) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Security Gate - Obfuscation Detection]: Target '%s' contains Base64-obfuscated reference to forbidden path '%s'", targetName, marker),
				}, true
			}
		}

		// 2. Check blacklist patterns in decoded payload
		for _, bl := range bundle.blacklistPatterns {
			if bl.MatchString(decodedStr) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Security Gate - Obfuscation Detection]: Target '%s' contains Base64-obfuscated destructive command '%s'", targetName, bl.String()),
				}, true
			}
		}

		// 3. Check anti-self-escalation in decoded payload
		if strings.Contains(decodedLower, "agyent") && (strings.Contains(decodedLower, "config.yaml") || strings.Contains(decodedLower, "agyent.db") || strings.Contains(decodedLower, "pkill") || strings.Contains(decodedLower, "killall")) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Obfuscation Detection]: Target '%s' contains Base64-obfuscated attack against gateway control-plane", targetName),
			}, true
		}
	}

	return domain.SecurityDecision{}, false
}

func inspectScriptContent(scriptPath string, data []byte, bundle *presetEvaluators) (domain.SecurityDecision, bool) {
	content := string(data)
	contentLower := strings.ToLower(content)

	// 1. Anti-self-escalation & gateway tamper check
	if strings.Contains(contentLower, "agyent") && (strings.Contains(contentLower, "config.yaml") || strings.Contains(contentLower, "agyent.db") || strings.Contains(contentLower, "hooks.json") || strings.Contains(contentLower, "pkill") || strings.Contains(contentLower, "killall")) {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Security Gate - Script Pre-Inspection]: Script '%s' references protected gateway configuration or control-plane resources", scriptPath),
		}, true
	}

	// 2. Forbidden path references
	forbiddenMarkers := []string{
		".ssh", ".aws", ".gnupg", ".kube", "config.yaml", "agyent.db", "hooks.json",
		"/etc/shadow", "/etc/passwd", "/etc/sudoers", "/private/etc",
	}
	for _, marker := range forbiddenMarkers {
		if strings.Contains(contentLower, marker) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Script Pre-Inspection]: Script '%s' contains forbidden path reference '%s'", scriptPath, marker),
			}, true
		}
	}

	// 3. Destructive blacklist patterns
	for _, bl := range bundle.blacklistPatterns {
		if bl.MatchString(content) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Script Pre-Inspection]: Script '%s' contains forbidden destructive pattern '%s'", scriptPath, bl.String()),
			}, true
		}
	}

	// 4. De-obfuscate Base64 payloads and inspect inside
	if dec, violated := scanDeobfuscatedPayloads(content, scriptPath, bundle); violated {
		return dec, true
	}

	return domain.SecurityDecision{}, false
}

func (m *Manager) evaluateCommandWithBundle(ctx context.Context, sessionKey string, role string, cmd string, ws string, cwd string, bundle *presetEvaluators, extraAllowedPaths ...string) (domain.SecurityDecision, error) {
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

	parsedCmds := ParseCommandPipeline(cmd)

	// 1.5. Inviolable Hard Guardrail: Check Anti-Self-Escalation across raw and all parsed commands
	if isSelfEscalationCommand(cmd) {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   fmt.Sprintf("🛡️ [Security Gate - Privilege Escalation Blocked]: Execution of administrative command '%s' to alter agyent configuration or security presets is strictly forbidden from an AI agent session", cmd),
		}, nil
	}
	for _, pcmd := range parsedCmds {
		if isSelfEscalationCommand(pcmd.Executable) || isSelfEscalationCommand(pcmd.Raw) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Privilege Escalation Blocked]: Subcommand '%s' references forbidden gateway resources", pcmd.Raw),
			}, nil
		}
	}

	// 1.6. De-obfuscation Check on inline commands
	if dec, violated := scanDeobfuscatedPayloads(cmd, "inline command", bundle); violated {
		return dec, nil
	}

	// 2. Inviolable Hard Guardrail: Check Blacklist Patterns
	for _, bl := range bundle.blacklistPatterns {
		if bl.MatchString(cmd) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Command Guardrail]: Command matches forbidden pattern '%s'", bl.String()),
			}, nil
		}
		for _, pcmd := range parsedCmds {
			if bl.MatchString(pcmd.Raw) || bl.MatchString(pcmd.Executable) {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Command Guardrail]: Pipeline subcommand '%s' matches forbidden pattern '%s'", pcmd.Raw, bl.String()),
				}, nil
			}
		}
	}

	// 2.5. Static Script Pre-Inspection: Inspect local script files referenced in commands
	if ws != "" {
		scriptRefs := ExtractScriptFileReferences(parsedCmds)
		for _, sref := range scriptRefs {
			targetScript := sref
			if !filepath.IsAbs(targetScript) {
				if cwd != "" {
					targetScript = filepath.Join(cwd, targetScript)
				} else {
					targetScript = filepath.Join(ws, targetScript)
				}
			}
			targetScript = filepath.Clean(targetScript)

			// Verify script path is within workspace or allowed paths
			pathDec, pathErr := bundle.pathjailEval.EvaluatePath(ws, targetScript, false, false, extraAllowedPaths...)
			if pathErr != nil || pathDec.Decision == domain.DecisionDeny {
				return domain.SecurityDecision{
					Decision: domain.DecisionDeny,
					Reason:   fmt.Sprintf("🛡️ [Security Gate - Script Pre-Inspection]: Target script '%s' is outside active workspace", sref),
				}, nil
			}

			// If file exists on disk, inspect content
			if info, err := os.Stat(targetScript); err == nil && !info.IsDir() {
				if info.Size() <= 2*1024*1024 {
					if data, err := os.ReadFile(targetScript); err == nil {
						if denial, violated := inspectScriptContent(sref, data, bundle); violated {
							return denial, nil
						}
					}
				}
			}
		}
	}

	// 2.7. Inviolable Hard Guardrail: Deep-scan command arguments and redirections for control-plane and sensitive paths
	for _, pcmd := range parsedCmds {
		if hasControlPlanePathReference(pcmd) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Security Gate - Control Plane Protection]: Command '%s' attempts to reference or mutate forbidden control-plane or system paths", pcmd.Raw),
			}, nil
		}
	}

	// 3. Check in-memory session grants (Valid for the active session, after hard guardrails pass)
	m.mu.RLock()
	rawGrants, hasGrants := m.sessionGrants[sessionKey]
	var grants []domain.SessionGrant
	if hasGrants && len(rawGrants) > 0 {
		grants = make([]domain.SessionGrant, len(rawGrants))
		copy(grants, rawGrants)
	}
	m.mu.RUnlock()

	if len(grants) > 0 && len(parsedCmds) > 0 {
		hasWildcard := false
		for _, g := range grants {
			if g.Pattern == "*" {
				hasWildcard = true
				break
			}
		}
		if hasWildcard {
			return domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Allowed by active wildcard session permission grant",
			}, nil
		}

		allGranted := true
		for _, pcmd := range parsedCmds {
			granted := false
			for _, g := range grants {
				if g.Pattern != "" && (g.Pattern == "*" || strings.EqualFold(pcmd.Executable, g.Pattern) || (pcmd.ParentExec != "" && strings.EqualFold(pcmd.ParentExec, g.Pattern)) || strings.HasPrefix(pcmd.Raw, g.Pattern+" ") || isBaseCommandMatch(pcmd.Raw, g.Pattern)) {
					granted = true
					break
				}
			}
			if !granted {
				allGranted = false
				break
			}
		}
		if allGranted {
			return domain.SecurityDecision{
				Decision: domain.DecisionAllow,
				Reason:   "Allowed by active session permission grant",
			}, nil
		}
	}

	m.mu.RLock()
	wlPatterns := make([]*regexp.Regexp, len(bundle.whitelistPatterns))
	copy(wlPatterns, bundle.whitelistPatterns)
	blPatterns := make([]*regexp.Regexp, len(bundle.blacklistPatterns))
	copy(blPatterns, bundle.blacklistPatterns)
	m.mu.RUnlock()

	return EvaluateParsedCommandPolicy(
		parsedCmds,
		cmd,
		preset,
		sensitiveCommandRegex,
		blPatterns,
		wlPatterns,
	)
}

// EvaluateCommand validates shell commands against active rules and session grants.
func (m *Manager) EvaluateCommand(ctx context.Context, sessionKey string, role string, cmd string) (domain.SecurityDecision, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	return m.evaluateCommandWithBundle(ctx, sessionKey, role, cmd, "", "", bundle)
}

// EvaluatePath validates file access within workspace boundaries.
func (m *Manager) EvaluatePath(ctx context.Context, sessionKey string, workspaceDir string, targetPath string, isWrite bool) (domain.SecurityDecision, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	if workspaceDir == "" {
		workspaceDir = "."
	}
	var extraPaths []string
	if turnCtx, ok := m.ResolveTurnContext("", workspaceDir); ok {
		extraPaths = turnCtx.AllowedPaths
	}
	return bundle.pathjailEval.EvaluatePath(workspaceDir, targetPath, isWrite, bundle.cfg.AgentConfigManagement.Enabled, extraPaths...)
}

// EvaluateURL validates outbound network URLs.
func (m *Manager) EvaluateURL(ctx context.Context, urlStr string) (domain.SecurityDecision, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	return bundle.networkEval.EvaluateURL(urlStr)
}

// SanitizeToolOutput masks secrets within tool output and validates against indirect prompt injection.
func (m *Manager) SanitizeToolOutput(ctx context.Context, toolName string, output string) (string, error) {
	bundle := m.getEvaluatorBundle(m.defaultPreset)
	redacted := bundle.sanitizerEval.RedactSecrets(output)
	if err := bundle.sanitizerEval.ValidateInjection(output); err != nil {
		m.logger.WarnContext(ctx, "Indirect prompt injection detected in tool output",
			"tool_name", toolName,
			"error", err,
		)
		neutralized := fmt.Sprintf("[POTENTIAL PROMPT INJECTION DETECTED AND NEUTRALIZED]\n%s", redacted)
		return neutralized, err
	}
	return redacted, nil
}

// HasWildcardGrant checks if the active session has an active wildcard (*) grant.
func (m *Manager) HasWildcardGrant(sessionKey string) bool {
	if sessionKey == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	grants, ok := m.sessionGrants[sessionKey]
	if !ok {
		return false
	}
	for _, g := range grants {
		if g.Pattern == "*" {
			return true
		}
	}
	return false
}

// GetSessionGrants retrieves all active permission grants for a given session.
func (m *Manager) GetSessionGrants(sessionKey string) []domain.SessionGrant {
	if sessionKey == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	grants, ok := m.sessionGrants[sessionKey]
	if !ok || len(grants) == 0 {
		return nil
	}
	result := make([]domain.SessionGrant, len(grants))
	copy(result, grants)
	return result
}

// GrantSessionPermission adds a permission grant to the session cache for the active session.
func (m *Manager) GrantSessionPermission(sessionKey string, pattern string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return
	}
	scope := "command"
	if pattern == "*" || strings.EqualFold(pattern, "all") || strings.EqualFold(pattern, "all_session") {
		pattern = "*"
		scope = "wildcard"
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, g := range m.sessionGrants[sessionKey] {
		if g.Pattern == pattern {
			return
		}
	}

	m.sessionGrants[sessionKey] = append(m.sessionGrants[sessionKey], domain.SessionGrant{
		Pattern:   pattern,
		Scope:     scope,
		GrantedAt: time.Now(),
	})
	m.logger.Info("Granted session permission", "session", sessionKey, "pattern", pattern, "scope", scope)
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

	m.sessionGrants = make(map[string][]domain.SessionGrant)
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

	if grants, ok := m.sessionGrants[sessionKey]; ok {
		for _, g := range grants {
			if g.Pattern == "*" {
				allowedCmds = append(allowedCmds, "* (wildcard session grant)")
			} else {
				allowedCmds = append(allowedCmds, fmt.Sprintf("%s (session)", g.Pattern))
			}
		}
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
			canonWS := canonicalizeWorkspacePath(turn.WorkspaceDir)
			stillUsed := false
			for _, other := range m.activeTurns {
				if other.TurnID != turnID && canonicalizeWorkspacePath(other.WorkspaceDir) == canonWS {
					m.activeWorkspaces[canonWS] = other
					stillUsed = true
					break
				}
			}
			if !stillUsed {
				delete(m.activeWorkspaces, canonWS)
			}
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
