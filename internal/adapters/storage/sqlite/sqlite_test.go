package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agyent/internal/adapters/storage/sqlite"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupIsolatedStore creates a completely isolated SQLiteStore for testing using t.TempDir().
func setupIsolatedStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), fmt.Sprintf("test_%d.db", time.Now().UnixNano()))
	store, err := sqlite.Open(dbPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = store.Close()
	})

	return store
}

// 1. SessionRepository & Multi-Project Context Isolation (TC-SESS-01..07)
func TestSQLiteStore_SessionCRUD_And_DualScopeIsolation(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	// Seed agent first
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:          "dev_expert",
		Status:        domain.StatusInitialized,
		WorkspacePath: "/data/dev",
	}))

	t.Run("TC-SESS-01: Get Non-existent Session", func(t *testing.T) {
		sess, err := store.GetSession(ctx, "telegram:non_existent")
		assert.Nil(t, sess)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ports.ErrNotFound))
		assert.True(t, errors.Is(err, ports.ErrSessionNotFound))
	})

	t.Run("TC-SESS-02 & 03: GetOrCreateSession New vs Existing", func(t *testing.T) {
		key := "telegram:user_123"
		sess1, err := store.GetOrCreateSession(ctx, key, "dev_expert")
		require.NoError(t, err)
		assert.Equal(t, key, sess1.SessionKey)
		assert.Equal(t, "dev_expert", sess1.ActiveAgent)
		assert.Equal(t, "", sess1.ActiveProject)

		// Subsequent call must retrieve existing session without resetting active_agent
		sess2, err := store.GetOrCreateSession(ctx, key, "other_agent")
		require.NoError(t, err)
		assert.Equal(t, "dev_expert", sess2.ActiveAgent)
	})

	t.Run("TC-SESS-04: Save and Update Session", func(t *testing.T) {
		key := "telegram:user_update"
		sess := &domain.Session{
			SessionKey:           key,
			ActiveAgent:          "dev_expert",
			ActiveProject:        "ecommerce",
			GlobalConversationID: "conv_global_1",
			UpdatedAt:            time.Now(),
		}
		require.NoError(t, store.SaveSession(ctx, sess))

		retrieved, err := store.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "ecommerce", retrieved.ActiveProject)
		assert.Equal(t, "conv_global_1", retrieved.GlobalConversationID)

		// Update fields
		retrieved.ActiveProject = "scraper"
		retrieved.GlobalConversationID = "conv_global_2"
		require.NoError(t, store.SaveSession(ctx, retrieved))

		updated, err := store.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, "scraper", updated.ActiveProject)
		assert.Equal(t, "conv_global_2", updated.GlobalConversationID)
	})

	t.Run("TC-SESS-05 & 06: Dual-Scope Project Conversation Isolation and Clear", func(t *testing.T) {
		key := "telegram:dual_scope"
		require.NoError(t, store.SaveSession(ctx, &domain.Session{
			SessionKey:  key,
			ActiveAgent: "dev_expert",
		}))

		// Seed projects
		require.NoError(t, store.SaveProject(ctx, &domain.Project{
			ID:          "dev_expert:ecom",
			AgentName:   "dev_expert",
			ProjectName: "ecom",
			ProjectPath: "/repo/ecom",
		}))
		require.NoError(t, store.SaveProject(ctx, &domain.Project{
			ID:          "dev_expert:scrap",
			AgentName:   "dev_expert",
			ProjectName: "scrap",
			ProjectPath: "/repo/scrap",
		}))

		// Set conversation IDs for two distinct projects
		require.NoError(t, store.SetProjectConversationID(ctx, key, "dev_expert:ecom", "conv_ecom_101"))
		require.NoError(t, store.SetProjectConversationID(ctx, key, "dev_expert:scrap", "conv_scrap_202"))

		// Verify independence
		cidEcom, err := store.GetProjectConversationID(ctx, key, "dev_expert:ecom")
		require.NoError(t, err)
		assert.Equal(t, "conv_ecom_101", cidEcom)

		cidScrap, err := store.GetProjectConversationID(ctx, key, "dev_expert:scrap")
		require.NoError(t, err)
		assert.Equal(t, "conv_scrap_202", cidScrap)

		// Clear only ecom project conversation
		require.NoError(t, store.ClearProjectConversationID(ctx, key, "dev_expert:ecom"))

		cidEcomAfter, err := store.GetProjectConversationID(ctx, key, "dev_expert:ecom")
		require.NoError(t, err)
		assert.Equal(t, "", cidEcomAfter)

		// scrap conversation must remain unaffected
		cidScrapAfter, err := store.GetProjectConversationID(ctx, key, "dev_expert:scrap")
		require.NoError(t, err)
		assert.Equal(t, "conv_scrap_202", cidScrapAfter)
	})

	t.Run("TC-SESS-06b: Auto Project Conversation Sync via SaveSession", func(t *testing.T) {
		autoSyncKey := "telegram:auto_sync"
		autoSess := &domain.Session{
			SessionKey:            autoSyncKey,
			ActiveAgent:           "dev_expert",
			ActiveProject:         "ecom",
			GlobalConversationID:  "conv_global_auto",
			ProjectConversationID: "conv_proj_auto_synced",
		}
		require.NoError(t, store.SaveSession(ctx, autoSess))

		loadedSess, err := store.GetSession(ctx, autoSyncKey)
		require.NoError(t, err)
		assert.Equal(t, "conv_proj_auto_synced", loadedSess.ProjectConversationID)
		assert.Equal(t, "conv_global_auto", loadedSess.GlobalConversationID)

		// Test clearing project conversation ID via SaveSession
		autoSess.ProjectConversationID = ""
		require.NoError(t, store.SaveSession(ctx, autoSess))

		clearedSess, err := store.GetSession(ctx, autoSyncKey)
		require.NoError(t, err)
		assert.Equal(t, "", clearedSess.ProjectConversationID, "ProjectConversationID must be cleared in SQLite")
	})

	t.Run("TC-SESS-07: Delete Session with CASCADE and Not Found Check", func(t *testing.T) {
		key := "telegram:to_delete"
		require.NoError(t, store.SaveSession(ctx, &domain.Session{
			SessionKey:  key,
			ActiveAgent: "dev_expert",
		}))
		require.NoError(t, store.SetProjectConversationID(ctx, key, "dev_expert:ecom", "conv_temp"))

		require.NoError(t, store.DeleteSession(ctx, key))

		_, err := store.GetSession(ctx, key)
		assert.True(t, errors.Is(err, ports.ErrNotFound))

		// Project conversation should also be deleted via cascade
		cid, err := store.GetProjectConversationID(ctx, key, "dev_expert:ecom")
		require.NoError(t, err)
		assert.Equal(t, "", cid)

		// Deleting non-existent session must return ErrSessionNotFound
		err = store.DeleteSession(ctx, "telegram:non_existent_to_del")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ports.ErrSessionNotFound))
	})
}

