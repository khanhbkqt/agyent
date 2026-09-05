# ADR 0001: Mandatory engineering workflow graphs

> **Document status:** Normative
> **Code authority:** `.agents/workflows/*.json`, `scripts/check_workflows.py`, and `AGENTS.md`
> **Last verified:** 2026-09-05

- Status: Accepted
- Date: 2026-09-05
- Owners: agyent maintainers
- Supersedes: none
- Superseded by: none

## Context

Repository guidance previously described sound engineering practices but did not
encode mandatory task states or prove that diagnosis, review, documentation, and
verification could not be skipped. Debugging also needed a distinct read-only
boundary so a diagnosis request could not silently become remediation.

Instructions spread across AGENTS files and skills are sensitive to ambiguity and
drift. The repository needs one machine-checkable process contract while still
allowing risk-specific tools and preserving the user's authority over mutations.

## Decision

All concrete repository diagnosis and modification work uses exactly one versioned
JSON graph: `debug`, `bugfix`, `change`, or `new-feature`.

Every node declares a subordinate system prompt, guide, skills, tools, inputs,
outputs, mutation capability, acceptance gate, and transitions. Debug is entirely
read-only. Mutating graphs converge three independent review dimensions before a
terminal gate; debug converges evidence and architecture reviews. P0 through P2
findings block completion, and remediation repeats verification, docs sync, and
all review branches.

The graphs do not grant authority. User and higher-level instructions remain
superior, unavailable tools may not be assumed, and external/deployment actions
still require their ordinary authorization.

## Consequences

- Agents receive deterministic routing and completion criteria for common work.
- Review and documentation become explicit state transitions rather than final
  reminders.
- Static validation catches unreachable steps, review bypasses, invalid mutation
  nodes, missing artifacts, and unknown project skills.
- Small changes carry more process overhead because the user explicitly requires
  every task to traverse the compliance graph.
- Review independence depends on available authorized delegation; otherwise a
  separate fresh-context pass is the defined fallback.

## Alternatives considered

- **Checklist-only AGENTS guidance:** simpler, but cannot validate reachability,
  mutation boundaries, or review convergence.
- **One universal linear workflow:** less duplication, but conflates diagnosis
  with remediation and omits feature/defect-specific evidence.
- **Runtime Go workflow engine:** stronger dynamic enforcement, but unnecessarily
  couples contributor process to the gateway runtime and expands production risk.
- **Mandatory subagents:** stronger independence, but unavailable in some agent
  environments and could imply delegation authority not granted by the user.

## Verification

- `python3 scripts/check_workflows.py`
- `python3 -m unittest scripts/check_workflows_test.py`
- `make workflow-check`
- `make verify`

The normative design and diagrams are maintained in
[Engineering workflow graphs](../engineering-workflow-graphs.md).
