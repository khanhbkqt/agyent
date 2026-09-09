package pathjail

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"agyent/internal/config"
	"agyent/internal/core/domain"
)

// Evaluator enforces virtual filesystem boundaries and canonical path security.
type Evaluator struct {
	enforceJail     bool
	allowedPaths    []string
	forbiddenPaths  []string
	manageableFiles []string
}

// NewEvaluator constructs a new path jail evaluator with canonicalized paths.
func NewEvaluator(cfg config.FilesystemGuardrailConfig, manageableFiles []string) *Evaluator {
	allowed := make([]string, 0, len(cfg.AllowedPaths))
	for _, p := range cfg.AllowedPaths {
		if p == "." || strings.HasPrefix(p, "./") || strings.HasPrefix(p, ".\\") {
			// Relative paths like "." represent the active workspace and are dynamically
			// evaluated in EvaluatePath, never bound statically to daemon startup CWD.
			continue
		}
		if expanded, err := config.ExpandPath(p); err == nil && expanded != "" {
			if abs, err := filepath.Abs(expanded); err == nil && filepath.IsAbs(expanded) {
				allowed = append(allowed, resolveSymlinksAndCanonicalize(abs))
			}
		}
	}

	forbidden := make([]string, 0, len(cfg.ForbiddenPaths))
	for _, p := range cfg.ForbiddenPaths {
		if expanded, err := config.ExpandPath(p); err == nil && expanded != "" {
			if abs, err := filepath.Abs(expanded); err == nil {
				forbidden = append(forbidden, resolveSymlinksAndCanonicalize(abs))
			}
		}
	}

	manageable := make([]string, 0, len(manageableFiles))
	for _, p := range manageableFiles {
		if expanded, err := config.ExpandPath(p); err == nil && expanded != "" {
			if abs, err := filepath.Abs(expanded); err == nil {
				manageable = append(manageable, resolveSymlinksAndCanonicalize(abs))
			}
		}
	}

	return &Evaluator{
		enforceJail:     cfg.EnforceWorkspaceJail,
		allowedPaths:    allowed,
		forbiddenPaths:  forbidden,
		manageableFiles: manageable,
	}
}

