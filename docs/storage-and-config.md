# Storage and configuration

> **Document status:** Reference
> **Code authority:** `internal/config/config.go`, `internal/adapters/storage/sqlite`, SQLite migrations
> **Last verified:** 2026-09-05

This document describes stable behavior and points to the current schema/defaults.
The Go structs and migration files are authoritative; do not copy this document's
examples into production without checking them.

## 1. Configuration lifecycle

`config.Load` follows this order:

1. Start with `DefaultConfig()`.
2. Read YAML when the configured file exists.
3. Apply supported `AGYENT_*` environment overrides.
4. Expand paths.
5. Let the command call `Validate()` before starting services.

`config.Save` creates the parent directory and writes the file with mode `0600`.
The default CLI path is `~/.agyent/config.yaml`.

The top-level sections are:

| Section | Purpose |
| --- | --- |
| `server` | Local HTTP bind address for webhook/server behavior |
| `telegram` | Single/multi-bot tokens, mode, webhook secret, admins and groups |
| `agy` | Binary, timeout, model/effort/mode, stream, compaction and queue behavior |
| `storage` | Database, agent root, debounce and typing heartbeat intervals |
| `logging` | `slog` level and text/JSON format |
| `evolution` | Reflection scanner, thresholds, limits and queue |
| `subagent` | Worker count, timeout, model and effort |
| `security` | Preset plus command/filesystem/subagent/network/DLP policies |
| `agents` | Per-agent workspace, model, effort, description and security overrides |

Use `internal/config/config.go` for the full field list and effective security
preset matrix. The wizard is the preferred way to create a valid initial file.

## 2. Minimal example

```yaml
telegram:
  bot_token: "replace-with-token"
  mode: polling
  admin_user_ids: [123456789]

agy:
  binary_path: agy
  default_timeout_seconds: 1800
  default_effort: high
  default_mode: accept-edits
  streaming_enabled: true
  auto_compact: true
  compact_threshold_ratio: 0.70
  queue_mode: fifo
  append_strategy: coalesce
  grace_timeout_seconds: 3.0

storage:
  db_path: ~/.agyent/agyent.db
  agents_dir: ~/.agyent
  debounce_seconds: 2.0
  heartbeat_interval_seconds: 4.0

security:
  preset: balanced

logging:
  level: info
  format: text
```

When `telegram.bots` is non-empty, it is the normalized multi-bot source. The
legacy `telegram.bot_token` becomes a single `default` bot only when no list is
configured.

Validation requires at least one bot token, at least one Telegram admin ID, a
database path, an agent root, and an AGY binary path. Webhook mode also requires a
webhook URL. A configured Telegram secret token must contain at least sixteen
non-space characters.

## 3. Environment overrides

Supported overrides are intentionally explicit. The prefixes currently include:

- `AGYENT_SERVER_*`
- `AGYENT_TELEGRAM_*`
- `AGYENT_AGY_*`
- `AGYENT_STORAGE_*`
- `AGYENT_LOGGING_*`
- `AGYENT_EVOLUTION_*`
- `AGYENT_SUBAGENT_*`

See `applyEnvOverrides` in `internal/config/config.go` for exact variable names.
Security policy is not broadly replaceable through environment variables; use the
validated configuration/CLI path.

## 4. Workspace layout

With the default `storage.agents_dir: ~/.agyent`:

```text
~/.agyent/
├── agyent.db
├── backups/
├── plugins/
├── workspace/             # default agent "agyent"
│   ├── IDENTITY.md
│   ├── SOUL.md
│   ├── USER.md
│   ├── MEMORY.md
│   ├── AGENTS.md
│   ├── HEARTBEAT.md
│   ├── memory/
│   └── .agents/
└── workspace-<agent>/     # custom agent
```

`ResolveAgentWorkspace` defines these names. `MigrateLegacyWorkspace` copies
missing persona files from the earlier `~/.agyent/agents/workspace` location into
the current default workspace without overwriting existing files.

Project workspaces can point elsewhere. The resolved workspace is passed to AGY
with `--project outside-of-project --add-dir <workspace>` and through APIS-4D
identity variables.

## 5. SQLite connection model

`sqlite.Open` builds two pools for file-backed databases:

| Pool | Limit | Use |
| --- | --- | --- |
| Writer | `MaxOpenConns(1)` | Migrations and all mutations |
| Reader | `MaxOpenConns(20)` | Concurrent read queries |

In-memory databases reuse the writer connection so both pools observe the same
database. `BuildDSN` enables busy timeout, WAL for file databases, synchronous
normal, foreign keys, in-memory temp storage, and a bounded page cache.

`Close` checkpoints and truncates the WAL before closing reader and writer pools.

## 6. Schema and migrations

The embedded files under `internal/adapters/storage/sqlite/migrations/` are the
only schema source of truth. At this review the latest version is `000011`.

| Migration | Adds/changes |
| --- | --- |
| `000001` | Users, groups, agents, projects, sessions, project conversations, audit logs |
| `000002` | Flat multi-conversation model |
| `000003` | Evolution cursor/state |
| `000004` | Cache-read token metrics |
| `000005` | Model and effort selection |
| `000006` | Subagent task persistence |
| `000007` | Agent ownership and ACL |
| `000008` | Per-agent security preset |
| `000009` | Conversation wildcard lookup index |
| `000010` | Agent schedules and heartbeats |
| `000011` | Privacy hardening and persistent security audit events |

Migrations are applied in filename order inside individual transactions and
recorded in `schema_migrations`. Before upgrading a legacy database to `000011`,
the store creates and integrity-checks a `VACUUM INTO` snapshot under `backups/`.
`000011` is intentionally irreversible; prior migrations have matching down
files.

Never modify an already released migration. Add the next number and test fresh
creation plus the relevant upgrade path.

## 7. Timestamp and query rules

- Persist timestamps as Unix milliseconds.
- Use `FlexTime` when reading columns that may contain legacy seconds,
  milliseconds, numeric strings, RFC3339, SQL time strings, or `time.Time`.
- Use the reader pool for `SELECT` operations and writer pool for mutations.
- Filter tenant-owned data in SQL with the authenticated scope key.
- Wrap transactional errors with the migration/repository operation and preserve
  the cause.

## 8. Operational inspection

Prefer read-only commands:

```bash
agyent doctor --skip-network
agyent stats
go run ./scripts/debug_session.go --session '<session-key>'
```

Do not edit a live database to repair state until logs, audit rows, and task/session
state establish the cause and the user has authorized remediation.
