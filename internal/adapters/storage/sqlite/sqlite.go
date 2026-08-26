package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
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
// and automatically executes any pending embedded schema migrations.
func Open(dbPath string) (*SQLiteStore, error) {
	// Create directory if not an in-memory database
	if dbPath != ":memory:" && !strings.HasPrefix(dbPath, "file::memory:") && dbPath != "" {
		dir := filepath.Dir(dbPath)
		if dir != "." && dir != "/" && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := writeDB.PingContext(ctx); err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("failed to ping sqlite write pool: %w", err)
	}

	// 2. Initialize Concurrent Read DB Pool (MaxOpenConns = 20)
	var readDB *sql.DB
	if dbPath == ":memory:" || strings.HasPrefix(dbPath, "file::memory:") || dbPath == "" {
		// In-memory databases must share the exact same pool to access in-memory tables
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

		if err := readDB.PingContext(ctx); err != nil {
			_ = writeDB.Close()
			_ = readDB.Close()
			return nil, fmt.Errorf("failed to ping sqlite read pool: %w", err)
		}
	}

	// Execute migrations on write pool
	if err := runMigrations(ctx, writeDB); err != nil {
		_ = writeDB.Close()
		if readDB != writeDB {
			_ = readDB.Close()
		}
		return nil, fmt.Errorf("failed to execute migrations: %w", err)
	}

	return &SQLiteStore{
		db:      writeDB,
		writeDB: writeDB,
		readDB:  readDB,
		lockMgr: NewSessionLockManager(),
	}, nil
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
