package context

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.ContextResolverPort = (*ContextResolver)(nil)

// ContextResolver implements ContextResolverPort for scanning and merging hierarchical context.
type ContextResolver struct{}

// NewContextResolver creates a new ContextResolver instance.
func NewContextResolver() *ContextResolver {
	return &ContextResolver{}
}

// ValidateSecurePath verifies that targetPath is contained within rootDir to prevent Path Traversal.
func ValidateSecurePath(rootDir, targetPath string) (string, error) {
	cleanRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("invalid root directory: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		realRoot = cleanRoot
	}

	fullPath := targetPath
	if !filepath.IsAbs(targetPath) {
		fullPath = filepath.Join(realRoot, targetPath)
	}

	cleanTarget, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid target path: %w", err)
	}
	realTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		realTarget = cleanTarget
	}

	relRoot := realRoot
	relTarget := realTarget
	if runtime.GOOS == "windows" {
		relRoot = strings.ToLower(relRoot)
		relTarget = strings.ToLower(relTarget)
	}

	rel, err := filepath.Rel(relRoot, relTarget)
	if err != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, "/..") || strings.HasPrefix(rel, `\..`) {
		return "", fmt.Errorf("security violation: path traversal detected outside root: %s", targetPath)
	}

	return realTarget, nil
}

// Resolve combines Global and Workspace directives and extracts active skills index.
func (r *ContextResolver) Resolve(ctx context.Context, globalHome string, workspaceDir string) (*domain.ResolvedContext, error) {
	resolved := &domain.ResolvedContext{
		WorkingDir:   workspaceDir,
		UserLocation: DetectUserLocation(globalHome),
		ResolvedAt:   time.Now(),
	}

	// 1. Scan Global Directives (Level 1: static prefix)
	if globalHome != "" {
		resolved.GlobalDirectives = r.scanDirectives(globalHome)
		resolved.TodayMemory = r.scanTodayMemory(globalHome)
	}

	// 2. Scan Workspace Directives (Level 2: workspace prefix)
	if workspaceDir != "" && workspaceDir != globalHome {
		resolved.WorkspaceDirectives = r.scanDirectives(workspaceDir)
		if resolved.TodayMemory == "" {
			resolved.TodayMemory = r.scanTodayMemory(workspaceDir)
		}
	}

	// 3. Assemble Combined Directives Block
	var sb strings.Builder
	if resolved.GlobalDirectives != "" {
		sb.WriteString("[GLOBAL CORE DIRECTIVES]\n")
		sb.WriteString(resolved.GlobalDirectives)
		sb.WriteString("\n\n")
	}

	if resolved.WorkspaceDirectives != "" {
		sb.WriteString("[WORKSPACE PROJECT DIRECTIVES]\n")
		sb.WriteString(resolved.WorkspaceDirectives)
		sb.WriteString("\n\n")
	}

	resolved.CombinedDirectives = strings.TrimSpace(sb.String())

	// 4. Discover Skills (Progressive Disclosure - Level 3)
	skills, err := r.DiscoverSkills(ctx, globalHome, workspaceDir)
	if err == nil {
		resolved.SkillHeaders = skills
	}

	// 5. Deterministic sorting for KV-cache prefix invariance
	r.sortContext(resolved)

	return resolved, nil
}

