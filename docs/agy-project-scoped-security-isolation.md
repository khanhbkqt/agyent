# AGY project-scoped security isolation

> **Document status:** Reference  
> **Code authority:** `internal/core/execution`, `internal/adapters/harness/agy`,
> `internal/adapters/security`, `internal/adapters/storage/sqlite`,
> `internal/adapters/subagent`, and `internal/config`  
> **Last verified:** 2026-09-06

This document describes the design and runtime implementation for isolating managed AGY guest
agents without Docker. It uses AGY's project-local permission store as a per-agent **native
grant scope**, while keeping agyent's authenticated hook/IPC path as the
authorization decision point. An AGY project ID is bound to execution identity,
filesystem boundary, tenant boundary, trusted registry, execution admission,
hook/IPC binding, and OS terminal sandbox.

The design applies to every managed worker, including subagents. The owner or
administrator agent is a separate, explicitly provisioned trust tier; it must
not receive a worker's identity or project grant by accident.

## Decision

For every managed agent, agyent will provision and own one dedicated **AGY
project ID**. The AGY runner will select that ID and request AGY's terminal
sandbox:

```text
authenticated turn
  -> agyent execution identity and policy
  -> execution admission and agent-to-AGY-project registry
  -> agy --project <agent-agy-project-id> --add-dir <canonical-workspace> --sandbox
  -> AGY native-permission gate and agyent PreToolUse/IPC gate
  -> sandboxed tool process only after both gates allow
```

The diagram is conceptual. The proposed registry and provisioning components do
not exist yet. Today the [AGY runner](../internal/adapters/harness/agy/runner.go)
and [subagent executor](../internal/adapters/subagent/executor.go) use
`--project outside-of-project`; the runner does not request `--sandbox`.

AGY's project-local configuration is a control-plane resource. It is stored
outside the agent workspace and is writable only by the agyent controller
identity or an administrator. The target OS trust boundary is explicit:

- The daemon/control plane runs under a controller identity. Each worker AGY
  process runs under a distinct unprivileged OS identity or Windows restricted
  token/SID. A same-UID daemon and worker cannot meet this requirement merely
  with mode `0600`.
- The controller owns the worker's AGY configuration, hook file, IPC
  socket/pipe directory, and workspace root. The worker receives only the read
  access that AGY needs to load the hook and project configuration; it receives
  no permission to replace, rename, change permissions on, or delete them.
- The workspace grants the worker write access to working files while preserving
  the controller-owned hook directory. On POSIX this requires a
  controller-owned sticky workspace root and a non-writable controller-owned
  `.agents` entry; on Windows it requires equivalent DACLs that deny delete and
  write-DAC/write-owner operations on controller material. The per-worker hook
  remains a controller-owned `<workspace>/.agents/hooks.json`, rather than a
  global AGY hook, so it cannot alter unrelated administrator or unmanaged AGY
  sessions.

The design is not enableable on a platform until AGY can load the controller
owned configuration under this model and native file tools, terminal commands,
subagents, and nested AGY processes cannot tamper with or avoid it after launch.
The platform POC must also prove the hook applies only to the intended worker
project and does not affect global sessions. A managed agent must never be able
to edit its project configuration, hook definition, security IPC endpoint, audit
database, or another agent's conversation storage.

Native AGY permission grants are a prerequisite for unattended execution, not
the authorization policy. They admit a tool call to AGY; agyent's PreToolUse
hook decides whether the identified agent may perform that concrete call. A
native grant is therefore paired with an active hook, a scoped workspace, and a
working sandbox. If any of those controls cannot be established, the turn is
rejected rather than falling back to `--dangerously-skip-permissions`.

## Evidence behind the decision

A manual POC was run on 2026-09-06 with AGY 1.1.27 on macOS arm64. It used only
temporary directories and synthetic canaries; it did not change agyent runtime
state or retain any user configuration changes.

