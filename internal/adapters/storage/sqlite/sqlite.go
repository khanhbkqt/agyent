package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agyent/internal/core/ports"

	_ "modernc.org/sqlite" // Pure-Go SQLite Driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Compile-time interface compliance assertions.
var (
	_ ports.StoragePort            = (*SQLiteStore)(nil)
	_ ports.SessionRepository      = (*SQLiteStore)(nil)
	_ ports.AgentRepository        = (*SQLiteStore)(nil)
	_ ports.ProjectRepository      = (*SQLiteStore)(nil)
	_ ports.UserRepository         = (*SQLiteStore)(nil)
	_ ports.GroupRepository        = (*SQLiteStore)(nil)
	_ ports.AuditRepository        = (*SQLiteStore)(nil)
	_ ports.ConversationRepository = (*SQLiteStore)(nil)
	_ ports.SubagentRepository     = (*SQLiteStore)(nil)
	_ ports.ScheduleRepository     = (*SQLiteStore)(nil)
)

// SQLiteStore implements ports.StoragePort using a Pure-Go SQLite backend in WAL mode with Dual-Pool architecture.
type SQLiteStore struct {
	db      *sql.DB // default compatibility reference
	writeDB *sql.DB // Single-Writer pool (MaxOpenConns = 1) for all mutations
	readDB  *sql.DB // Concurrent Read pool (MaxOpenConns = 20) for high-throughput reads
	lockMgr *SessionLockManager
}

func (s *SQLiteStore) writer() *sql.DB {
	if s.writeDB != nil {
		return s.writeDB
	}
	return s.db
}

func (s *SQLiteStore) reader() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.db
}

// BuildDSN constructs a robust SQLite connection string with performance PRAGMAs.
func BuildDSN(dbPath string) string {
	trimmed := strings.TrimSpace(dbPath)
	if trimmed == "" || trimmed == ":memory:" {
		return "file::memory:?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	}
	if strings.HasPrefix(trimmed, "file:") {
		return trimmed
	}

	cleanPath := filepath.Clean(trimmed)
	slashPath := filepath.ToSlash(cleanPath)

	params := url.Values{}
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "journal_mode(WAL)")
	params.Add("_pragma", "synchronous(NORMAL)")
	params.Add("_pragma", "foreign_keys(1)")
	params.Add("_pragma", "temp_store(MEMORY)")
	params.Add("_pragma", "cache_size(-8000)") // 8MB page cache

	return fmt.Sprintf("file:%s?%s", slashPath, params.Encode())
}

// Open initializes the SQLite database with Dual-Pool Single-Writer architecture,
// Open initializes the SQLite database with Dual-Pool Single-Writer architecture,
// creates a WAL-safe snapshot backup if running migrations on a legacy database,
// and automatically executes any pending embedded schema migrations.
func Open(dbPath string) (*SQLiteStore, error) {
	isMemory := dbPath == ":memory:" || strings.HasPrefix(dbPath, "file::memory:") || dbPath == ""
	legacyFileExists := false
	if !isMemory {
		if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() && fi.Size() > 0 {
			legacyFileExists = true
		}
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "/" && dir != "" {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, fmt.Errorf("failed to create database directory %s: %w", dir, err)
			}
		}
	}

	dsn := BuildDSN(dbPath)

	// 1. Initialize Single-Writer DB Pool (MaxOpenConns = 1)
	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite write pool: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	writeDB.SetConnMaxLifetime(0)
	writeDB.SetConnMaxIdleTime(0)

	// Verify write connection
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := writeDB.PingContext(pingCtx); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("failed to ping sqlite write pool: %w", err)
	}

	// 2. Snapshot backup before migration if legacy DB version < 11
	migrationCtx, migrationCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer migrationCancel()

	if err := backupSnapshotIfNeeded(migrationCtx, writeDB, dbPath, legacyFileExists); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("database pre-migration backup failed: %w", err)
	}

	// 3. Execute migrations on write pool
	if err := runMigrations(migrationCtx, writeDB); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("failed to execute migrations: %w", err)
	}

	// 4. Initialize Concurrent Read DB Pool (MaxOpenConns = 20) ONLY AFTER MIGRATIONS SUCCEED
	var readDB *sql.DB
	if isMemory {
		readDB = writeDB
	} else {
		readDB, err = sql.Open("sqlite", dsn)
		if err != nil {
			_ = writeDB.Close()
			return nil, fmt.Errorf("failed to open sqlite read pool: %w", err)
		}
		readDB.SetMaxOpenConns(20)
		readDB.SetMaxIdleConns(5)
		readDB.SetConnMaxLifetime(0)
		readDB.SetConnMaxIdleTime(0)

		if err := readDB.PingContext(pingCtx); err != nil {
			_ = writeDB.Close()
			_ = readDB.Close()
			return nil, fmt.Errorf("failed to ping sqlite read pool: %w", err)
		}
	}

	return &SQLiteStore{
		db:      writeDB,
		writeDB: writeDB,
		readDB:  readDB,
		lockMgr: NewSessionLockManager(),
	}, nil
}

