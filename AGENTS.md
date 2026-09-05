# AGENTS.md — agyent engineering contract

This file is the shortest reliable entry point for coding agents. Read it before
changing the repository, then open only the component documents needed for the
task.

## 1. Instruction and documentation authority

Apply instructions in this order:

1. The user's current request.
2. The nearest applicable `AGENTS.md` from the repository root to the file being
   changed.
3. Canonical and normative documents listed in `docs/README.md`.
4. Current code, tests, schemas, and generated command help.
5. Reference documents.
6. Proposed and historical documents under `docs/plans/`.

Code and executable tests are authoritative when a reference document has
drifted. Do not implement a roadmap item merely because a historical plan
describes it. When behavior changes, update the relevant canonical/reference
document in the same change.

## 2. Start here

Before editing:

1. Run `git status --short`; preserve unrelated user changes.
2. Read `docs/README.md` and `docs/architecture.md`.
3. Use `.agents/skills/agyent-engineering-workflow/SKILL.md` to select exactly one
   workflow graph for debug, bugfix, change, or new-feature work.
4. Trace the current behavior from composition root to port to implementation and
   tests. Prefer `rg` and `go list` over assumptions from prose.
5. State the contract and invariants affected by the change.
6. Choose the smallest verification set from `docs/engineering-method.md`.

For runtime session hangs, timeouts, or SQLite triage, use
`.agents/skills/agyent-session-debugger/SKILL.md`.

## 3. Mandatory workflow routing

Every concrete repository diagnosis or mutation follows one validated graph in
`.agents/workflows/`:

- `debug.json`: diagnosis only; every node is read-only.
- `bugfix.json`: reproduce, prove root cause, fix, verify, sync docs, review.
- `change.json`: map impact and compatibility before modifying existing behavior.
- `new-feature.json`: acceptance criteria, architecture and ADR decision before
  implementation.

Each graph node has a subordinate system prompt, guide, named skills/tools,
inputs, outputs, mutation capability, acceptance gate, and transitions. Mirror
the selected nodes in the active task plan and do not skip a gate. A graph never
expands user authorization.

Bugfix, change, and feature graphs require independent read-only reviews for
architecture, correctness, and security/systems risk. Debug requires independent
evidence and architecture review. `P0`, `P1`, and `P2` findings block completion;
remediation loops through verification, documentation sync, and full re-review.
Follow `docs/engineering-workflow-graphs.md` and the graph guide.

## 4. What the system is today

`agyent` is a Go 1.25 personal-assistant gateway. The current production channel
adapter is Telegram. Other channels are extension points behind `ChannelPort` and
`CompositeChannelMux`; do not describe them as implemented unless an adapter
exists under `internal/adapters/channels/`.

The primary runtime path is:

```text
Telegram adapter
  -> canonical message + admission checks
  -> debouncer
  -> core engine + session lock
  -> context/session/agent/project resolution
  -> policy engine
  -> execution service (authorized chokepoint)
  -> AGY harness
  -> stream events
  -> EventBus
  -> Telegram delivery throttler
```

The composition root is `cmd/agyent/run.go`. It wires SQLite, EventBus, locks,
AGY harness, Telegram, authorization, execution, security IPC, context/plugins,
workspace management, scheduler, subagents, and evolution.

## 5. Package boundaries

The repository follows ports and adapters with a pragmatic application core:

- `internal/core/domain`: domain values and state only. It must not import
  adapters, CLI packages, or infrastructure libraries.
- `internal/core/ports`: interfaces owned by the core. It may depend on domain;
  it must not depend on adapter implementations.
- `internal/core/{auth,concurrency,debouncer,engine,eventbus,execution,retry,scheduler}`:
  application policy and orchestration. These packages must not import
  `internal/adapters`.
- `internal/adapters`: implementations for Telegram, AGY, SQLite, security, MCP,
  plugins, subagents, context, evolution, and workspaces. Adapters depend inward
  on domain/ports.
- `cmd/agyent`: the composition root and CLI surface. Wiring belongs here; domain
  policy does not.
- `builtin/plugins`: embedded capability bundles, not Go core packages. Each
  plugin is independently validated and runs through MCP/security boundaries.

Add or change behavior in this order when all layers are involved:

```text
domain contract -> core-owned port -> core policy/orchestration
                -> adapter implementation -> cmd wiring -> tests/docs
```

Do not create an interface beside its only adapter solely for symmetry. A port is
justified when the core owns the contract or a test/implementation boundary needs
it.

## 6. Non-negotiable invariants

### 6.1 Authorized execution and tenant identity

- All AGY turns must pass through `internal/core/execution.Service` unless a
  documented bootstrap exception exists.
- Authorization is fail-closed when the policy engine is absent.
- Preserve the execution identity fields: principal, agent, workspace, project,
  session key, user ID, conversation ID, and turn ID.
- Preserve APIS-4D environment propagation:
  `AGYENT_AGENT_WORKSPACE`, `AGYENT_AGENT_NAME`, `AGYENT_SESSION_KEY`, and
  `AGYENT_USER_ID`. `AGYENT_TURN_ID` binds hook decisions to the active turn.
- Never let a channel callback, plugin payload, or subagent choose a broader
  resource scope than the authenticated principal owns.
- Security hook/IPC failures remain fail-closed. Never weaken a preset as a
  fallback.

### 6.2 Workspace and subprocess isolation

- AGY processes run with `--project outside-of-project`; pass `--add-dir` only for
  the resolved workspace.
