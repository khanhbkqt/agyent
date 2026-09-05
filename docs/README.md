# Documentation map

> **Document status:** Canonical
> **Code authority:** repository-wide
> **Last verified:** 2026-09-05

This page defines how to read and maintain the `agyent` documentation. Coding
agents should start with the root `AGENTS.md`, this map, and
`docs/architecture.md`; load component documents only when a task enters that
area.

## Status taxonomy

| Status | Meaning | May drive implementation? |
| --- | --- | --- |
| **Canonical** | Current system map or repository-wide truth | Yes, after confirming against code/tests |
| **Normative** | Required engineering or compatibility contract | Yes |
| **Reference** | Detailed explanation of an implemented subsystem | Yes, but code/tests win on drift |
| **Proposed** | Unshipped design or extension direction | Only when the user explicitly requests it |
| **Historical** | Milestone record, benchmark note, or superseded plan | No |

Every Markdown file under `docs/` must declare one of these statuses near the
top. Plans must be labeled Historical even when all checklist items are complete.

## Read path for coding agents

1. `../AGENTS.md` — mandatory repository contract and invariants.
2. `architecture.md` — current components, boundaries, and runtime flows.
3. `engineering-workflow-graphs.md` — mandatory debug, bugfix, change, and feature
   state machines.
4. `engineering-method.md` — how to assess risk, implement, verify, document, and record
   decisions.
5. One or two component documents directly related to the change.
6. `adr/README.md` when the change is cross-cutting or hard to reverse.

Do not preload the whole documentation tree. Detailed design files are large and
some intentionally preserve context from earlier milestones.

## Canonical and normative documents

| Document | Status | Use it for |
| --- | --- | --- |
| [Architecture](architecture.md) | Canonical | Current package map, control flow, ownership, boundaries |
| [Engineering workflow graphs](engineering-workflow-graphs.md) | Normative | Required task routing, node gates, cross-review and docs convergence |
| [Engineering method](engineering-method.md) | Normative | Change workflow, test matrix, review and documentation rules |
| [Plugin isolation standard](plugin-developer-and-isolation-standard.md) | Normative | APIS-4D identity, filesystem, process and MCP requirements |
| [Architecture decisions](adr/README.md) | Normative | ADR threshold, format and lifecycle |

## Implemented subsystem references

| Area | Document | Primary code authority |
| --- | --- | --- |
| Agent lifecycle | [Lifecycle and bootstrap](lifecycle-and-bootstrap.md) | `internal/core/engine`, `internal/adapters/workspace` |
| Message flow | [Message pipeline](message-pipeline.md) | `internal/adapters/channels/telegram`, `internal/core/debouncer`, `internal/core/engine` |
| Conversations | [Multi-conversation architecture](multi-conversation-architecture.md) | `internal/core/domain/conversation.go`, SQLite conversation repository |
| Projects | [Multi-project model](multi-project.md) | `internal/core/domain/project.go`, engine resolver |
| Context | [Context management](context-management-architecture.md) | context adapter, prompt bootstrap, compactor |
| Prompt prefix | [System meta-instructions](system-meta-instruction-architecture.md) | `internal/core/engine/bootstrap.go` |
| Models and effort | [Model/effort selection](model-and-effort-selection-architecture.md) | domain model capabilities, engine resolver, AGY parser |
| AGY process | [AGY harness](agy-cli-harness.md) | `internal/adapters/harness/agy` |
| Streaming | [AGY streaming protocol](agy-streaming-protocol.md) | stream parser, EventBus, Telegram throttler |
| Storage/config | [Storage and configuration](storage-and-config.md) | config package, SQLite store, migrations |
| Plugins | [Plugin system](plugin-system-architecture.md) | plugin manager, context resolver, MCP syncer |
| Security | [Security and guardrails](security-and-guardrails-architecture.md) | core auth/execution, security adapters and IPC |
| Ownership | [Agent ownership and multi-bot](agent-ownership-and-multi-bot-architecture.md) | auth policy, agent repository, Telegram adapter |
| Zalo channel | [Zalo channel adapter](zalo-channel-architecture.md) | Zalo adapter, composite mux, config and doctor |
| Subagents | [Subagent architecture](subagent-architecture.md) | subagent adapter, ports and repository |
| Evolution | [Self-learning and evolution](agent-self-learning-and-evolution-architecture.md) | evolution adapter and engine integration |

## Proposed designs

- [Extensible/multi-channel architecture](extensible-architecture.md) describes
  extension points. Telegram and Zalo are the currently wired production channel adapters.
- [Multi-account architecture](multi-account-architecture.md) is a proposal. No
  account-pool domain, port, adapter, or migration exists in the current tree.

## Historical material

`plans/` contains implementation plans and milestone sign-off records. Read
`plans/README.md` before using them. They preserve rationale but do not override
the current package map, schemas, commands, or tests.

The root `CHANGELOG.md` records release changes. `tokens_benchmark_report.md` is a
historical measurement artifact rather than an invariant.

## Ownership and update rules

When code changes, update the smallest authoritative document:

- Package ownership or end-to-end flow: update `architecture.md`.
- A repository rule or verification command: update `AGENTS.md` and
  `engineering-method.md` if applicable.
- A component contract: update that component's reference document.
- A hard-to-reverse decision: add or supersede an ADR.
- User installation/operation: update the root `README.md`.
- Skill/tool behavior: update the matching `SKILL.md`, plugin rules, and tool
  schema together.

Avoid copying full Go structs, SQL schemas, command help, or tool schemas into
multiple documents. Link to the source and document the behavior that must remain
stable.
