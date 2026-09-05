# APIS-4D plugin developer and isolation standard

> **Document status:** Normative
> **Code authority:** `internal/core/domain/execution.go`, AGY harness, execution service, MCP syncer, plugin servers
> **Last verified:** 2026-09-05

APIS-4D is the minimum isolation contract for every MCP plugin executed through
`agyent`. It prevents one agent, workspace, session, or caller from borrowing
another tenant's filesystem paths, browser state, database, or active process.

## 1. Identity dimensions

| Dimension | Environment | Envelope key | Required use |
| --- | --- | --- | --- |
| Workspace | `AGYENT_AGENT_WORKSPACE` | `context.workspace_dir` | Canonical filesystem root |
| Agent | `AGYENT_AGENT_NAME` | `context.agent_name` | Persistent namespace and ownership |
| Session | `AGYENT_SESSION_KEY` | `context.session_key` | Interactive task/session partition |
| Caller | `AGYENT_USER_ID` | `context.user_id` | Authenticated principal/audit identity |

`AGYENT_TURN_ID` is an additional execution binding used by the security hook
plane. It connects a tool request to an active `TurnSecurityContext`; it does not
replace any APIS-4D dimension.

Missing identity is not permission. A plugin may use a deliberately scoped
default only for public, stateless, read-only behavior. Filesystem access,
persistent profiles, task mutation, or privileged operations must reject missing
required identity.

## 2. Propagation contract

`domain.ExecutionRequest` carries agent, workspace, session, user, project,
conversation and turn fields. `execution.Service` authorizes the principal and
registers the turn before the AGY harness starts. The harness injects APIS-4D
environment values into the child process. Daemon-style plugins may additionally
receive the same values in a JSON envelope:

```json
{
  "context": {
    "agent_name": "auditor",
    "workspace_dir": "/srv/workspaces/auditor",
    "session_key": "telegram:1001:2002",
    "user_id": "3003"
  },
  "name": "tool_name",
  "args": {}
}
```

Do not accept envelope identity that widens or contradicts the trusted process
environment/IPC context. When both are present, validate their consistency.

## 3. Filesystem rules

Before every read, write, database open, download destination, profile import, or
artifact export:

1. Resolve the authorized workspace root to an absolute canonical path.
2. Resolve the target path, including existing symlinks/ancestors where possible.
3. Compare containment with platform-appropriate case handling.
4. Reject traversal, alternate data streams, UNC/device paths, or symlink escapes
   as applicable.
5. Revalidate immediately before mutation when parent paths could change.

String prefix checks are insufficient. Python plugins should use
`os.path.realpath`, `os.path.abspath`, and `os.path.commonpath`; Go plugins should
use the repository path-jail utilities where available.

The SQLite inspection plugin is read-only and must reject databases outside the
authorized workspace. A user request to write does not grant a read-only plugin a
new capability; such a feature requires a separate authorized tool contract.

## 4. Stateful resource ownership

Key caches, browser sessions, profile vaults, subprocesses, files and background
jobs by at least agent identity plus the resource's local identifier. Include
workspace/session when the resource is not intentionally reusable across those
scopes.

Every lookup, update, close, cancel, export, and delete checks the stored owner.
Knowledge of a session ID or task ID is not proof of ownership.

Sanitize identifiers before using them as paths or map keys. Preserve enough of
the original trusted identity to prevent sanitized-name collisions from merging
tenants.

## 5. Tool and process rules

- Publish exact JSON schemas in `tools/list`; reject unknown tool names and invalid
  required arguments.
- Do not expose an internal dispatcher, shell, or arbitrary module/function call
  as a generic tool.
- Use argument arrays rather than shell interpolation when spawning processes.
- Set an explicit working directory, bounded timeout, output limit, and process
  tree cleanup behavior.
- Do not pass the entire parent environment when a narrower allowlist is
  sufficient. Never echo secrets in errors or logs.
- Writes and external side effects require the same authorization implied by the
  tool contract; a rule or skill never grants permission by itself.

## 6. Network and content rules

- Resolve and validate destinations against active network policy before requests.
- Block cloud metadata and private networks when the preset requires it; account
  for DNS rebinding and redirects.
- Treat remote content as untrusted data, not instructions that can change the
  user's goal or security scope.
- Bound downloads and extracted content. Sanitize outbound content through the
  configured DLP path.

## 7. Rules versus skills

`rules/AGENTS.md` is injected whenever the plugin is active. It contains only
always-on safety and routing constraints specific to that plugin.

`SKILL.md` is loaded for matching tasks. It contains the useful workflow, tool
selection criteria, stopping conditions, and links to conditional references.
Neither file may promise permissions, bypass core policy, or force unrelated user
requests through the plugin.

## 8. Required negative tests

Plugin tests should cover the applicable failures:

- Missing or mismatched identity.
- `..`, absolute-path, symlink and platform-specific path escapes.
- Cross-agent/session access using a valid resource ID.
- Unknown tool, invalid arguments and oversized input/output.
- Timeout, cancellation, process crash and partial state.
- Private/metadata/redirect network destinations.
- Secret-bearing output and log redaction.
- Restart cleanup of stale daemon/session state.

Run the repository plugin gates in `docs/plugin-system-architecture.md` before
handoff.