// 2. AgentRepository Tests (TC-AGT-01..05)
func TestSQLiteStore_AgentRepository(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	t.Run("TC-AGT-01 & 02: Save, Get, and Update Lifecycle Status", func(t *testing.T) {
		agent := &domain.Agent{
			Name:          "coder",
			Description:   "Coding assistant",
			Status:        domain.StatusUninitialized,
			WorkspacePath: "/home/coder",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		require.NoError(t, store.SaveAgent(ctx, agent))

		retrieved, err := store.GetAgent(ctx, "coder")
		require.NoError(t, err)
		assert.Equal(t, "coder", retrieved.Name)
		assert.False(t, retrieved.IsInitialized())

		// Transition status
		retrieved.Status = domain.StatusInitialized
		retrieved.Description = "Initialized coding assistant"
		require.NoError(t, store.SaveAgent(ctx, retrieved))

		updated, err := store.GetAgent(ctx, "coder")
		require.NoError(t, err)
		assert.True(t, updated.IsInitialized())
		assert.Equal(t, "Initialized coding assistant", updated.Description)
	})

	t.Run("TC-AGT-03: List Agents", func(t *testing.T) {
		require.NoError(t, store.SaveAgent(ctx, &domain.Agent{Name: "agent_a", Status: domain.StatusInitialized, WorkspacePath: "/a"}))
		require.NoError(t, store.SaveAgent(ctx, &domain.Agent{Name: "agent_b", Status: domain.StatusInitialized, WorkspacePath: "/b"}))

		agents, err := store.ListAgents(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(agents), 2)
	})

	t.Run("TC-AGT-04: Get Non-existent Agent", func(t *testing.T) {
		_, err := store.GetAgent(ctx, "ghost_agent")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ports.ErrNotFound))
		assert.True(t, errors.Is(err, ports.ErrAgentNotFound))
	})
}

