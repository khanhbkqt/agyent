# Architecture decision records

> **Document status:** Normative
> **Code authority:** accepted ADRs and the implementation they reference
> **Last verified:** 2026-09-05

ADRs record decisions whose rationale would otherwise be lost. They do not replace
component documentation.

## When an ADR is required

Write an ADR for a decision that changes one or more of:

- Dependency direction or package ownership.
- A persisted schema or migration policy.
- A public CLI, stream, MCP, hook, or event contract.
- Authentication, authorization, tenant isolation, or fail-open/fail-closed
  behavior.
- Process lifecycle, cancellation, or cross-platform execution strategy.
- Prompt-prefix ordering or conversation compatibility.
- A major dependency or an intentionally accepted operational tradeoff.

Routine refactors, localized bug fixes, and reversible implementation details do
not need an ADR.

## Lifecycle

Use sequential four-digit names: `0001-short-title.md`. Status is one of
`Proposed`, `Accepted`, `Superseded`, or `Rejected`.

An accepted ADR is immutable except for typo/link fixes. Change a decision by
adding a new ADR whose `Supersedes` field points to the old one; update the old
record's status to `Superseded` and add a reciprocal link.

## Required format

```markdown
# ADR NNNN: Decision title

- Status: Proposed
- Date: YYYY-MM-DD
- Owners: team or role
- Supersedes: none
- Superseded by: none

## Context

What forces a decision? Include constraints and evidence.

## Decision

State the chosen rule and its scope.

## Consequences

Record benefits, costs, failure modes, and operational impact.

## Alternatives considered

List realistic alternatives and why they were not chosen.

## Verification

Point to tests, metrics, migrations, or review gates that keep the decision true.
```

Link accepted ADRs from `docs/README.md` or the owning component document when
they become part of the current architecture.