func backupSnapshotIfNeeded(ctx context.Context, db *sql.DB, dbPath string, legacyFileExists bool) error {
	if !legacyFileExists || dbPath == ":memory:" || strings.HasPrefix(dbPath, "file::memory:") || dbPath == "" {
		return nil
	}

	// Check if schema_migrations table exists
	var tableExists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='schema_migrations')`).Scan(&tableExists); err != nil {
		return fmt.Errorf("failed to inspect schema_migrations before backup: %w", err)
	}
	if !tableExists {
		return nil // Fresh DB before initial schema migration
	}

	var maxVersion int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		return fmt.Errorf("failed to determine legacy schema version before backup: %w", err)
	}
	if maxVersion == 0 || maxVersion >= 11 {
		return nil // Fresh DB or already migrated to v11+
	}

	// Legacy DB detected needing migration 000011 -> Create snapshot via atomic VACUUM INTO
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	backupName := fmt.Sprintf("agyent.pre_000011.%d.%d.bak", time.Now().Unix(), time.Now().UnixNano())
	backupPath := filepath.Join(backupDir, backupName)

	escapedBackupPath := strings.ReplaceAll(backupPath, "'", "''")
	vacuumQuery := fmt.Sprintf("VACUUM INTO '%s'", escapedBackupPath)
	if _, err := db.ExecContext(ctx, vacuumQuery); err != nil {
		return fmt.Errorf("failed to create pre-migration snapshot via VACUUM INTO: %w", err)
	}

	if err := os.Chmod(backupPath, 0600); err != nil {
		return fmt.Errorf("failed to secure backup snapshot permissions: %w", err)
	}

	// Verify integrity of the backup snapshot
	verifyDB, err := sql.Open("sqlite", BuildDSN(backupPath))
	if err != nil {
		return fmt.Errorf("failed to open backup snapshot for verification: %w", err)
	}
	defer verifyDB.Close()

	var integrity string
	if err := verifyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fmt.Errorf("backup snapshot integrity check failed (result: %s, err: %v)", integrity, err)
	}

	slog.Info("Verified WAL-consistent snapshot created before migration 000011",
		slog.String("backup_path", backupPath),
		slog.Int("previous_version", maxVersion),
	)

	return nil
}

// DB returns the underlying write *sql.DB instance (useful for advanced/raw queries if needed).
func (s *SQLiteStore) DB() *sql.DB {
	return s.writer()
}

// LockManager returns the SessionLockManager instance.
func (s *SQLiteStore) LockManager() *SessionLockManager {
	return s.lockMgr
}

// Migrate manually triggers migration execution on the write pool.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	return runMigrations(ctx, s.writer())
}

// Close gracefully closes the SQLite database connections after flushing WAL checkpoints.
func (s *SQLiteStore) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if s.writeDB != nil {
		// Truncate WAL journal on writer to ensure clean zero-lag shutdown
		_, _ = s.writeDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	}

	var firstErr error
	if s.readDB != nil && s.readDB != s.writeDB {
		if err := s.readDB.Close(); err != nil {
			firstErr = err
		}
	}
	if s.writeDB != nil {
		if err := s.writeDB.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	// 1. Create schema_migrations table if not exists
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL,
			description TEXT NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// 2. Read embedded migration files
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var upFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			upFiles = append(upFiles, entry.Name())
		}
	}
	sort.Strings(upFiles)

	// 3. Apply unapplied migrations in separate transactions
	for _, file := range upFiles {
		var version int
		// File format: 000001_init_schema.up.sql
		parts := strings.SplitN(file, "_", 2)
		if len(parts) < 2 {
			return fmt.Errorf("invalid migration filename format %s: expected <version>_<description>.up.sql", file)
		}
		if _, err := fmt.Sscanf(parts[0], "%d", &version); err != nil {
			return fmt.Errorf("invalid migration filename format %s: %w", file, err)
		}
		desc := strings.TrimSuffix(parts[1], ".up.sql")

		var exists bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`, version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration version %d: %w", version, err)
		}
		if exists {
			continue
		}

		content, err := migrationFS.ReadFile("migrations/" + file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		// Execute in transaction with isolated closure ensuring rollback on error
		err = func() error {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("failed to begin transaction for migration %d: %w", version, err)
			}
			defer tx.Rollback()

			if _, err := tx.ExecContext(ctx, string(content)); err != nil {
				return fmt.Errorf("failed executing migration %s: %w", file, err)
			}

			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at, description) VALUES (?, ?, ?)`,
				version, time.Now().UnixMilli(), desc)
			if err != nil {
				return fmt.Errorf("failed recording migration %d: %w", version, err)
			}

			return tx.Commit()
		}()
		if err != nil {
			return err
		}
	}

	return nil
}
