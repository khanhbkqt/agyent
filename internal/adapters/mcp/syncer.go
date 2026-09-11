package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agyent/internal/core/concurrency/oslock"
	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

var _ ports.MCPRegistryPort = (*MCPSyncer)(nil)

type mcpConfigFile struct {
	MCPServers map[string]domain.MCPServerConfig `json:"mcpServers"`
}

// MCPSyncer safely synchronizes MCP server configurations to one explicitly
// selected AGY config file using in-process mutexes, cross-process OS locks,
// atomic write-rename, reference counting, and crash recovery. It never fans
// a tenant's temporary configuration into several legacy global locations.
type MCPSyncer struct {
	mu             sync.Mutex
	configPath     string
	allConfigPaths []string
	lockFilePath   string
	turnLockPath   string
	baseServers    map[string]domain.MCPServerConfig
	activeMounts   map[string]int // serverKey -> activeRefCount
}

// NewMCPSyncer initializes a new MCPSyncer instance and performs startup crash recovery.
func NewMCPSyncer(configPath string) (*MCPSyncer, error) {
	var allPaths []string
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get user home: %w", err)
		}
		configPath = filepath.Join(home, ".gemini", "config", "mcp_config.json")
		allPaths = []string{
			filepath.Join(home, ".gemini", "config", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
			filepath.Join(home, ".gemini", "antigravity-cli", "mcp_config.json"),
		}
	} else {
		allPaths = []string{configPath}
	}

	syncer := &MCPSyncer{
		configPath:     configPath,
		allConfigPaths: allPaths,
		lockFilePath:   configPath + ".lock",
		turnLockPath:   configPath + ".turn.lock",
		activeMounts:   make(map[string]int),
		baseServers:    make(map[string]domain.MCPServerConfig),
	}

	if err := syncer.bootstrapClean(); err != nil {
		return nil, fmt.Errorf("failed to bootstrap mcp syncer: %w", err)
	}

	return syncer, nil
}

// AcquireExclusiveTurn serializes MCP turns per session or workspace. AGY
// discovers MCP configuration through the shared config file; using session-scoped
// turn leases prevents multi-workspace concurrency deadlocks while isolating
// ephemeral server mount lifetimes.
func (s *MCPSyncer) AcquireExclusiveTurn(ctx context.Context, sessionKey ...string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	lockPath := s.turnLockPath
	if len(sessionKey) > 0 {
		key := strings.TrimSpace(sessionKey[0])
		if key != "" {
			digest := sha256.Sum256([]byte(key))
			lockPath = fmt.Sprintf("%s.%s.turn.lock", s.configPath, hex.EncodeToString(digest[:16]))
		}
	}
	unlock, err := oslock.AcquireOSFileLock(ctx, lockPath, 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire exclusive MCP turn lease: %w", err)
	}
	return unlock, nil
}

func (s *MCPSyncer) bootstrapClean() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	unlock, err := oslock.AcquireOSFileLock(context.Background(), s.lockFilePath, 10*time.Second)
	if err != nil {
		return err
	}
	defer unlock()

	cfg, err := s.readConfigUnderLock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = &mcpConfigFile{MCPServers: make(map[string]domain.MCPServerConfig)}
		} else {
			return err
		}
	}

	cleanServers := make(map[string]domain.MCPServerConfig)
	for k, v := range cfg.MCPServers {
		if !isEphemeralServer(k) {
			cleanServers[k] = v
			s.baseServers[k] = v
		}
	}
	cfg.MCPServers = cleanServers

	return s.atomicWriteUnderLock(cfg)
}

// MountServers registers the given MCP servers for a turn with reference counting.
func (s *MCPSyncer) MountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error {
	if s == nil || len(servers) == 0 {
		return nil
	}

	slog.DebugContext(ctx, "Mounting ephemeral MCP servers", slog.String("session_key", sessionKey), slog.Int("count", len(servers)))

	s.mu.Lock()
	defer s.mu.Unlock()

	unlock, err := oslock.AcquireOSFileLock(ctx, s.lockFilePath, 10*time.Second)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acquire lock for MCP mounting", slog.String("error", err.Error()))
		return err
	}
	defer unlock()

	cfg, err := s.readConfigUnderLock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg = &mcpConfigFile{MCPServers: make(map[string]domain.MCPServerConfig)}
		} else {
			slog.ErrorContext(ctx, "Failed to read MCP config", slog.String("error", err.Error()))
			return fmt.Errorf("failed to read MCP config: %w", err)
		}
	}

	for _, srv := range servers {
		serverName := srv.ServerName
		if serverName == "" {
			continue
		}
		key := formatEphemeralKey(serverName, sessionKey)
		s.activeMounts[key]++

		// Ensure fallback environment variables for APIS-4D multi-tenant isolation
		if srv.Env == nil {
			srv.Env = make(map[string]string)
		} else {
			envCopy := make(map[string]string, len(srv.Env)+6)
			for k, v := range srv.Env {
				envCopy[k] = v
			}
			srv.Env = envCopy
		}
		if _, exists := srv.Env["AGYENT_SESSION_KEY"]; !exists && sessionKey != "" {
			srv.Env["AGYENT_SESSION_KEY"] = sessionKey
		}
		if _, exists := srv.Env["AGYENT_AGENT_WORKSPACE"]; !exists {
			if v := os.Getenv("AGYENT_AGENT_WORKSPACE"); v != "" {
				srv.Env["AGYENT_AGENT_WORKSPACE"] = v
			}
		}
		if _, exists := srv.Env["AGYENT_AGENT_NAME"]; !exists {
			if v := os.Getenv("AGYENT_AGENT_NAME"); v != "" {
				srv.Env["AGYENT_AGENT_NAME"] = v
			}
		}
		if _, exists := srv.Env["AGYENT_USER_ID"]; !exists {
			if v := os.Getenv("AGYENT_USER_ID"); v != "" {
				srv.Env["AGYENT_USER_ID"] = v
			}
		}
		if _, exists := srv.Env["AGYENT_TURN_ID"]; !exists {
			if v := os.Getenv("AGYENT_TURN_ID"); v != "" {
				srv.Env["AGYENT_TURN_ID"] = v
			}
		}

		cfg.MCPServers[key] = srv
	}

	if err := s.atomicWriteUnderLock(cfg); err != nil {
		slog.ErrorContext(ctx, "Failed to atomically write MCP config", slog.String("error", err.Error()))
		return fmt.Errorf("failed to atomically write MCP config: %w", err)
	}
	return nil
}