// sortContext ensures deterministic ordering of slices for KV-cache invariance.
func (r *ContextResolver) sortContext(resolved *domain.ResolvedContext) {
	if resolved == nil {
		return
	}
	if len(resolved.SkillHeaders) > 1 {
		sort.Slice(resolved.SkillHeaders, func(i, j int) bool {
			if resolved.SkillHeaders[i].Scope != resolved.SkillHeaders[j].Scope {
				return resolved.SkillHeaders[i].Scope < resolved.SkillHeaders[j].Scope
			}
			if resolved.SkillHeaders[i].Name != resolved.SkillHeaders[j].Name {
				return resolved.SkillHeaders[i].Name < resolved.SkillHeaders[j].Name
			}
			return resolved.SkillHeaders[i].FilePath < resolved.SkillHeaders[j].FilePath
		})
	}
	if len(resolved.ActiveMCPServers) > 1 {
		sort.Slice(resolved.ActiveMCPServers, func(i, j int) bool {
			return resolved.ActiveMCPServers[i].ServerName < resolved.ActiveMCPServers[j].ServerName
		})
	}
	if len(resolved.ActivePlugins) > 1 {
		sort.Slice(resolved.ActivePlugins, func(i, j int) bool {
			return resolved.ActivePlugins[i].Manifest.Name < resolved.ActivePlugins[j].Manifest.Name
		})
	}
}

// DetectUserLocation inspects USER.md in dir to detect the user's timezone, defaulting to time.Local.
func DetectUserLocation(dir string) *time.Location {
	if dir == "" {
		return time.Local
	}
	userFile := filepath.Join(dir, "USER.md")
	safePath, err := ValidateSecurePath(dir, userFile)
	if err != nil {
		return time.Local
	}
	data, err := os.ReadFile(safePath)
	if err == nil {
		return domain.ParseLocationFromText(string(data))
	}
	return time.Local
}

// scanDirectives scans static directives: IDENTITY.md, SOUL.md, USER.md, MEMORY.md, AGENTS.md.
// Dynamic daily memory is explicitly excluded to preserve Level 0-3 KV-cache prefix invariance.
func (r *ContextResolver) scanDirectives(dir string) string {
	files := []struct {
		tag  string
		name string
	}{
		{tag: "IDENTITY", name: "IDENTITY.md"},
		{tag: "SOUL", name: "SOUL.md"},
		{tag: "USER_PROFILE", name: "USER.md"},
		{tag: "LONG_TERM_MEMORY", name: "MEMORY.md"},
		{tag: "CORE_RULES", name: "AGENTS.md"},
	}

	var sb strings.Builder
	hasContent := false

	for _, f := range files {
		targetFile := filepath.Join(dir, f.name)
		safePath, err := ValidateSecurePath(dir, targetFile)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(safePath)
		if err == nil {
			trimmed := strings.TrimSpace(string(data))
			if len(trimmed) > 0 {
				sb.WriteString(fmt.Sprintf("<%s>\n%s\n</%s>\n\n", f.tag, trimmed, f.tag))
				hasContent = true
			}
		}
	}

	if !hasContent {
		return ""
	}
	return strings.TrimSpace(sb.String())
}

// scanTodayMemory scans dynamic daily episodic memory (memory/YYYY-MM-DD.md)
// or falls back to yesterday's memory if absent (48h rolling window).
// This content is placed in Level 4 (turn dynamic scope) to avoid invalidating Gemini KV-caches.
func (r *ContextResolver) scanTodayMemory(dir string) string {
	if dir == "" {
		return ""
	}
	loc := DetectUserLocation(dir)
	now := time.Now().In(loc)
	todayStr := now.Format("2006-01-02")
	todayRel := filepath.Join("memory", fmt.Sprintf("%s.md", todayStr))

	targetFile := filepath.Join(dir, todayRel)
	if safePath, err := ValidateSecurePath(dir, targetFile); err == nil {
		if data, err := os.ReadFile(safePath); err == nil {
			trimmed := strings.TrimSpace(string(data))
			if len(trimmed) > 0 {
				return fmt.Sprintf("<TODAY_MEMORY>\n%s\n</TODAY_MEMORY>", trimmed)
			}
		}
	}

	// 48h Rolling Window: If TODAY_MEMORY is absent or empty, check YESTERDAY_MEMORY for cold-start handover
	yesterdayStr := now.AddDate(0, 0, -1).Format("2006-01-02")
	yesterdayRel := filepath.Join("memory", fmt.Sprintf("%s.md", yesterdayStr))
	targetFile = filepath.Join(dir, yesterdayRel)
	if safePath, err := ValidateSecurePath(dir, targetFile); err == nil {
		if data, err := os.ReadFile(safePath); err == nil {
			trimmed := strings.TrimSpace(string(data))
			if len(trimmed) > 0 {
				return fmt.Sprintf("<RECENT_ACTIVITY>\n%s\n</RECENT_ACTIVITY>", trimmed)
			}
		}
	}

	return ""
}