// 3. ProjectRepository Tests (TC-PRJ-01..04)
func TestSQLiteStore_ProjectRepository(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	// Seed agents
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{Name: "dev", Status: domain.StatusInitialized, WorkspacePath: "/dev"}))
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{Name: "ops", Status: domain.StatusInitialized, WorkspacePath: "/ops"}))

	t.Run("TC-PRJ-01 & 02: Save, Get, and List by Agent", func(t *testing.T) {
		p1 := &domain.Project{
			ID:          "dev:frontend",
			AgentName:   "dev",
			ProjectName: "frontend",
			ProjectPath: "/src/frontend",
			CreatedAt:   time.Now(),
		}
		p2 := &domain.Project{
			ID:          "dev:backend",
			AgentName:   "dev",
			ProjectName: "backend",
			ProjectPath: "/src/backend",
			CreatedAt:   time.Now(),
		}
		p3 := &domain.Project{
			ID:          "ops:infra",
			AgentName:   "ops",
			ProjectName: "infra",
			ProjectPath: "/src/infra",
			CreatedAt:   time.Now(),
		}

		require.NoError(t, store.SaveProject(ctx, p1))
		require.NoError(t, store.SaveProject(ctx, p2))
		require.NoError(t, store.SaveProject(ctx, p3))

		retrieved, err := store.GetProject(ctx, "dev:frontend")
		require.NoError(t, err)
		assert.Equal(t, "/src/frontend", retrieved.ProjectPath)

		// List for dev
		devProjects, err := store.ListProjects(ctx, "dev")
		require.NoError(t, err)
		assert.Len(t, devProjects, 2)

		// List all
		allProjects, err := store.ListProjects(ctx, "")
		require.NoError(t, err)
		assert.Len(t, allProjects, 3)
	})

	t.Run("TC-PRJ-03: Get Non-existent Project", func(t *testing.T) {
		_, err := store.GetProject(ctx, "dev:missing")
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ports.ErrNotFound))
		assert.True(t, errors.Is(err, ports.ErrProjectNotFound))
	})

	t.Run("TC-PRJ-04: Delete Project", func(t *testing.T) {
		require.NoError(t, store.DeleteProject(ctx, "dev:frontend"))
		_, err := store.GetProject(ctx, "dev:frontend")
		assert.True(t, errors.Is(err, ports.ErrNotFound))
	})
}