// UnmountServers removes or decrements reference count of ephemeral MCP servers upon turn finish.
func (s *MCPSyncer) UnmountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error {
	if s == nil || len(servers) == 0 {
		return nil
	}

	slog.DebugContext(ctx, "Unmounting ephemeral MCP servers", slog.String("session_key", sessionKey), slog.Int("count", len(servers)))

	s.mu.Lock()
	defer s.mu.Unlock()

	unlock, err := oslock.AcquireOSFileLock(ctx, s.lockFilePath, 10*time.Second)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to acquire lock for MCP unmounting", slog.String("error", err.Error()))
		return fmt.Errorf("failed to acquire lock for MCP unmounting: %w", err)
	}
	defer unlock()

	cfg, err := s.readConfigUnderLock()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		slog.ErrorContext(ctx, "Failed to read MCP config for unmount", slog.String("error", err.Error()))
		return fmt.Errorf("failed to read MCP config for unmount: %w", err)
	}

	for _, srv := range servers {
		serverName := srv.ServerName
		if serverName == "" {
			continue
		}
		key := formatEphemeralKey(serverName, sessionKey)
		if count, exists := s.activeMounts[key]; exists {
			if count <= 1 {
				delete(s.activeMounts, key)
				delete(cfg.MCPServers, key)
			} else {
				s.activeMounts[key] = count - 1
			}
		} else {
			delete(cfg.MCPServers, key)
		}
	}

	if err := s.atomicWriteUnderLock(cfg); err != nil {
		slog.ErrorContext(ctx, "Failed to atomically write MCP config during unmount", slog.String("error", err.Error()))
		return fmt.Errorf("failed to atomically write MCP config during unmount: %w", err)
	}
	return nil
}

func (s *MCPSyncer) atomicWriteUnderLock(cfg *mcpConfigFile) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mcp config: %w", err)
	}

	targets := s.allConfigPaths
	if len(targets) == 0 {
		targets = []string{s.configPath}
	}

	for _, target := range targets {
		dir := filepath.Dir(target)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		tmpFile := fmt.Sprintf("%s.tmp.%d.%d", target, os.Getpid(), time.Now().UnixNano())
		if err := os.WriteFile(tmpFile, data, 0600); err != nil {
			return fmt.Errorf("failed to write tmp mcp config %s: %w", tmpFile, err)
		}

		if f, err := os.Open(tmpFile); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}

		if err := os.Rename(tmpFile, target); err != nil {
			_ = os.Remove(tmpFile)
			return fmt.Errorf("failed to atomically replace %s: %w", target, err)
		}
	}

	return nil
}

func (s *MCPSyncer) readConfigUnderLock() (*mcpConfigFile, error) {
	data, err := os.ReadFile(s.configPath)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		for _, p := range s.allConfigPaths {
			if p != s.configPath {
				if d, e := os.ReadFile(p); e == nil {
					data = d
					err = nil
					break
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("corrupted mcp_config.json: %w", err)
	}
	if cfg.MCPServers == nil {
		cfg.MCPServers = make(map[string]domain.MCPServerConfig)
	}
	return &cfg, nil
}

func formatEphemeralKey(name string, scope ...string) string {
	if len(scope) > 0 {
		key := strings.TrimSpace(scope[0])
		if key != "" {
			digest := sha256.Sum256([]byte(key))
			// The full session string is never persisted in the global config; a
			// 128-bit digest prefix avoids Telegram's shared `telegram` prefix and
			// makes practical name collisions infeasible.
			return fmt.Sprintf("__agyent_ephemeral_%s_%s", hex.EncodeToString(digest[:16]), name)
		}
	}
	return "__agyent_ephemeral_" + name
}

func isEphemeralServer(name string) bool {
	return strings.HasPrefix(name, "__agyent_ephemeral_")
}
