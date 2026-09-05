package context_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	contextAdapter "agyent/internal/adapters/context"
	"agyent/internal/core/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContextResolver_ResolveDirectives(t *testing.T) {
	globalDir := t.TempDir()
	wsDir := t.TempDir()

	// Write global directives including USER.md with timezone and MEMORY.md
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "IDENTITY.md"), []byte("# Global Identity\nRole: Assistant"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "SOUL.md"), []byte("# Global Soul\nTone: Polite"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "USER.md"), []byte("Múi giờ: Asia/Ho_Chi_Minh\nName: Khánh"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "MEMORY.md"), []byte("# Long Term Memory\nFact: Go Clean Architecture"), 0644))

	// Create daily memory file memory/YYYY-MM-DD.md
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	todayStr := time.Now().In(loc).Format("2006-01-02")
	memDir := filepath.Join(globalDir, "memory")
	require.NoError(t, os.MkdirAll(memDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(memDir, fmt.Sprintf("%s.md", todayStr)), []byte("- [10:00] Daily standup meeting"), 0644))

	// Write workspace directives
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "AGENTS.md"), []byte("# Project Rules\n- Clean Architecture Go"), 0644))

	resolver := contextAdapter.NewContextResolver()
	ctx := context.Background()

	res, err := resolver.Resolve(ctx, globalDir, wsDir)
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Contains(t, res.GlobalDirectives, "<IDENTITY>")
	assert.Contains(t, res.GlobalDirectives, "Role: Assistant")
	assert.Contains(t, res.GlobalDirectives, "<SOUL>")
	assert.Contains(t, res.GlobalDirectives, "<USER_PROFILE>")
	assert.Contains(t, res.GlobalDirectives, "Múi giờ: Asia/Ho_Chi_Minh")
	assert.Contains(t, res.GlobalDirectives, "<LONG_TERM_MEMORY>")
	assert.Contains(t, res.GlobalDirectives, "Fact: Go Clean Architecture")
	assert.NotContains(t, res.GlobalDirectives, "<TODAY_MEMORY>")
	assert.Contains(t, res.TodayMemory, "<TODAY_MEMORY>")
	assert.Contains(t, res.TodayMemory, "[10:00] Daily standup meeting")
	assert.Contains(t, res.WorkspaceDirectives, "<CORE_RULES>")
	assert.Contains(t, res.WorkspaceDirectives, "Clean Architecture Go")

	assert.Contains(t, res.CombinedDirectives, "[GLOBAL CORE DIRECTIVES]")
	assert.Contains(t, res.CombinedDirectives, "[WORKSPACE PROJECT DIRECTIVES]")
}

func TestTimezoneParsingAndDetection(t *testing.T) {
	// 1. Standard IANA Timezone in text
	loc1 := domain.ParseLocationFromText("User timezone is Asia/Ho_Chi_Minh.")
	assert.Equal(t, "Asia/Ho_Chi_Minh", loc1.String())

	loc2 := domain.ParseLocationFromText("Timezone: America/New_York (EST/EDT)")
	assert.Equal(t, "America/New_York", loc2.String())

	// 2. UTC Offset formats
	loc3 := domain.ParseLocationFromText("Timezone: UTC+7")
	_, offset3 := time.Now().In(loc3).Zone()
	assert.Equal(t, 7*3600, offset3)

	loc4 := domain.ParseLocationFromText("Offset: UTC-05:00")
	_, offset4 := time.Now().In(loc4).Zone()
	assert.Equal(t, -5*3600, offset4)

	// 3. Fallback when invalid / empty
	loc5 := domain.ParseLocationFromText("No timezone here")
	assert.Equal(t, time.Local, loc5)

	loc6 := domain.ParseLocationFromText("")
	assert.Equal(t, time.Local, loc6)

	// 4. Test DetectUserLocation with file
	tempDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "USER.md"), []byte("Timezone: Europe/London"), 0644))
	locDir := contextAdapter.DetectUserLocation(tempDir)
	assert.Equal(t, "Europe/London", locDir.String())

	// Empty dir fallback
	assert.Equal(t, time.Local, contextAdapter.DetectUserLocation(""))
}

func TestContextResolver_MissingDailyMemory_GracefulFallback(t *testing.T) {
	globalDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "IDENTITY.md"), []byte("Role: Assistant"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "MEMORY.md"), []byte("Long term memory only"), 0644))

	resolver := contextAdapter.NewContextResolver()
	res, err := resolver.Resolve(context.Background(), globalDir, "")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Contains(t, res.GlobalDirectives, "<LONG_TERM_MEMORY>")
	assert.NotContains(t, res.GlobalDirectives, "<TODAY_MEMORY>", "Missing daily memory file must be gracefully skipped without error or empty tag")
}

