# Engineering method

> **Document status:** Normative
> **Code authority:** `AGENTS.md`, tests, and component contracts
> **Last verified:** 2026-09-05

This document supplies risk and implementation guidance inside the mandatory
graphs defined by [Engineering workflow graphs](engineering-workflow-graphs.md).
The selected JSON graph controls step transitions, reviews, documentation sync,
and the final gate.

## 1. Select the graph and classify risk

Select exactly one graph before work begins:

| User intent | Workflow graph |
| --- | --- |
| Diagnose or explain without mutation | `debug` |
| Diagnose and explicitly fix a defect | `bugfix` |
| Modify existing behavior, refactor, config, or docs | `change` |
| Add a new capability or public contract | `new-feature` |

Use `.agents/skills/agyent-engineering-workflow/SKILL.md` to load only the chosen
graph. Then identify the technical risk:

| Change type | Typical scope | Main risk |
| --- | --- | --- |
| Local implementation | One package, no contract change | Regression in local behavior |
| Port/domain change | Domain plus one or more adapters | Dependency reversal or incompatible implementations |
| Persistence change | Domain, port, migration, repository | Data loss, cross-tenant leakage, upgrade failure |
| Runtime flow change | Engine, execution, channel, scheduler | Concurrency, duplicate execution, lifecycle leaks |
| Security change | Auth, execution, IPC, path/network/DLP | Fail-open behavior or privilege widening |
| Prompt/context change | Resolver, bootstrap, compactor | Prefix instability or lost dynamic context |
| Plugin/skill change | Manifest, MCP schema, rules, skill | Tool mismatch, unsafe scope, undiscoverable guidance |
| Documentation only | Markdown and validation tooling | Incorrect authority or stale links |

Escalate verification when one change spans several rows. Risk classification
does not permit skipping graph nodes; it selects additional tests and reviewers'
attention.

## 2. Establish the contract from evidence

Use this order:

1. Find the public entry point or composition wiring.
2. Find the core-owned port and domain types.
3. Find every implementation and compile-time interface assertion.
4. Find focused tests and migration history.
5. Read the component reference document.

Write down the preconditions, output/state change, failure mode, cancellation
behavior, authorization decision, and tenant key. If one is unknown, inspect more
code before designing the change.

## 3. Design within the dependency direction

Prefer these placements:

- Domain vocabulary and state: `internal/core/domain`.
- Interface needed by policy/orchestration: `internal/core/ports`.
- Authorization or application policy: `internal/core/auth` or a focused core
  service.
- Turn orchestration: `internal/core/engine`.
- Authorized AGY execution: `internal/core/execution`.
- Technology-specific behavior: `internal/adapters/...`.
- Object graph and CLI flags: `cmd/agyent`.

Do not let Telegram types, SQLite rows, JSON-RPC envelopes, or AGY parser details
become domain types. Translate at adapter boundaries.

For a cross-cutting change, draw the intended path in one line before coding:

```text
input -> domain contract -> policy -> port -> adapter -> event/output
```

## 4. Implement in reviewable slices

Keep each slice buildable when practical:

1. Add or adjust domain/port contracts.
2. Add focused tests that express meaningful behavior or reproduce the defect.
3. Implement core policy/orchestration.
4. Implement adapters and migration work.
5. Wire the composition root.
6. Update user/agent documentation and skills.

Preserve unrelated edits. Avoid broad renames or formatting mixed with behavioral
work unless the user requested them.

## 5. Verification matrix

Run focused tests while iterating. Before handoff, run `make verify`.

| Area changed | Minimum focused verification | Additional gate when relevant |
| --- | --- | --- |
| Domain/ports | `go test ./internal/core/domain/... ./internal/core/ports/...` | All implementers compile |
| Engine/context | `go test ./internal/core/engine/... ./internal/adapters/context/...` | Prefix/continuation assertions |
| SQLite/migrations | `go test ./internal/adapters/storage/sqlite/...` | Fresh DB and upgrade path; race suite for concurrency |
| Security/auth/execution | `go test ./internal/core/auth/... ./internal/core/execution/... ./internal/adapters/security/...` | Negative authorization and fail-closed paths |
| Telegram/streaming | `go test ./internal/adapters/channels/telegram/... ./internal/adapters/harness/agy/...` | Cancellation, chunking, error delivery, race suite |
| Scheduler/subagents | Corresponding core + adapter packages | State transitions, timeout/cancel, restart reconciliation |
| Plugins/skills | Plugin/context tests plus `make docs-check` | MCP `tools/list` names match skill instructions |
| Cross-platform process code | Package tests plus `make build-all` when requested | POSIX and Windows compile paths |
| Documentation only | `make docs-check` | Inspect rendered diagrams if changed |

Do not add tests that merely assert comments, exact prose, or implementation
details. Prefer state transitions, denied paths, protocol inputs/outputs, and
observable lifecycle behavior.

## 6. Review checklist

Review the diff from five angles:

### Architecture

- Does core import an adapter?
- Did technology-specific data leak into domain contracts?
- Is wiring confined to the composition root?

### Security and tenancy

- Which principal is acting on which resource?
- Are agent, workspace, session, project, user, conversation, and turn identities
  preserved?
- Are queries scoped before rows leave SQLite?
- Is the failure path still fail-closed?

### Concurrency and lifecycle

- Who owns each goroutine, channel, lock, timer, subprocess, and temporary state?
- What happens on context cancellation, timeout, duplicate delivery, or shutdown?
- Can external I/O occur while a lock is held?

### Compatibility and data

- Is an existing migration being edited?
- Can old timestamps/configuration values still be read?
- Are CLI, slash command, JSON-RPC, and stream protocol changes backward
  compatible or explicitly versioned?

### Documentation

- Is the document status correct?
- Are claims implemented and measurable?
- Did a skill or rule retain a stale tool name or argument?
- Does a plan accidentally read like current capability?

## 7. Definition of done

A change is done when:

- The requested outcome works through the actual entry point.
- Architecture and security invariants remain true.
- Focused tests and `make verify` pass.
- Relevant current documentation and skill guidance match the implementation.
- Remaining limitations are explicit and are not disguised as implemented
  features.

## 8. Architecture decision records

Create an ADR when a change is hard to reverse, changes a public/runtime protocol,
alters dependency direction, changes persistence semantics, changes a security
boundary, or intentionally accepts a significant tradeoff. Follow
`adr/README.md`; do not create ADRs for routine refactors or obvious bug fixes.
