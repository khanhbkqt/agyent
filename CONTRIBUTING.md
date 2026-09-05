# Contributing to agyent

Thank you for improving `agyent`. The repository optimizes for secure local
execution, explicit tenant identity, predictable lifecycle behavior, and a clear
ports-and-adapters dependency direction.

## Before changing code

1. Read [AGENTS.md](AGENTS.md), [the documentation map](docs/README.md), and
   [the current architecture](docs/architecture.md).
2. Read the nearest scoped `AGENTS.md` for the package being changed.
3. Run `git status --short` and preserve unrelated work.
4. Find the current port, implementation, wiring, tests, and migration history.
5. Follow the risk-calibrated workflow in
   [docs/engineering-method.md](docs/engineering-method.md).

Historical milestone files under `docs/plans/` explain earlier intent; they are not
current implementation instructions.

## Toolchain

- Go 1.25, matching `go.mod`.
- CGO disabled for release builds.
- Python 3 for syntax-checking embedded Python plugins.
- `agy` only for live integration tests; ordinary unit tests are hermetic.

## Architecture rules

- Core packages do not import adapters. Domain packages do not contain channel,
  database, process, or MCP wire concerns.
- Interfaces are owned by `internal/core/ports`; adapters translate and implement
  them.
- AGY execution goes through `internal/core/execution.Service` after authorization
  and active-turn registration.
- Tenant identity and APIS-4D environment values are preserved end to end.
- SQLite remains pure Go, WAL-backed, and dual-pool. Released migrations are
  immutable.
- Prompt Levels 0–3 remain deterministic; daily/continuity context stays at Level
  4; warm turns use the continuation prompt.
- Components own and stop their goroutines, timers, queues, locks, and subprocess
  trees.

The complete contract is in [AGENTS.md](AGENTS.md).

## Development loop

First route the task through the
[engineering workflow graphs](docs/engineering-workflow-graphs.md): `debug`,
`bugfix`, `change`, or `new-feature`. Each graph requires evidence gates,
independent read-only review branches, documentation synchronization, and review
convergence before the final gate.

Run focused tests while iterating, then the standard gate:

```bash
make verify
```

Additional checks by risk:

```bash
go test ./...
go test -race ./...
go vet ./...
make build
make build-all
```

Use the race suite for concurrency, SQLite, scheduler, EventBus, debouncer,
streaming, cancellation, and process lifecycle changes. Cross-compile when editing
OS-specific process or filesystem code.

Live tests in `internal/core/engine/real_agy_test.go` require a working AGY
installation and should remain opt-in. Do not make the default suite depend on
network access, personal credentials, or local conversation state.

## Storage changes

Add the next numbered migration under
`internal/adapters/storage/sqlite/migrations/`. Include a down migration unless an
irreversible security hardening step is justified and documented. Test both a
fresh database and the relevant upgrade path. Scope tenant-owned operations in
SQL.

## Plugin and skill changes

Follow [the plugin isolation standard](docs/plugin-developer-and-isolation-standard.md)
and `builtin/plugins/AGENTS.md`.

- Manifest, skill, and directory names must agree.
- Tool names/arguments in `SKILL.md` and rules must match the MCP `tools/list`
  schema.
- Rules contain always-on plugin constraints; workflows belong in skills.
- Keep `SKILL.md` compact and move conditional recipes to `references/`.
- Run `make docs-check` and plugin/context tests.

## Documentation changes

Every file under `docs/` declares its status, code authority, and verification
date. Update current architecture/reference documents with the code change. Mark
unshipped designs Proposed and milestone records Historical. Use an ADR for
cross-cutting, security-sensitive, protocol, data, or hard-to-reverse decisions.

## Pull request checklist

- The change solves the requested problem through the real entry point.
- Dependency direction and security boundaries remain intact.
- Failure, cancellation, restart, and tenant-scope paths were considered.
- The selected workflow graph completed every required node and review branch.
- No unresolved P0, P1, or P2 review finding remains; P3 is fixed or accepted.
- Focused tests and `make verify` pass.
- Race/cross-platform tests were run when the risk calls for them.
- Documentation, help text, plugin schemas, and skills match the implementation.
- The diff does not contain secrets, generated runtime state, or unrelated edits.