// DiscoverSkills scans both global and workspace directories for .agents/skills/ or skills/,
// parsing YAML frontmatters defensively and deduplicating by name (Workspace > Global).
func (r *ContextResolver) DiscoverSkills(ctx context.Context, globalHome string, workspaceDir string) ([]domain.SkillHeader, error) {
	skillMap := make(map[string]domain.SkillHeader)

	// 1. Scan Global Skills
	if globalHome != "" {
		globalPaths := []string{
			filepath.Join(globalHome, "skills"),
			filepath.Join(globalHome, ".agents", "skills"),
		}
		for _, p := range globalPaths {
			r.scanSkillsFromDir(p, domain.ScopeGlobal, skillMap)
		}
	}

	// 2. Scan Workspace Skills (Overrides Global)
	if workspaceDir != "" {
		wsPaths := []string{
			filepath.Join(workspaceDir, ".agents", "skills"),
			filepath.Join(workspaceDir, "skills"),
		}
		for _, p := range wsPaths {
			r.scanSkillsFromDir(p, domain.ScopeWorkspace, skillMap)
		}
	}

	result := make([]domain.SkillHeader, 0, len(skillMap))
	for _, s := range skillMap {
		result = append(result, s)
	}

	// Deterministic sorting to preserve Gemini Prefix KV-Cache invariant across turns
	sort.Slice(result, func(i, j int) bool {
		if result[i].Scope != result[j].Scope {
			return result[i].Scope < result[j].Scope
		}
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		return result[i].FilePath < result[j].FilePath
	})

	return result, nil
}

func (r *ContextResolver) scanSkillsFromDir(skillsRoot string, scope domain.ContextScope, targetMap map[string]domain.SkillHeader) {
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillFile := filepath.Join(skillsRoot, entry.Name(), "SKILL.md")
		header, err := SafeParseSkillHeader(skillFile, scope)
		if err == nil && header != nil {
			targetMap[header.Name] = *header
		}
	}
}

// ParseSkillHeaderFromBytes safely parses YAML frontmatter delimiters (---) from raw SKILL.md bytes.
func ParseSkillHeaderFromBytes(data []byte, filePath string, scope domain.ContextScope) (*domain.SkillHeader, error) {
	content := string(data)
	if !strings.HasPrefix(content, "---") {
		return nil, fmt.Errorf("missing YAML frontmatter delimiters in %s", filePath)
	}

	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("malformed frontmatter in %s", filePath)
	}

	var meta struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}

	decoder := yaml.NewDecoder(bytes.NewReader([]byte(parts[1])))
	if err := decoder.Decode(&meta); err != nil {
		return nil, fmt.Errorf("yaml syntax error in %s: %w", filePath, err)
	}

	if strings.TrimSpace(meta.Name) == "" {
		return nil, fmt.Errorf("skill in %s is missing name", filePath)
	}

	return &domain.SkillHeader{
		Name:        meta.Name,
		Description: meta.Description,
		FilePath:    filePath,
		Scope:       scope,
	}, nil
}

// SafeParseSkillHeader safely parses YAML frontmatter delimiters (---) from a SKILL.md file.
func SafeParseSkillHeader(filePath string, scope domain.ContextScope) (*domain.SkillHeader, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read skill file: %w", err)
	}
	return ParseSkillHeaderFromBytes(data, filePath, scope)
}