func TestContextResolver_DiscoverSkills_Deduplication(t *testing.T) {
	globalDir := t.TempDir()
	wsDir := t.TempDir()

	// 1. Global skill: "deploy"
	globalSkillDir := filepath.Join(globalDir, ".agents", "skills", "deploy")
	require.NoError(t, os.MkdirAll(globalSkillDir, 0755))
	globalSkillContent := "---\nname: deploy\ndescription: Global generic deployment\n---\n# Deploy Global"
	require.NoError(t, os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(globalSkillContent), 0644))

	// 2. Workspace skill overriding "deploy"
	wsSkillDir := filepath.Join(wsDir, ".agents", "skills", "deploy")
	require.NoError(t, os.MkdirAll(wsSkillDir, 0755))
	wsSkillContent := "---\nname: deploy\ndescription: Project specific K8s deploy\n---\n# Deploy K8s"
	require.NoError(t, os.WriteFile(filepath.Join(wsSkillDir, "SKILL.md"), []byte(wsSkillContent), 0644))

	// 3. Unique workspace skill: "db-migrate"
	wsMigrateDir := filepath.Join(wsDir, ".agents", "skills", "db-migrate")
	require.NoError(t, os.MkdirAll(wsMigrateDir, 0755))
	wsMigrateContent := "---\nname: db-migrate\ndescription: SQLite migration runner\n---\n# Migration"
	require.NoError(t, os.WriteFile(filepath.Join(wsMigrateDir, "SKILL.md"), []byte(wsMigrateContent), 0644))

	resolver := contextAdapter.NewContextResolver()
	ctx := context.Background()

	skills, err := resolver.DiscoverSkills(ctx, globalDir, wsDir)
	require.NoError(t, err)
	assert.Len(t, skills, 2)

	skillMap := make(map[string]domain.SkillHeader)
	for _, s := range skills {
		skillMap[s.Name] = s
	}

	// Verify "deploy" was overridden by Workspace
	assert.Equal(t, "Project specific K8s deploy", skillMap["deploy"].Description)
	assert.Equal(t, domain.ScopeWorkspace, skillMap["deploy"].Scope)

	// Verify "db-migrate" exists
	assert.Equal(t, "SQLite migration runner", skillMap["db-migrate"].Description)
	assert.Equal(t, domain.ScopeWorkspace, skillMap["db-migrate"].Scope)
}

func TestContextResolver_PathTraversalProtection(t *testing.T) {
	rootDir := t.TempDir()

	// Valid path inside root
	validPath := filepath.Join(rootDir, "valid.txt")
	require.NoError(t, os.WriteFile(validPath, []byte("ok"), 0644))
	safe, err := contextAdapter.ValidateSecurePath(rootDir, validPath)
	require.NoError(t, err)
	assert.NotEmpty(t, safe)

	// Malicious relative path trying to escape root
	maliciousPath := filepath.Join(rootDir, "..", "..", "windows", "system32")
	_, err = contextAdapter.ValidateSecurePath(rootDir, maliciousPath)
	assert.Error(t, err, "path traversal escaping root must be rejected")
}

func TestContextResolver_TimezoneBoundaries(t *testing.T) {
	// Test boundary UTC 2026-08-25 18:00:00 is 2026-08-26 01:00:00 in UTC+7 (Asia/Ho_Chi_Minh)
	utcTime := time.Date(2026, 8, 25, 18, 0, 0, 0, time.UTC)

	locVn, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	require.NoError(t, err)
	vnTime := utcTime.In(locVn)
	assert.Equal(t, "2026-08-26", vnTime.Format("2006-01-02"))
	assert.Equal(t, "2026-08-25", utcTime.Format("2006-01-02"))

	// Test boundary UTC 2026-08-25 03:00:00 is 2026-08-24 23:00:00 in UTC-4 (America/New_York EDT)
	locNy, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	nyTime := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC).In(locNy)
	assert.Equal(t, "2026-08-24", nyTime.Format("2006-01-02"))

	// Test max offset UTC+14 (e.g. Line Islands / Kiribati)
	loc14 := domain.ParseLocationFromText("Timezone: UTC+14")
	time14 := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC).In(loc14)
	assert.Equal(t, "2026-08-26", time14.Format("2006-01-02"))
}

func TestContextResolver_WhitespaceOnlyFiles_Omitted(t *testing.T) {
	globalDir := t.TempDir()
	// Write whitespace-only files
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "IDENTITY.md"), []byte("   \n\t\r\n  "), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "SOUL.md"), []byte(""), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "USER.md"), []byte("   \n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "MEMORY.md"), []byte("\t\t\n"), 0644))

	resolver := contextAdapter.NewContextResolver()
	res, err := resolver.Resolve(context.Background(), globalDir, "")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Empty(t, res.GlobalDirectives, "Whitespace-only files must not generate empty XML tags")
	assert.Empty(t, res.CombinedDirectives)
}

func TestContextResolver_UnicodeAndSpecialPaths(t *testing.T) {
	rootDir := t.TempDir()
	unicodeSubdir := filepath.Join(rootDir, "dự_án_mới", "hội_thoại_2026")
	require.NoError(t, os.MkdirAll(unicodeSubdir, 0755))

	agentsFile := filepath.Join(unicodeSubdir, "AGENTS.md")
	require.NoError(t, os.WriteFile(agentsFile, []byte("# Quy tắc dự án tiếng Việt\n- Chuẩn hóa Unicode UTF-8"), 0644))

	safePath, err := contextAdapter.ValidateSecurePath(rootDir, agentsFile)
	require.NoError(t, err)
	assert.NotEmpty(t, safePath)

	resolver := contextAdapter.NewContextResolver()
	res, err := resolver.Resolve(context.Background(), "", unicodeSubdir)
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Contains(t, res.WorkspaceDirectives, "<CORE_RULES>")
	assert.Contains(t, res.WorkspaceDirectives, "Chuẩn hóa Unicode UTF-8")
}