// EvaluatePath validates whether access to targetPath is permitted within workspaceDir.
func (e *Evaluator) EvaluatePath(workspaceDir string, targetPath string, isWrite bool, allowDelegatedConfig bool, extraAllowedPaths ...string) (domain.SecurityDecision, error) {
	if targetPath == "" {
		return domain.SecurityDecision{
			Decision: domain.DecisionDeny,
			Reason:   "Target file path cannot be empty",
		}, nil
	}

	// 1. Expand ~ and make absolute
	expanded, err := config.ExpandPath(targetPath)
	if err != nil {
		expanded = targetPath
	}

	var absTarget string
	if filepath.IsAbs(expanded) {
		absTarget = filepath.Clean(expanded)
	} else {
		absTarget = filepath.Clean(filepath.Join(workspaceDir, expanded))
	}

	// 2. Windows specific quirks check
	if runtime.GOOS == "windows" {
		// Check for Alternate Data Streams (e.g. file.txt:hidden.exe)
		vol := filepath.VolumeName(absTarget)
		pathWithoutVol := strings.TrimPrefix(absTarget, vol)
		if strings.Contains(pathWithoutVol, ":") {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Path Jail]: Windows Alternate Data Streams (ADS) access forbidden: '%s'", targetPath),
			}, nil
		}

		// Block raw device / UNC namespaces
		if strings.HasPrefix(absTarget, `\\?\`) || strings.HasPrefix(absTarget, `\\.\`) || strings.HasPrefix(absTarget, `\\127.0.0.1\`) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Path Jail]: Device and loopback UNC path access forbidden: '%s'", targetPath),
			}, nil
		}
	}

	// 3. Resolve Symlinks and canonicalize target path
	canonTarget := resolveSymlinksAndCanonicalize(absTarget)

	// 4. Control-plane path protection: writing to security hooks or daemon database/config is strictly forbidden
	if isWrite {
		if isForbidden, desc := isControlPlaneWriteForbidden(canonTarget); isForbidden {
			// If target is in manageable files and delegated config is enabled (e.g. config.yaml in developer preset) -> Ask via HITL
			if allowDelegatedConfig && e.isManageable(canonTarget) && !isHookConfigFile(canonTarget) && !isDaemonDatabaseFile(canonTarget) {
				return domain.SecurityDecision{
					Decision: domain.DecisionAsk,
					Reason:   fmt.Sprintf("🛡️ [Security Gate]: Modification to protected configuration file '%s' requires user confirmation.", targetPath),
				}, nil
			}

			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Path Jail - Control Plane Protection]: Modification of %s ('%s') is strictly forbidden", desc, targetPath),
			}, nil
		}
	}

	// 5. Absolute Forbidden Blacklist check (Tier 0 Host Infrastructure)
	for _, forbidden := range e.forbiddenPaths {
		if pathMatches(canonTarget, forbidden) {
			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Path Jail]: Access strictly forbidden to protected path: '%s'", targetPath),
			}, nil
		}
	}

	// 6. Workspace Jail Enforcement check
	if e.enforceJail {
		canonWorkspace := resolveSymlinksAndCanonicalize(workspaceDir)
		isInside := canonWorkspace != "" && pathMatches(canonTarget, canonWorkspace)

		if !isInside {
			for _, allowed := range e.allowedPaths {
				if pathMatches(canonTarget, allowed) {
					isInside = true
					break
				}
			}
		}

		if !isInside && len(extraAllowedPaths) > 0 {
			for _, extra := range extraAllowedPaths {
				if extra == "" {
					continue
				}
				expandedExtra, err := config.ExpandPath(extra)
				if err != nil {
					expandedExtra = extra
				}
				var absExtra string
				if filepath.IsAbs(expandedExtra) {
					absExtra = filepath.Clean(expandedExtra)
				} else {
					absExtra = filepath.Clean(filepath.Join(workspaceDir, expandedExtra))
				}
				canonExtra := resolveSymlinksAndCanonicalize(absExtra)
				if canonExtra != "" && pathMatches(canonTarget, canonExtra) {
					isInside = true
					break
				}
			}
		}

		if !isInside {
			// If target is in manageable files and delegated config is enabled -> Ask via HITL
			if allowDelegatedConfig && e.isManageable(canonTarget) {
				return domain.SecurityDecision{
					Decision: domain.DecisionAsk,
					Reason:   fmt.Sprintf("🛡️ [Security Gate]: Modification to external configuration file '%s' requires user confirmation.", targetPath),
				}, nil
			}

			return domain.SecurityDecision{
				Decision: domain.DecisionDeny,
				Reason:   fmt.Sprintf("🛡️ [Path Jail]: Target path '%s' resides outside active workspace '%s'", targetPath, workspaceDir),
			}, nil
		}
	}

	return domain.SecurityDecision{
		Decision: domain.DecisionAllow,
		Reason:   "Target path verified within active filesystem jail",
	}, nil
}

func (e *Evaluator) isManageable(canonPath string) bool {
	baseName := filepath.Base(canonPath)
	for _, m := range e.manageableFiles {
		if pathMatches(canonPath, m) {
			return true
		}
		mBase := filepath.Base(m)
		if mBase == baseName {
			return true
		}
		if matched, err := filepath.Match(strings.ToLower(mBase), strings.ToLower(baseName)); err == nil && matched {
			return true
		}
	}
	return false
}

func resolveSymlinksAndCanonicalize(p string) string {
	if p == "" {
		return ""
	}
	// Walk up to find nearest existing ancestor directory
	curr := filepath.Clean(p)
	suffix := ""
	for {
		if eval, err := filepath.EvalSymlinks(curr); err == nil {
			curr = eval
			break
		}
		parent := filepath.Dir(curr)
		if parent == curr || parent == "." || parent == "" {
			break
		}
		if suffix == "" {
			suffix = filepath.Base(curr)
		} else {
			suffix = filepath.Join(filepath.Base(curr), suffix)
		}
		curr = parent
	}
	if suffix != "" {
		curr = filepath.Join(curr, suffix)
	}
	return canonicalize(curr)
}

func canonicalize(p string) string {
	cleaned := filepath.Clean(p)
	if runtime.GOOS == "windows" {
		cleaned = strings.ToLower(cleaned)
		cleaned = strings.ReplaceAll(cleaned, "/", "\\")
	}
	return cleaned
}

func pathMatches(target, base string) bool {
	if target == base {
		return true
	}
	baseWithSep := base
	if !strings.HasSuffix(baseWithSep, string(filepath.Separator)) {
		baseWithSep += string(filepath.Separator)
	}
	return strings.HasPrefix(target, baseWithSep)
}

func (e *Evaluator) AllowedPaths() []string {
	return append([]string(nil), e.allowedPaths...)
}

func isControlPlaneWriteForbidden(canonTarget string) (bool, string) {
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(canonTarget)))

	if isHookConfigFile(norm) {
		return true, "security hook configuration"
	}
	if isDaemonDatabaseFile(norm) {
		return true, "gateway database"
	}
	if isDaemonConfigFile(norm) {
		return true, "gateway daemon configuration file"
	}
	return false, ""
}

func isHookConfigFile(normPath string) bool {
	normPath = strings.ToLower(filepath.ToSlash(filepath.Clean(normPath)))
	return strings.HasSuffix(normPath, "/.agents/hooks.json") ||
		normPath == ".agents/hooks.json" ||
		strings.HasSuffix(normPath, "/.gemini/config/hooks.json") ||
		normPath == ".gemini/config/hooks.json"
}

func isDaemonDatabaseFile(normPath string) bool {
	normPath = strings.ToLower(filepath.ToSlash(filepath.Clean(normPath)))
	base := filepath.Base(normPath)
	return base == "agyent.db" || strings.HasPrefix(base, "agyent.db-") || strings.HasPrefix(base, "agyent.db.")
}

func isDaemonConfigFile(normPath string) bool {
	normPath = strings.ToLower(filepath.ToSlash(filepath.Clean(normPath)))
	base := filepath.Base(normPath)
	if base != "config.yaml" && base != "config.yml" && base != "config.json" {
		return false
	}
	dir := strings.ToLower(filepath.ToSlash(filepath.Dir(normPath)))
	return strings.HasSuffix(dir, "/.agyent") || dir == ".agyent" || dir == "~/.agyent"
}