// 4. UserRepository & GroupRepository Tests (TC-USR-01..05)
func TestSQLiteStore_UserRepository_And_GroupRepository(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	t.Run("TC-USR-01..03: User CRUD & Whitelist Check", func(t *testing.T) {
		user := &domain.User{
			ID:        "123456789",
			Username:  "stevan",
			FullName:  "Stevan Nguyen",
			Role:      "admin",
			CreatedAt: time.Now(),
		}
		require.NoError(t, store.SaveUser(ctx, user))

		retrieved, err := store.GetUser(ctx, "123456789")
		require.NoError(t, err)
		assert.Equal(t, "Stevan Nguyen", retrieved.FullName)
		assert.Equal(t, "admin", retrieved.Role)

		allowed, err := store.IsUserAllowed(ctx, "123456789")
		require.NoError(t, err)
		assert.True(t, allowed)

		notAllowed, err := store.IsUserAllowed(ctx, "999999999")
		require.NoError(t, err)
		assert.False(t, notAllowed)

		users, err := store.ListUsers(ctx)
		require.NoError(t, err)
		assert.Len(t, users, 1)

		require.NoError(t, store.DeleteUser(ctx, "123456789"))
		_, err = store.GetUser(ctx, "123456789")
		assert.True(t, errors.Is(err, ports.ErrNotFound))
	})

	t.Run("TC-USR-04 & 05: Group CRUD & IsGroupAllowed Check", func(t *testing.T) {
		gActive := &domain.Group{
			GroupID:    "-100111",
			GroupTitle: "Active Group",
			IsActive:   true,
			CreatedAt:  time.Now(),
		}
		gInactive := &domain.Group{
			GroupID:    "-100222",
			GroupTitle: "Inactive Group",
			IsActive:   false,
			CreatedAt:  time.Now(),
		}
		require.NoError(t, store.SaveGroup(ctx, gActive))
		require.NoError(t, store.SaveGroup(ctx, gInactive))

		allowed, err := store.IsGroupAllowed(ctx, "-100111")
		require.NoError(t, err)
		assert.True(t, allowed)

		allowedInactive, err := store.IsGroupAllowed(ctx, "-100222")
		require.NoError(t, err)
		assert.False(t, allowedInactive)

		groups, err := store.ListGroups(ctx)
		require.NoError(t, err)
		assert.Len(t, groups, 2)

		require.NoError(t, store.DeleteGroup(ctx, "-100111"))
		_, err = store.GetGroup(ctx, "-100111")
		assert.True(t, errors.Is(err, ports.ErrNotFound))
	})
}

// 5. AuditRepository Tests (TC-AUD-01..03)
func TestSQLiteStore_AuditRepository(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	t.Run("TC-AUD-01..03: Log Audit, Pagination, and Descending Ordering", func(t *testing.T) {
		sessionKey := "telegram:audit_test"
		for i := 1; i <= 5; i++ {
			entry := &domain.AuditLog{
				SessionKey:      sessionKey,
				AgentName:       "dev_expert",
				ProjectName:     "ecommerce",
				ConversationID:  fmt.Sprintf("conv_%d", i),
				PromptLength:    100 * i,
				ResponseLength:  200 * i,
				DurationSeconds: float64(i) * 0.5,
				Usage: domain.TokenUsage{
					InputTokens:     50 * i,
					OutputTokens:    150 * i,
					ThinkingTokens:  10 * i,
					CacheReadTokens: 30 * i,
					TotalTokens:     210 * i,
				},
				Status:       "SUCCESS",
				ErrorMessage: "",
				CreatedAt:    time.Now().Add(time.Duration(i) * time.Millisecond),
			}
			require.NoError(t, store.LogAudit(ctx, entry))
			assert.Greater(t, entry.ID, int64(0))
		}

		// Fetch limit 3
		logs, err := store.ListAuditLogs(ctx, sessionKey, 3)
		require.NoError(t, err)
		require.Len(t, logs, 3)

		// Must be descending by created_at (conv_5, conv_4, conv_3)
		assert.Equal(t, "conv_5", logs[0].ConversationID)
		assert.Equal(t, 150, logs[0].Usage.CacheReadTokens)
		assert.Equal(t, "conv_4", logs[1].ConversationID)
		assert.Equal(t, "conv_3", logs[2].ConversationID)

		// Test GetTokenStats
		convStats, err := store.GetTokenStats(ctx, sessionKey, "conv_5")
		require.NoError(t, err)
		assert.Equal(t, 250, convStats.InputTokens)
		assert.Equal(t, 150, convStats.CacheReadTokens)
		assert.Equal(t, 60.0, convStats.CacheHitRatio())
		assert.Equal(t, 100, convStats.UncachedInputTokens())

		sessionStats, err := store.GetTokenStats(ctx, sessionKey, "")
		require.NoError(t, err)
		// Sum 1..5 of 50*i = 750
		assert.Equal(t, 750, sessionStats.InputTokens)
		// Sum 1..5 of 30*i = 450
		assert.Equal(t, 450, sessionStats.CacheReadTokens)
		assert.Equal(t, 60.0, sessionStats.CacheHitRatio())
	})
}