- Validate and canonicalize paths before access. Account for symlinks and Windows
  case/UNC/ADS behavior in security-sensitive code.
- On POSIX, process trees use their own process group. On Windows, use Job Objects.
- Graceful interruption precedes forced termination. Always remove active stream
  registrations on every exit path.

### 6.3 SQLite

- Keep the project CGO-free. Use `modernc.org/sqlite`; never add
  `github.com/mattn/go-sqlite3`.
- Preserve WAL, `busy_timeout(5000)`, foreign keys, and the dual-pool design:
  one writer connection and up to twenty reader connections.
- Writes use the writer pool; reads use the reader pool.
- Add schema changes as the next immutable `*.up.sql` migration. Do not edit an
  already released migration. A down migration is preferred; a documented
  irreversible security migration is permitted.
- Persist timestamps as Unix milliseconds. Use `FlexTime` when scanning values
  that may come from legacy integer or textual encodings.
- Scope tenant-owned queries in SQL, not only after loading rows in Go.

### 6.4 Prompt and context ordering

The initial prompt prefix is ordered and deterministic:

1. Level 0: system runtime foundation.
2. Level 1: global `IDENTITY.md`, `SOUL.md`, `USER.md`, `MEMORY.md`, `AGENTS.md`.
3. Level 2: workspace directives and active plugin rules.
4. Level 3: sorted progressive skill index.
5. Level 4: attachments, temporal marker, continuity digest, daily memory, user
   message.

Daily memory and continuity digests stay in Level 4. Continuation turns use
`ComposeContinuationPrompt`; do not serialize the static prefix again. Any new
map/slice added to the prefix must be sorted deterministically.

### 6.5 Concurrency and lifecycle

- Session work is serialized with the FIFO lock manager.
- Command fast paths and append-mode steering must not create two active turns for
  one session.
- Queue/channel sends need an explicit bounded or cancellation behavior.
- Goroutines started by a component must be owned by a context or stop method.
- Avoid holding a lock during network I/O, channel delivery, or subprocess waits.
- Event handlers must choose synchronous versus asynchronous delivery based on
  whether failure must stop the caller. Do not move authorization to an
  asynchronous event.

### 6.6 Logging and secrets

- Use `log/slog` with stable snake_case keys such as `session_key`, `agent_name`,
  `turn_id`, and `error`.
- Never log bot tokens, credentials, raw environment secrets, approval tokens, or
  unredacted tool output.
- Return errors with operation context and preserve causes using `%w`.

## 7. Change playbooks

### Add a channel

Implement `ChannelPort`, normalize to `CanonicalMessage`, route output using
`TargetContext`, add provider-aware principal mapping and tests, then wire it in
the composition root. Authorization policy remains in core/auth and execution;
channel filters are admission checks, not the final authority.

### Add a slash or CLI command

Keep parsing thin. Put reusable policy in a core service, apply RBAC before state
changes, test aliases/error cases, and update generated/help documentation.

### Add storage behavior

Change domain/port contracts first, add the migration if needed, implement scoped
queries on the correct pool, and cover fresh migration plus upgraded-schema paths.

### Add an MCP plugin or skill

Follow `docs/plugin-developer-and-isolation-standard.md` and
`builtin/plugins/AGENTS.md`. Keep `SKILL.md` concise and route conditional detail
to `references/`. Tool names and schemas must match `tools/list` exactly.

### Change prompt assembly

Update `bootstrap.go`, resolver ordering, and targeted prefix/continuation tests.
Treat ordering changes as compatibility and cost-sensitive changes.

## 8. Verification

Use the narrowest meaningful checks during iteration, then run the repository gate
before handoff:

```bash
make verify
```

Useful focused commands:

```bash
go test ./internal/core/engine/...
go test ./internal/adapters/storage/sqlite/...
go test ./internal/adapters/security/... ./internal/core/auth/... ./internal/core/execution/...
go test ./internal/adapters/plugin/... ./internal/adapters/context/...
go test ./...
go test -race ./...
go vet ./...
```

`make verify` checks documentation/skills, workflow graph integrity, architecture
dependency direction, plugin syntax, and the short Go suite. Run
`go test -race ./...` for concurrency, lifecycle, SQLite, EventBus, debouncer,
scheduler, or streaming changes.

## 9. Documentation maintenance

- Every file under `docs/` declares a status from the taxonomy in
  `docs/README.md`.
- Canonical docs explain the current system. Normative docs define rules.
  Reference docs explain implemented subsystems. Proposed docs describe unshipped
  designs. Historical docs record completed planning work.
- Use repository-relative links and exact package/file names.
- Avoid volatile benchmark claims in normative instructions. Put measured results,
  hardware, date, and command in a benchmark report.
- Prefer links to migrations, structs, and tests over copying large code/schema
  blocks that will drift.
- Record cross-cutting or hard-to-reverse decisions using `docs/adr/README.md`.

## 10. Runtime diagnostics

Start with read-only evidence:

```bash
agyent doctor --skip-network
go run ./scripts/debug_session.go --session '<session-key>'
```

Relevant state is in `~/.agyent/agyent.db`, structured daemon logs, and the AGY
conversation transcript under
`~/.gemini/antigravity/brain/<conversation-id>/.system_generated/logs/transcript.jsonl`.
Do not run `/force_unlock`, reset a conversation, change a security preset, or
modify the database unless the user requested remediation and the evidence points
to that action.
