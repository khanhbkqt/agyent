# Plugin system architecture

> **Document status:** Reference
> **Code authority:** `internal/adapters/plugin`, `internal/adapters/context`, `internal/adapters/mcp`, `builtin/plugins`
> **Last verified:** 2026-09-05

An `agyent` plugin is an embedded or installed capability bundle. It can contribute
MCP server declarations, always-on rules, and progressively disclosed skills.

## 1. Bundle contract

```text
<plugin-name>/
├── plugin.json                         # required, strict schema
├── mcp_config.json                     # optional MCP server declarations
├── rules/AGENTS.md                     # optional always-on plugin constraints
├── skills/<skill-name>/SKILL.md        # optional workflow metadata/instructions
├── skills/<skill-name>/references/     # optional conditional detail
└── server.py or other runtime files    # plugin-specific
```

`plugin.json` accepts only fields declared by `domain.PluginManifest`; unknown
fields are rejected. The manifest name and plugin directory name must match.

The current embedded catalog is:

| Plugin | Default | MCP tools | Skills |
| --- | --- | --- | --- |
| `database-sqlite` | enabled | `sqlite_query_readonly` | `sqlite-inspect` |
| `scheduler` | enabled | schedule/list/cancel and heartbeat tools | `schedule-management` |
| `subagent-dispatcher` | enabled | dispatch/progress/cancel/list | `subagent-dispatch` |
| `system-diagnostics` | enabled | `get_system_health` | `system-health` |
| `browser-camoufox` | disabled | search, extraction, sessions, actions, capture and media tools | `web-browse-camoufox` |

The manifest and each server's `tools/list` response are authoritative for exact
capabilities.

## 2. Discovery and precedence

`PluginManager.ListPlugins` scans sources from lowest to highest precedence:

1. Plugins embedded in the binary.
2. Repository `builtin/plugins` when running from a development checkout.
3. User-global `~/.agyent/plugins`.
4. Agent-home `plugins` and `.agents/plugins` paths.
5. Project/workspace `.agents/plugins` and `plugins` paths.

Plugins are deduplicated by manifest name; later sources override earlier ones.
Workspace overrides therefore replace a global/builtin plugin with the same name.
Do not rely on map iteration order for presentation or prompt construction; sort
the resulting capabilities.

## 3. Activation and context assembly

Only a plugin whose effective manifest has `enabled: true` contributes
capabilities:

- MCP servers become temporary mounts for a turn.
- Each `SKILL.md` contributes a lightweight name/description/path header to the
  Level 3 skill index. The full skill is read only when it applies.
- `rules/AGENTS.md` is placed in the workspace/plugin directive block and is
  therefore always active with the plugin.

Keep rules small and unconditional. Put task-specific workflows in skills so they
do not consume or constrain every turn.

## 4. MCP lifecycle and isolation

AGY currently discovers MCP servers from
`~/.gemini/antigravity-cli/mcp_config.json`. `MCPSyncer` therefore:

1. Acquires an OS-backed exclusive turn lease for MCP-enabled turns.
2. Adds session-scoped server entries with a hashed ephemeral key.
3. Writes the configuration atomically under an OS lock.
4. Reference-counts mounts.
5. Unmounts entries when the turn finishes.
6. Removes stale ephemeral entries on startup while preserving base servers.

The exclusive lease is an isolation constraint imposed by the process-global AGY
configuration. Do not weaken it until AGY supports per-invocation MCP config and a
replacement design is tested.

MCP processes also receive APIS-4D identity. Plugins must enforce identity and
path ownership themselves; temporary configuration names are not an authorization
boundary.

## 5. Installation and updates

- Global installation target: `~/.agyent/plugins/<name>`.
- Workspace installation target: `<workspace>/.agents/plugins/<name>`.
- Enabling/disabling an embedded-only plugin first extracts it to the selected
  disk scope, then changes that copy's manifest.
- Embedded plugins are synchronized at daemon startup and through CLI plugin
  update commands.
- Dependency validation resolves declared MCP commands through `exec.LookPath`.

Use `agyent plugin --help` for current CLI syntax. Chat command behavior is owned
by `internal/core/engine/commands.go`.

## 6. Skill contract

A skill directory contains at least `SKILL.md` with YAML frontmatter:

```yaml
---
name: example-skill
description: Explain the capability and the requests that should activate it.
---
```

Names use lowercase letters, digits, and hyphens and match the directory. The
description must be discriminating; avoid generic catchalls. The body contains
the shared workflow and real constraints. Conditional schemas/recipes belong in
`references/` and must be linked from the body.

## 7. Verification

For any plugin change:

```bash
make docs-check
make lint-plugins
go test ./internal/adapters/plugin/... ./internal/adapters/context/... ./internal/adapters/mcp/...
```

Also exercise the MCP `initialize`, `tools/list`, invalid tool, invalid arguments,
and authorized/denied filesystem or tenant paths relevant to the change.