// 6. Foreign Key Constraints (CASCADE & RESTRICT)
func TestSQLiteStore_ForeignKeys_Enforcement(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	// 1. Insert orphan project (must fail FK)
	orphanProject := &domain.Project{
		ID:          "ghost:proj",
		AgentName:   "ghost",
		ProjectName: "proj",
		ProjectPath: "/tmp",
	}
	err := store.SaveProject(ctx, orphanProject)
	assert.Error(t, err, "Orphan project insert must fail foreign key constraint")

	// 2. Create valid agent
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:          "dev_master",
		Status:        domain.StatusInitialized,
		WorkspacePath: "/tmp/dev",
	}))

	// 3. Test ON DELETE RESTRICT: create session on dev_master
	sess := &domain.Session{SessionKey: "telegram:locked_sess", ActiveAgent: "dev_master"}
	require.NoError(t, store.SaveSession(ctx, sess))

	// Deleting dev_master while session is active must fail
	err = store.DeleteAgent(ctx, "dev_master")
	assert.Error(t, err, "Deleting agent with active session must fail due to ON DELETE RESTRICT")

	// 4. Test ON DELETE CASCADE: delete session, add project and delete agent
	require.NoError(t, store.DeleteSession(ctx, "telegram:locked_sess"))
	proj := &domain.Project{
		ID:          "dev_master:project_x",
		AgentName:   "dev_master",
		ProjectName: "project_x",
		ProjectPath: "/tmp/x",
	}
	require.NoError(t, store.SaveProject(ctx, proj))

	// Deleting agent now should cascade delete project_x
	require.NoError(t, store.DeleteAgent(ctx, "dev_master"))
	_, err = store.GetProject(ctx, "dev_master:project_x")
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

// 7. SQL Injection Immunity
func TestSQLiteStore_SQLInjection_Immunity(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:          "legit_agent",
		Status:        domain.StatusInitialized,
		WorkspacePath: "/tmp",
	}))

	payloads := []string{
		"telegram:' OR '1'='1' --",
		"telegram:123'; DROP TABLE users; --",
		"telegram:123' UNION SELECT * FROM allowed_groups --",
		`telegram:123"'; \ / * % _ @ # $ ^ & ()`,
	}

	for _, payload := range payloads {
		sess := &domain.Session{
			SessionKey:    payload,
			ActiveAgent:   "legit_agent",
			ActiveProject: "normal_proj",
		}
		require.NoError(t, store.SaveSession(ctx, sess))

		retrieved, err := store.GetSession(ctx, payload)
		require.NoError(t, err)
		assert.Equal(t, payload, retrieved.SessionKey)
	}

	// Verify users table wasn't dropped
	users, err := store.ListUsers(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, users)
}

// 8. Unicode & Vietnamese Diacritics
func TestSQLiteStore_UnicodeAndDiacritics(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	user := &domain.User{
		ID:        "user_vn_001",
		Username:  "nguyen_hoang_hai",
		FullName:  "Nguyễn Hoàng Hải 🚀 (Trưởng nhóm R&D)",
		Role:      "admin",
		CreatedAt: time.Now(),
	}
	require.NoError(t, store.SaveUser(ctx, user))

	retrieved, err := store.GetUser(ctx, "user_vn_001")
	require.NoError(t, err)
	assert.Equal(t, "Nguyễn Hoàng Hải 🚀 (Trưởng nhóm R&D)", retrieved.FullName)

	audit := &domain.AuditLog{
		SessionKey:      "telegram:topic_tiếng_việt:99",
		AgentName:       "dev_expert",
		ProjectName:     "Dự Án Thương Mại Điện Tử 🛒",
		ConversationID:  "conv-việt-nam-2026-🔥",
		PromptLength:    1200,
		ResponseLength:  2500,
		DurationSeconds: 2.345,
		Status:          "SUCCESS",
		ErrorMessage:    "Không có lỗi nào phát sinh ✨",
		CreatedAt:       time.Now(),
	}
	require.NoError(t, store.LogAudit(ctx, audit))

	logs, err := store.ListAuditLogs(ctx, "telegram:topic_tiếng_việt:99", 10)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	assert.Equal(t, "Dự Án Thương Mại Điện Tử 🛒", logs[0].ProjectName)
}

