# Milestone 7: Context Management, Skills Subsystem & Extensible Plugins

Comprehensive technical specification and implementation design for **Milestone 7: Context Management, Progressive Skills, Dynamic MCP Syncing & Extensible Plugins** of the **`agyent`** system.

---

## 1. Objectives & Architectural Guardrails

```mermaid
flowchart TD
    subgraph HardeningLayer ["5 MANDATORY PRODUCTION HARDENING GUARDRAILS"]
        H1["1. Concurrency & Sync:\nAtomic Write (.tmp rename) + flock/LockFileEx + RefCount Registry"]
        H2["2. Process Tree Safety:\nWindows Kernel Job Object + Linux PR_SET_PDEATHSIG (100% Zombie Cleanup)"]
        H3["3. Streaming Pruning:\nHead-Tail Sandwich (800 head + 800 tail) + Rune/Codeblock Auto-balancer"]
        H4["4. Security & Sandboxing:\nCanonical Path Boundary Check (EvalSymlinks) + DisallowUnknownFields"]
        H5["5. Clean Architecture:\nDedicated MCPRegistryPort decoupled from ContextResolverPort"]
    end

    HardeningLayer --> Engine["Agyent Core Engine (Milestone 7)"]
```

---

## 2. Implementation Phases & Work Breakdown Structure

```mermaid
gantt
    title Detailed Milestone 7 Implementation Roadmap
    dateFormat  YYYY-MM-DD
    section Phase 1: Clean Domain & Ports
    Pure Domain Models (context.go, plugin.go)     :p1_1, 2026-08-28, 1d
    Ports (ContextResolver, MCPRegistry, Plugin)   :p1_2, after p1_1, 1d
    section Phase 2: Process & Concurrency Adapters
    Windows Job Object & Linux Process Guard       :p2_1, after p1_2, 1d
    MCPSyncer (Atomic Write + FileLock + RefCount) :p2_2, after p2_1, 1d
    ContextResolver & Safe YAML Frontmatter Parser :p2_3, after p2_2, 1d
    PluginManager & Dependency Pre-flight Checker  :p2_4, after p2_3, 1d
    Unit & Concurrency Tests Suite                 :p2_5, after p2_4, 1d
    section Phase 3: Built-in Plugins Catalog
    Bundle browser-camoufox (Camoufox Stealth)     :p3_1, after p2_5, 1d
    Bundle system-diagnostics & sqlite             :p3_2, after p3_1, 1d
    section Phase 4: Engine Integration & Pruning
    Engine executeTurn Integration                 :p4_1, after p3_2, 1d
    Head-Tail Sandwich Pruning Middleware          :p4_2, after p4_1, 1d
    section Phase 5: Slash Commands & E2E Testing
    Slash Commands (/context, /skills, /plugins)   :p5_1, after p4_2, 1d
    Live Harness & Real agy CLI E2E Integration    :p5_2, after p5_1, 1d
```

---

### Phase 1: Pure Core Domain & Ports

#### 1.1. `internal/core/domain/context.go` & `plugin.go`
- **Goal:** Define pure domain models independent of OS infrastructure.
- **Components:**
  - `ContextScope`: `ScopeGlobal` | `ScopeWorkspace`.
  - `SkillHeader`: `Name`, `Description`, `FilePath`, `Scope`.
  - `MCPServerConfig`: `ServerName`, `Command`, `Args`, `ServerURL`, `Env`, `Scope`, `Disabled`.
  - `PluginManifest`: `Name`, `Version`, `Description`, `Author`, `Enabled`, `Tags`.
  - `Plugin`: `Manifest`, `Path`, `Scope`, `MCPServers`, `Skills`, `Rules`, `InstalledAt`.
  - `ResolvedContext`: `WorkingDir`, `CombinedDirectives`, `SkillHeaders`, `ActiveMCPServers`, `ActivePlugins`, `ResolvedAt`.

#### 1.2. `internal/core/ports/` (Clean SRP Responsibility Separation)
- **`ports/context.go`**:
  ```go
  type ContextResolverPort interface {
      Resolve(ctx context.Context, globalHome string, workspaceDir string) (*domain.ResolvedContext, error)
      DiscoverSkills(ctx context.Context, globalHome string, workspaceDir string) ([]domain.SkillHeader, error)
  }
  ```
- **`ports/mcp.go`**:
  ```go
  type MCPRegistryPort interface {
      MountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error
      UnmountServers(ctx context.Context, sessionKey string, servers []domain.MCPServerConfig) error
  }
  ```