| Check | Result | Design consequence |
| --- | --- | --- |
| A command grant in a dedicated AGY project completed when AGY was invoked with that project's ID. | Passed. | A per-agent AGY project can supply native headless permission without the dangerous flag. |
| The same grant was unavailable when the invocation selected `outside-of-project`. | Passed: AGY returned a cancelled result with a denied command action. | The runner must select the registered project ID on every path. |
| A project-local `command(*)` grant allowed a different command under that same project. | Passed. | Broad native grants are technically usable, but must be treated as a transport grant guarded by agyent policy and a sandbox. |
| A PreToolUse deny prevented `view_file` and `invoke_subagent`. | Passed. | The hook is a viable common enforcement point for current AGY. |
| A hook allow or permission override alone did not satisfy an absent native headless read permission. | Observed. | Dynamic hook replies must not be relied on to bypass AGY native permission checks. |
| A sandboxed `ps` could not observe a temporary host-process marker; unsandboxed AGY could. | Passed on macOS. | `--sandbox` is mandatory, subject to a per-platform feature gate. |

The exact project-file representation observed in this POC was AGY-version
specific. AGY documents project configuration precedence, native permissions,
headless soft denials, and hooks, but agyent must validate its provisioning
method against the pinned AGY version instead of treating an observed JSON shape
as a stable API. Relevant AGY sources are the [project configuration
changelog](https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md),
[permissions guide](https://antigravity.google/docs/cli/permissions/),
[headless guide](https://antigravity.google/docs/cli/headless/), and
[hook protocol](https://antigravity.google/docs/hooks).

The POC is evidence for the macOS/AGY-version pair only. Linux and Windows are
release gates, not assumed compatibility. In particular, the sandbox's process,
filesystem, network, shell, and configuration visibility must be measured on
each supported platform before a worker tier is enabled there.

## Security gaps to remove before migration

The existing architecture has useful pieces, but they are insufficient as the
native boundary described above:

- The current runner can still receive `DangerouslySkipPermissions` for a
  privileged caller. [Execution service policy](../internal/core/execution/service.go)
  must remove that managed-agent escape hatch.
- The current path-jail configuration resolves `.` while the daemon is starting.
  [Path-jail construction](../internal/adapters/security/pathjail/pathjail.go)
  therefore risks authorizing a daemon-current-directory path rather than the
  resolved agent workspace. Every allowed and protected root must instead be a
  canonical agent-scoped absolute path.
- The current hook file is provisioned inside the agent workspace. An agent that
  can write the file can remove its future hook. [Hook provisioning](../internal/adapters/security/provisioner.go)
  and workspace setup must move or protect that control-plane material before
  any broad native grant is issued.
- Command filtering must parse a command into executable and arguments, then
  enforce an allowlist/denylist over the complete command structure. Matching a
  prefix or a regular expression against a shell string accepts appended
  commands, substitutions, redirections, and interpreter indirection.
- Unknown AGY tools are currently allowed by the security manager. The target
  policy defaults them to deny until their argument schema, resource model, and
  tests are registered.
- AGY can report a headless native denial as a completed process result with
  `denied_actions`. [Batch parsing](../internal/adapters/harness/agy/parser.go)
  and streaming parsing must surface that state as a denied turn, not successful
  work.

These are remediation requirements, not claims that the linked implementation
already has the proposed behaviour.

## Target trust model and invariants

The following invariants define the worker tier.

1. A registry key `(tenant_id, agent_id, agent_generation, execution_host_id,
   agy_config_namespace_id)` maps to exactly one active AGY project ID and
   canonical workspace identity. The mapping is created by trusted provisioning,
   is stored with tenant ownership, and cannot be supplied by a channel payload,
   plugin, prompt, or subagent request. Ownership transfer, host/config-namespace
   change, agent recreation, grant revocation, or workspace re-provisioning
   invalidates the mapping and any linked AGY conversation.
2. Every managed AGY process receives an execution-service-issued admission
   containing principal, agent, workspace, AGY project ID, session, and turn.
   The runner rejects a missing or incomplete admission; it has no direct-runner
   fallback. It uses the resolved workspace in `AGYENT_AGENT_WORKSPACE`, selects
   the mapped project ID, passes exactly one `--add-dir <canonical-workspace>`,
   starts with `--sandbox`, and receives no dangerous-permission argument. This
   includes batch, streaming, continuation, scheduled, compaction, reflection,
   evolution, and subagent paths.
3. The workspace is an independent directory, never a nested child of another
   agent workspace. Provisioning rejects symlinks, junctions, aliases, and
   pre-existing cross-workspace hard links; the supported platform's canonical
   path rules are applied before every file operation.
4. Native grants exist only in that agent's AGY project. Global AGY grants are
   never widened to solve an individual worker's headless prompt.
5. A native grant may be broad enough for a worker's normal automation only
   while the hook/IPC control plane is immutable to that worker, the hook binds
   the active turn identity, and the sandbox health check passes. Hook or IPC
   failure denies the tool call. A broad `command(*)` grant is an exceptional,
   administrator-approved worker capability, never a preset default; it is not
   issued before the per-worker hook placement, global-session isolation, and
   nested-process POC passes.
6. agyent policy resolves every path relative to the canonical workspace,
   evaluates symlink targets, protects control-plane paths, and fails closed on
   an unknown path form. Windows validation includes drive-relative paths, UNC
   paths, alternate data streams, reparse points, and case-insensitive
   comparison.
7. The command policy evaluates every executable in a shell pipeline, compound
   statement, script, interpreter invocation, command substitution, and
   redirection. It validates `Cwd` as a canonical admitted-workspace path, gives
   no process an inherited external working directory, and explicitly handles
   script/interpreter/redirect paths where they are known. It does not make a
   security decision from a substring match; the OS sandbox contains opaque
   application-level file access.
8. A native AGY denial, a missing hook result, an invalid turn binding, or an
   unexpected tool schema is a failed/denied turn. Audit events retain the
   agent, turn, tool, decision reason, and policy version without retaining raw
   secrets or unrestricted tool output.
9. The sandbox is a defence-in-depth isolation boundary. On each supported
   platform it must prevent access to other agent workspaces, AGY configuration,
   transcripts, databases, host process tables, and service credentials. The
   policy layer remains responsible for a precise allow decision inside the
   sandbox.

10. Subagents obtain their execution admission through
    `internal/core/execution.Service`; they do not assemble a separate direct
    AGY command. A temporary bootstrap exception requires an explicit,
    documented contract with the same registry lookup, project selection,
    sandbox, hook, denied-action parsing, and turn registration. No such worker
    exception is accepted by this proposal.

11. A resumed or continued AGY conversation is accepted only after its stored
    `(tenant, agent, agent_generation, execution_host, agy_config_namespace,
    project, workspace)` binding matches the new execution admission. Existing
    guest conversation IDs are quarantined and re-created during migration; they
    are not reassigned after an ownership transfer or project revocation.

12. A worker cannot select `Unrestricted`, delegated configuration, a different
    policy version, an additional native grant, or a more privileged role. The
    controller selects the immutable worker policy and capability registry for
    the admission.

## Capability matrix

The native project grant admits a tool class. The agyent worker policy is the
following default-deny capability matrix. Every known tool is registered under
one class with an argument schema; an unregistered tool is denied. `admin` is a
separately authenticated controller role, not an AGY project grant inherited by
a worker.

| Tool class | Worker default | Worker allow condition | Admin/controller |
| --- | --- | --- | --- |
| File tools | Deny outside the canonical workspace. | Canonical path and resolved target are within the admitted workspace; control-plane paths remain denied. | Explicit maintenance workflow. |
| Terminal and scripts | Deny. | Parsed executable/arguments and every nested invocation match a worker capability; OS sandbox remains active. | Explicit break-glass workflow, audited. |
| Network egress | Deny, including loopback, link-local, private ranges, and metadata endpoints. | Capability names scheme, host/domain, port, DNS/IP policy, and request method; redirect and proxy targets are revalidated. | Explicit service policy. |
| MCP | Deny. | Registered server identity, tool/method, argument schema, tenant scope, and required secret handle match the capability. | Registered administrative server/method only. |
| Browser/profile tools | Deny host profiles, cookies, keychains, downloads outside workspace, and profile management. | Ephemeral controller-provisioned profile and permitted destination capability only. | Privileged profile action with audit. |
| Collaboration, tasks, schedules, and messages | Deny. | A policy authorizes the target tenant, agent, recipient, and task type; `invoke_subagent` produces a new execution admission. | Tenant-scoped administrative operation. |
| Configuration, database, transcript, and credential tools | Deny. | None for a worker. | Controller maintenance only. |

Network, browser, MCP, and collaboration tools must not be grouped under an
otherwise permissive generic tool class. Their capabilities are independent
from filesystem and terminal grants, and their schema/version changes require a
default-deny review. Every registered capability records its input schema,
resource scope, output-redaction rule, credential source/handle, policy version,
and test cases; worker-visible output never exposes the controller's credential
material.

## Native grants and approval flow

AGY's project-local grant should be deliberately minimal where a precise grant
is sufficient. Some workers need flexible shell automation; the POC confirmed
that a project-local wildcard command grant can enable this without a global
grant or dangerous CLI option. That is permitted only after the invariants above
are proved, because it makes the agyent hook and terminal sandbox the practical
guardrails for individual commands.

The approval sequence is consequently bounded and correlated:

```text
AGY invokes the tool authorization path
  -> native project grant and agyent PreToolUse policy are independent mandatory gates
  -> IPC authenticates active turn, principal, resource scope, and action hash
  -> for ask: agyent delivers an approval request and waits to a fixed deadline
  -> authenticated approval returns allow to AGY; every other outcome returns deny
  -> execution occurs only after both gates allow and the sandbox remains healthy
```

For `ask`, agyent owns the user-facing approval and its expiry/audit record. The
implementation must not assume `permissionOverrides` changes AGY's own native
permission decision: the macOS POC did not establish that behaviour. Instead,
the static native project grant admits the class and the hook supplies the
per-call decision. The approval record binds turn ID, principal, agent,
capability/action hash, requested scope, expiry, and a single-use decision. User
cancellation, expiry, channel-delivery failure, no authenticated response, IPC
timeout, or turn cancellation returns `deny` to AGY and invalidates any pending
approval. An end-to-end Telegram approval POC is required before rollout; AGY's
interactive headless prompt is not a fallback.

Project configuration changes are privileged operations. Provisioning must use
a documented AGY mechanism or a version-pinned, integration-tested format; lock
the project record; write atomically; validate by starting a harmless AGY turn;
and restore or quarantine on failure. It must not inspect, rewrite, or inherit a
user's unrelated global configuration.

## Delivery slices and release gates

### 0. Lock the compatibility contract

Pin the supported AGY version range and add a startup capability probe for
project selection, hooks, sandboxing, and structured denials. Record the OS,
AGY version, and probe result. A worker configuration is unavailable when its
platform cannot demonstrate the required properties; it never downgrades to the
dangerous option or to `outside-of-project`.

### 1. Correct the existing agyent enforcement boundary

Make allowed/protected paths agent-specific canonical paths. Make the hook
control plane read-only to workers and verify its integrity before each turn.
Replace shell-string matching with a complete command-policy model, default
unknown tools to deny, remove managed dangerous-permission forwarding, and make
all batch/streaming native denials visible to the execution service. This slice
also introduces the capability matrix above and the distinct controller/worker
OS identity and ACL model. It needs to prove that a hook modification cannot
survive an ordinary `execution.Service` turn boundary or a post-launch command,
file-tool, subagent, or nested-AGY attempt, and that the hook does not affect a
global/unmanaged AGY session.

### 2. Add agent-to-AGY-project ownership

Add a core-owned registry and execution-admission contract, plus a tenant-scoped
persistence model for the AGY project ID and provisioning state. The engine
resolves it alongside the existing execution identity; no caller chooses it.
`Runner.Execute` and `Runner.ExecuteStream` accept only an admission issued by
the execution service. Compaction, reflection/evolution, scheduler work, and
all existing fallback paths must resolve a mapped owner agent or use a
deterministic non-AGY fallback; they cannot invoke the runner without an
admission. Migrations and queries must scope the record in SQL by the
owner/agent relationship.

### 3. Provision and select the project on every runner path

Create or adopt only a verified empty AGY project for the mapped agent, install
the minimum native grants, and change the batch, stream, continuation, scheduler,
compaction, reflection/evolution, and subagent runners to use its project ID,
one canonical `--add-dir`, and `--sandbox`. The selected project ID, workspace
identity, sandbox state, and policy version are recorded as redacted audit
metadata.

### 4. Isolate data stores and control planes

Complete agent/tenant predicates for transcripts and audit data, remove direct
database/file tools over shared state, and ensure the process sandbox cannot
mount AGY configuration, another workspace, global transcript directories, or
service credentials. This work is separate from project-local AGY permissions:
the latter alone does not scope agyent's SQLite or transcript storage.

### Mandatory sequencing

Slices 0 through 4 are all release prerequisites. Slice 3 may provision a
project and execute harmless synthetic tests, but it must not issue a broad
native grant or enable a managed worker until the storage/control-plane proof in
slice 4 also passes. Before every continuation or resume, the execution service
validates the complete registry/conversation binding from invariant 11. Existing
guest conversation IDs are migrated by quarantine and fresh registration, never
by changing their owner or project in place.

### 5. Roll out by worker tier

Enable one non-privileged agent on a platform only after the acceptance matrix
passes. Keep an administrator-only migration/disable path. A failed health
check quarantines the worker: it invalidates active turn IDs, cancels pending
approvals, gracefully interrupts every active AGY process tree, forcibly stops
the process group or Windows Job Object after its deadline, verifies termination,
then revokes the affected project-local grant. It preserves audit evidence and
does not broaden native permissions.

## Acceptance POC matrix

Each row is required on macOS, Linux, and Windows for every pinned AGY version
before enabling that platform. Tests use synthetic canaries and a disposable
agent; no real user workspace, transcript, or secret is read.

| Area | Required proof |
| --- | --- |
| Project selection | The registered project grant works; the same operation with a different project ID or `outside-of-project` is denied. |
| Filesystem | Read/write/list/search works in the workspace and is denied for sibling/parent/master paths, absolute paths, symlinks, junctions, aliases, and pre-existing hard links. |
| Control plane | Attempts to modify hooks, project configuration, IPC sockets/pipes, policy files, databases, and transcript roots are denied; controller/worker ownership and ACLs prevent rename/delete/permission changes; a second ordinary service turn remains protected. |
| Commands | Policy rejects process discovery, process control, external `Cwd`, shell chaining, pipes, substitutions, redirects, scripts, `sh -c`, PowerShell, CMD, and interpreter bypasses. Approved worker commands still complete. |
| Capabilities | Every registered AGY tool class follows the matrix: files, terminal, network, MCP server/method, browser/profile, collaboration/task control, and control-plane data. Unknown tools and localhost, metadata, credential-bearing MCP/browser paths are denied. |
| Host isolation | Process listing cannot reveal a synthetic host marker; other workspaces, AGY state, credentials, and transcript stores are inaccessible. |
| Native/agyent interaction | A native deny becomes a denied agyent turn; hook deny, expired/cancelled approval, channel-delivery failure, invalid turn ID, and IPC outage all fail closed. An end-to-end Telegram approval returns only `allow` or `deny` to AGY. |
| Agent topology | Nested workspace discovery does not inherit parent instructions/configuration, and concurrent agents cannot exchange files or transcripts. |
| Lifecycle | Batch, stream, continuation, scheduled, compaction, reflection/evolution, cancellation, retry, and subagent paths all require an execution admission, reject direct-runner fallback, select the same scoped project and one canonical `--add-dir`, and clean up process groups/Job Objects. A mid-turn health failure terminates the active tree before grant revocation. |

The platform test harness belongs in the repository before rollout so these
properties become repeatable integration tests rather than a one-time manual
check.

## Rollback and residual risk

The first rollout should be reversible by disabling the affected worker tier and
revoking its project-local grants; it must not edit global AGY grants. AGY
project records are retained for audit until the relevant retention period has
ended, then deleted by a privileged maintenance process.

This design deliberately avoids Docker, but it does not claim equivalent
containment until the OS-specific acceptance matrix passes. A flaw in AGY's
sandbox, project configuration semantics, or hook lifecycle remains a material
dependency risk. The capability probe, pinned version policy, fail-closed
execution, immutable control plane, and cross-platform regression suite are the
controls that keep that dependency from silently widening a worker's reach.

## Related current documents and code

- [Security and guardrails](security-and-guardrails-architecture.md) describes
  the current authorization, security manager, hook, and IPC architecture.
- [AGY CLI harness](agy-cli-harness.md) describes the current process adapter.
- [Plugin isolation standard](plugin-developer-and-isolation-standard.md) defines
  the existing APIS-4D and plugin-isolation requirements.
- [ADR 0002](adr/0002-agy-project-scoped-worker-isolation.md) records the
  proposed cross-platform trust-boundary decision and alternatives.
- [AGY runner](../internal/adapters/harness/agy/runner.go),
  [security manager](../internal/adapters/security/manager.go),
  [path jail](../internal/adapters/security/pathjail/pathjail.go),
  [hook provisioner](../internal/adapters/security/provisioner.go), and
  [AGY parser](../internal/adapters/harness/agy/parser.go) are the primary code
  points for implementation.