// 9. High-Concurrency Stress Test (100 Goroutines under -race)
func TestSQLiteStore_HighConcurrency_100Workers_Race(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	// Pre-seed agent, projects, and sessions so foreign key constraints are satisfied during concurrent testing
	require.NoError(t, store.SaveAgent(ctx, &domain.Agent{
		Name:          "stress_agent",
		Status:        domain.StatusInitialized,
		WorkspacePath: "/tmp",
	}))

	for p := 0; p < 3; p++ {
		require.NoError(t, store.SaveProject(ctx, &domain.Project{
			ID:          fmt.Sprintf("stress_agent:project_%d", p),
			AgentName:   "stress_agent",
			ProjectName: fmt.Sprintf("project_%d", p),
			ProjectPath: fmt.Sprintf("/tmp/project_%d", p),
		}))
	}

	for s := 0; s < 10; s++ {
		require.NoError(t, store.SaveSession(ctx, &domain.Session{
			SessionKey:  fmt.Sprintf("telegram:stress_%d", s),
			ActiveAgent: "stress_agent",
		}))
	}

	const numWorkers = 100
	const opsPerWorker = 20
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	var errorCount atomic.Int64
	startSignal := make(chan struct{})

	for i := 0; i < numWorkers; i++ {
		workerID := i
		go func() {
			defer wg.Done()
			<-startSignal

			sessionKey := fmt.Sprintf("telegram:stress_%d", workerID%10)

			for j := 0; j < opsPerWorker; j++ {
				opType := (workerID + j) % 4
				switch opType {
				case 0:
					sess := &domain.Session{
						SessionKey:    sessionKey,
						ActiveAgent:   "stress_agent",
						ActiveProject: fmt.Sprintf("project_%d", j%3),
						UpdatedAt:     time.Now(),
					}
					if err := store.SaveSession(ctx, sess); err != nil {
						errorCount.Add(1)
					}
				case 1:
					if err := store.SetProjectConversationID(ctx, sessionKey, fmt.Sprintf("stress_agent:project_%d", j%3), fmt.Sprintf("conv_%d_%d", workerID, j)); err != nil {
						errorCount.Add(1)
					}
				case 2:
					audit := &domain.AuditLog{
						SessionKey:      sessionKey,
						AgentName:       "stress_agent",
						PromptLength:    100,
						ResponseLength:  200,
						DurationSeconds: 0.5,
						Status:          "SUCCESS",
						CreatedAt:       time.Now(),
					}
					if err := store.LogAudit(ctx, audit); err != nil {
						errorCount.Add(1)
					}
				case 3:
					if _, err := store.GetSession(ctx, sessionKey); err != nil && !errors.Is(err, ports.ErrNotFound) {
						errorCount.Add(1)
					}
				}
			}
		}()
	}

	close(startSignal)
	wg.Wait()

	assert.Equal(t, int64(0), errorCount.Load(), "Must complete 2,000 concurrent SQLite operations with 0 errors and 0 SQLITE_BUSY")
}

