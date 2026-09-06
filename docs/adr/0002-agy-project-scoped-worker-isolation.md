# ADR 0002: AGY project-scoped worker isolation

> **Document status:** Normative  
> **Code authority:** `internal/core/execution`, `internal/adapters/harness/agy`,
> `internal/adapters/security`, `internal/adapters/storage/sqlite`,
> `internal/adapters/subagent`  
> **Last verified:** 2026-09-06

- Status: Accepted
- Date: 2026-09-06
- Owners: agyent maintainers
- Supersedes: none
- Superseded by: none

## Context

Managed AGY guest agents originally shared the daemon user's process and filesystem
view. The runner selected `outside-of-project`, did not request AGY's sandbox,
and hook/path/command enforcement had gaps documented in
[AGY project-scoped security isolation](../agy-project-scoped-security-isolation.md).
Global AGY native permission grants would permit unattended work but would also
make that permission available outside an individual worker's scope.

A macOS POC with AGY 1.1.27 confirmed that an AGY project-local permission grant
is selected by `--project <id>`, is not available to `outside-of-project`, and
can run under `--sandbox`. It did not establish Linux or Windows compatibility,
nor does it make an AGY project ID an agent identity or a data-isolation
boundary.

## Decision

For managed worker agents, agyent will use a controller-owned registry keyed by
`(tenant, agent, agent_generation, execution_host, AGY configuration namespace)`
that maps the authenticated agent identity to a dedicated AGY project ID and
canonical workspace. Every AGY launch must be admitted by
`internal/core/execution.Service` and carry that binding, its session/conversation
binding, and turn identity. The runner selects that project ID, passes exactly
one canonical workspace with `--add-dir`, and uses `--sandbox`; it never
forwards `--dangerously-skip-permissions` for a managed worker. Ownership
transfer, agent recreation, configuration-namespace/host change, or revocation
invalidates the project mapping and linked conversation rather than reusing it.

The AGY project ID scopes native AGY grants only. The dynamic authorization
decision remains in agyent's authenticated PreToolUse hook/IPC policy, using the
worker capability matrix. Native denials are execution failures. Missing,
invalid, or unavailable admission, hook, IPC, sandbox, or platform capability
fails closed.

The control plane and AGY worker must have distinct OS security identities. The
controller owns the project configuration, hooks, IPC endpoint, and workspace
root; the worker has only the minimum read/write ACLs needed for AGY and its
workspace. POSIX sticky-directory ownership or Windows DACLs must prohibit a
worker from replacing, deleting, changing permissions on, or redirecting
controller-owned hook/configuration material. The platform is unsupported for a
worker tier until this model is validated through native file tools, terminal
tools, subagents, nested AGY processes, and a proof that the worker hook does
not affect a global/unmanaged AGY session. No broad grant is issued before that
proof passes.

## Consequences

- Agyent gains a tenant-owned persistent AGY project mapping and an
  execution-admission contract. All direct runner fallbacks, including
  compaction, reflection/evolution, scheduler, and subagent paths, must move to
  that contract or use a deterministic non-AGY fallback.
- Project-local grants can support headless workers without broadening the
  user's global AGY grants. A broad native command grant remains acceptable only
  behind the hook, command capability policy, separate OS identity, and active
  sandbox.
- Worker provisioning requires platform-specific account/token, ACL, AGY
  configuration, sandbox, and lifecycle support. The operational cost is higher
  than a same-user process, and a platform can be unavailable until its POC
  passes.
- The design adds failure modes around project configuration, ACL provisioning,
  and sandbox probing. They stop the worker tier rather than silently executing
  with a global project, an ambient directory, or dangerous permissions.
- Project-local AGY grants do not isolate agyent databases or transcripts by
  themselves. Tenant-scoped storage and sandbox mount restrictions remain
  required work. They are release prerequisites alongside project provisioning,
  rather than a later hardening step after a worker is enabled.
- A turn-level approval protocol must return only `allow` or `deny` to AGY after
  a bounded, authenticated decision tied to the turn and action hash. It cannot
  rely on AGY's headless prompt or undocumented permission-override behaviour.

## Alternatives considered

- **Global AGY settings or global permission grants:** rejected because one
  agent's headless allowance becomes ambient authority for unrelated AGY work.
- **`--dangerously-skip-permissions`:** rejected for managed workers because it
  bypasses AGY's native permission prompt and weakens the defence-in-depth
  design.
- **Same OS identity with workspace hook files:** rejected because the worker
  can potentially modify its controller material; file mode `0600` does not
  distinguish parent and child sharing a UID.
- **Docker-only containment:** not selected because the requested platform set
  includes macOS and Windows and the design must be available without Docker.
  Native OS identities, ACLs, AGY sandboxing, and agyent policy are still held
  to the cross-platform acceptance matrix.
- **AGY project ID as the sole isolation mechanism:** rejected because it scopes
  native grants but does not bind a principal, constrain host data, or protect
  agyent control planes.

## Verification

Before accepting or enabling the decision, the repository must contain a
repeatable synthetic-canary integration suite for the matrix in
[AGY project-scoped security isolation](../agy-project-scoped-security-isolation.md#acceptance-poc-matrix).
It must run on macOS, Linux, and Windows for each pinned AGY version and cover
project selection, one `--add-dir`, sandbox visibility, controller/worker ACLs,
file and command traversal, all registered capability classes, native denials,
turn-bound hooks/IPC, end-to-end Telegram approval, direct-runner fallback
rejection, storage/transcript isolation, subagents, and lifecycle cleanup. The
acceptance change must include the registry migration, tenant-scoped SQL tests,
and a mid-turn rollback test that invalidates the turn and terminates the active
process tree before revoking only the affected project-local grant.