- **`ports/plugin.go`**:
  ```go
  type PluginManagerPort interface {
      ListPlugins(ctx context.Context, globalHome, workspaceDir string) ([]domain.Plugin, error)
      TogglePlugin(ctx context.Context, pluginName string, enabled bool, scope domain.ContextScope, workspaceDir string) error
      InstallBuiltinPlugin(ctx context.Context, pluginName string, targetScope domain.ContextScope, workspaceDir string) error
      ValidatePlugin(ctx context.Context, plugin *domain.Plugin) error
      AssemblePluginCapabilities(ctx context.Context, globalHome, workspaceDir string) (*domain.ResolvedContext, error)
  }
  ```

---

### Phase 2: Infrastructure, Safe Synchronization & Process Lifecycles

#### 2.1. `internal/adapters/harness/agy/job_windows.go` & `job_linux.go`
- **Windows:** Attach spawned `exec.Cmd` processes to Windows Kernel Job Objects (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`). When the parent process terminates, all child processes (Camoufox / Firefox / Python) are immediately reaped by the OS kernel, eliminating zombie processes.
- **Linux:** Enable `SysProcAttr: { Pdeathsig: syscall.SIGKILL }`.

#### 2.2. `internal/adapters/mcp/syncer.go` (`MCPRegistryPort` Implementation)
- **Atomic Write:** Writes to `.tmp` file $\rightarrow$ `f.Sync()` $\rightarrow$ `os.Rename`.
- **Cross-Process File Lock:** Uses `LockFileEx` (Windows) / `flock` (Linux) to protect against concurrent file writes.
- **Reference Counting:** Tracks active session usage per server (`activeMounts[key]++`) to prevent premature unmounting during concurrent turns.
- **Crash Recovery:** Automatically purges servers with prefix `__agyent_ephemeral_` upon startup.

#### 2.3. `internal/adapters/context/resolver.go`
- **Security Check:** Integrates `ValidateSecurePath` with `filepath.EvalSymlinks` to prevent path traversal attacks.
- **Safe YAML Parser:** Parses `SKILL.md` frontmatter safely, isolating syntax errors without crashing the daemon.

#### 2.4. `internal/adapters/plugin/manager.go` & `validator.go`
- **Dependency Pre-flight:** Verifies required binaries exist (`exec.LookPath`) before enabling plugins.
- **Strict JSON Decoder:** Uses `DisallowUnknownFields()` to validate `plugin.json` manifests.

---

### Phase 3: Built-in Capability Plugins Catalog

1. **`builtin/plugins/browser-camoufox/`:**
   - `plugin.json`, `server.py` (JSON-RPC 2.0 Stdio MCP), `SKILL.md`, `rules/AGENTS.md`.
2. **`builtin/plugins/system-diagnostics/`:**
   - Host CPU, RAM, Disk, and Process monitoring.
3. **`builtin/plugins/database-sqlite/`:**
   - Schema inspection and local SQLite querying.

---

### Phase 4: Engine Integration & Head-Tail Sandwich Pruning

#### 4.1. `internal/core/engine/pruner.go`
- Prunes outputs exceeding 2000 characters using a **Head-Tail Sandwich** strategy (first 800 characters + summary notice + last 800 characters).
- Auto-balances Markdown code fences (```` ``` ````) and respects UTF-8 rune boundaries.

#### 4.2. `internal/core/engine/engine.go` & `bootstrap.go`
- Integrates `ContextResolverPort`, `MCPRegistryPort`, `PluginManagerPort` into `executeTurn()`.
- Implements **Progressive Disclosure** (injecting only skill headers into system prompts).

---

### Phase 5: Slash Commands & Live E2E Testing

#### 5.1. `internal/core/engine/commands.go`
- `/context`: Detailed breakdown of token budget, CWD, active MCP servers, and directives.
- `/skills`: Lists loaded skills with scope.
- `/plugins`: Lists plugins with `[ENABLED] / [DISABLED]` status.
- `/plugin enable <name>` / `/plugin disable <name>`: Toggle plugins directly via Telegram.

#### 5.2. Comprehensive Testing
- **Automated Unit & Concurrency Tests:** `go test -v -race ./...` (Coverage > 85%).
- **Live Harness E2E:** Live validation with `agy` CLI binary and `bin/agyent`.

---

## 3. Definition of Done (DoD)

- [x] Zero Race Conditions: Passed all concurrency tests under `-race`.
- [x] Zero Zombie Subprocesses: Process management enforced via Windows Job Objects and Linux PDEATHSIG.
- [x] Zero Corrupted Config: 100% atomic writes for `mcp_config.json`.
- [x] Telegram HTML formatting preserved during streaming and in-memory pruning.
- [x] Built-in Plugin `browser-camoufox` executed live with real `agy` CLI.