// 10. Migration Idempotency & Rollback Tests
func TestMigrations_IdempotencyAndRollback(t *testing.T) {
	t.Run("Migration Idempotency", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "migrate_idempotent.db")
		store, err := sqlite.Open(dbPath)
		require.NoError(t, err)
		defer store.Close()

		err = store.Migrate(context.Background())
		assert.NoError(t, err, "Calling Migrate() repeatedly on initialized DB must be a no-op without error")
	})

	t.Run("Transactional Rollback on Malformed SQL", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "migrate_rollback.db")
		db, err := sql.Open("sqlite", dbPath)
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`)
		require.NoError(t, err)

		badMigrationScript := `
			CREATE TABLE step_one_valid (id TEXT PRIMARY KEY);
			CREATE THIS IS INVALID SQL SYNTAX;
		`

		tx, err := db.BeginTx(context.Background(), nil)
		require.NoError(t, err)

		_, err = tx.Exec(badMigrationScript)
		assert.Error(t, err)
		_ = tx.Rollback()

		var tableName string
		err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='step_one_valid'").Scan(&tableName)
		assert.Equal(t, sql.ErrNoRows, err, "Partial tables from failed migration must be completely rolled back")
	})

	t.Run("Invalid DSN and Unwritable Path", func(t *testing.T) {
		// Use invalid path containing null character or illegal characters
		_, err := sqlite.Open("file:///invalid/path\x00/impossible.db")
		assert.Error(t, err)
	})
}

// 11. Edge Cases, Null Handling, and Coverage Boost
func TestSQLiteStore_EdgeCasesAndHelpers(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	t.Run("Nil Entity Checks", func(t *testing.T) {
		assert.Error(t, store.SaveSession(ctx, nil))
		assert.Error(t, store.SaveAgent(ctx, nil))
		assert.Error(t, store.SaveProject(ctx, nil))
		assert.Error(t, store.SaveUser(ctx, nil))
		assert.Error(t, store.SaveGroup(ctx, nil))
		assert.Error(t, store.LogAudit(ctx, nil))
	})

	t.Run("Delete Non-existent Entities", func(t *testing.T) {
		assert.Error(t, store.DeleteAgent(ctx, "non_existent_agent_999"))
		assert.Error(t, store.DeleteProject(ctx, "non_existent_proj_999"))
		assert.Error(t, store.DeleteUser(ctx, "non_existent_user_999"))
		assert.Error(t, store.DeleteGroup(ctx, "non_existent_group_999"))
	})

	t.Run("Audit Log Default Values and Limit", func(t *testing.T) {
		// Log with empty status, zero created_at
		audit := &domain.AuditLog{
			SessionKey: "telegram:defaults",
			AgentName:  "dev",
		}
		require.NoError(t, store.LogAudit(ctx, audit))

		// List with limit <= 0
		logs, err := store.ListAuditLogs(ctx, "telegram:defaults", 0)
		require.NoError(t, err)
		assert.Len(t, logs, 1)
	})

	t.Run("Project Default ID Formatting", func(t *testing.T) {
		require.NoError(t, store.SaveAgent(ctx, &domain.Agent{Name: "formatter_agent", Status: domain.StatusInitialized, WorkspacePath: "/f"}))
		proj := &domain.Project{
			AgentName:   "formatter_agent",
			ProjectName: "auto_id_proj",
			ProjectPath: "/auto",
		}
		require.NoError(t, store.SaveProject(ctx, proj))
		assert.Equal(t, "formatter_agent:auto_id_proj", proj.ID)
	})

	t.Run("User and Group Default Values", func(t *testing.T) {
		user := &domain.User{
			ID: "default_user_1",
		}
		require.NoError(t, store.SaveUser(ctx, user))
		retrievedUser, err := store.GetUser(ctx, "default_user_1")
		require.NoError(t, err)
		assert.Equal(t, "admin", retrievedUser.Role)

		group := &domain.Group{
			GroupID: "-100default",
		}
		require.NoError(t, store.SaveGroup(ctx, group))
		retrievedGroup, err := store.GetGroup(ctx, "-100default")
		require.NoError(t, err)
		assert.False(t, retrievedGroup.IsActive)
	})

	t.Run("BuildDSN Edge Cases and Getters", func(t *testing.T) {
		dsnMem := sqlite.BuildDSN(":memory:")
		assert.Contains(t, dsnMem, "memory:")

		dsnEmpty := sqlite.BuildDSN("")
		assert.Contains(t, dsnEmpty, "memory:")

		dsnFile := sqlite.BuildDSN("file:/tmp/custom.db?_pragma=busy_timeout(1000)")
		assert.Equal(t, "file:/tmp/custom.db?_pragma=busy_timeout(1000)", dsnFile)

		assert.NotNil(t, store.DB())
		assert.NotNil(t, store.LockManager())

		// Test memory Open
		memStore, err := sqlite.Open(":memory:")
		require.NoError(t, err)
		assert.NoError(t, memStore.Close())
	})
}

// Test FlexTime parsing RFC3339 string timestamps vs integer timestamps across all tables
func TestSQLiteStore_FlexTime_StringAndNumericTimestamps(t *testing.T) {
	store := setupIsolatedStore(t)
	ctx := context.Background()

	// 1. Manually insert RFC3339 timestamp strings into agents table (as reported in bug report)
	_, err := store.DB().ExecContext(ctx, `
		INSERT INTO agents (name, description, status, workspace_path, created_at, updated_at)
		VALUES ('legacy_string_agent', 'Legacy Agent', 'initialized', '/data/legacy', '2026-08-25T07:21:11.172945+00:00', '2026-08-25T07:21:11.172945+00:00')
	`)
	require.NoError(t, err)

	// Verify GetAgent parses string timestamp without scan error
	agent, err := store.GetAgent(ctx, "legacy_string_agent")
	require.NoError(t, err)
	assert.Equal(t, "legacy_string_agent", agent.Name)
	assert.False(t, agent.CreatedAt.IsZero())
	assert.Equal(t, 2026, agent.CreatedAt.Year())

	// Verify ListAgents parses string timestamp without scan error
	agents, err := store.ListAgents(ctx)
	require.NoError(t, err)
	assert.Len(t, agents, 1)
	assert.Equal(t, "legacy_string_agent", agents[0].Name)

	// 2. Manually insert RFC3339 timestamps into projects, users, allowed_groups, audit_logs
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO projects (id, agent_name, project_name, project_path, created_at)
		VALUES ('legacy_string_agent:proj1', 'legacy_string_agent', 'proj1', '/path/proj1', '2026-08-25T08:00:00Z')
	`)
	require.NoError(t, err)

	proj, err := store.GetProject(ctx, "legacy_string_agent:proj1")
	require.NoError(t, err)
	assert.Equal(t, "proj1", proj.ProjectName)
	assert.Equal(t, 2026, proj.CreatedAt.Year())

	projs, err := store.ListProjects(ctx, "legacy_string_agent")
	require.NoError(t, err)
	assert.Len(t, projs, 1)

	// 3. User with string timestamp
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO users (id, username, full_name, role, created_at)
		VALUES ('user_str', 'user_str', 'User String', 'admin', '2026-08-25 12:00:00')
	`)
	require.NoError(t, err)

	user, err := store.GetUser(ctx, "user_str")
	require.NoError(t, err)
	assert.Equal(t, "user_str", user.ID)
	assert.Equal(t, 2026, user.CreatedAt.Year())

	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)

	// 4. Group with string timestamp
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO allowed_groups (group_id, group_title, is_active, created_at)
		VALUES ('-100999', 'Group String', 1, '2026-08-25T15:00:00.000Z')
	`)
	require.NoError(t, err)

	group, err := store.GetGroup(ctx, "-100999")
	require.NoError(t, err)
	assert.Equal(t, "-100999", group.GroupID)
	assert.Equal(t, 2026, group.CreatedAt.Year())

	groups, err := store.ListGroups(ctx)
	require.NoError(t, err)
	assert.Len(t, groups, 1)

	// 5. Audit log with string timestamp
	_, err = store.DB().ExecContext(ctx, `
		INSERT INTO audit_logs (
			session_key, agent_name, project_name, conversation_id,
			prompt_length, response_length, duration_seconds,
			input_tokens, output_tokens, thinking_tokens, total_tokens,
			status, error_message, created_at
		) VALUES (
			'telegram:chat_str', 'legacy_string_agent', '', '',
			10, 20, 1.5,
			100, 200, 50, 350,
			'SUCCESS', '', '2026-08-25T16:00:00Z'
		)
	`)
	require.NoError(t, err)

	logs, err := store.ListAuditLogs(ctx, "telegram:chat_str", 10)
	require.NoError(t, err)
	assert.Len(t, logs, 1)
	assert.Equal(t, 2026, logs[0].CreatedAt.Year())
}

