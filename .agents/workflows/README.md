# Engineering workflow graphs

These files define the mandatory execution graphs for repository work. They are
instruction artifacts, not additional authority: the user's request and
higher-level system/developer instructions always win, and a graph never grants
permission to mutate state that the user did not authorize.

## Routing

| Request | Graph | Mutation boundary |
| --- | --- | --- |
| Diagnose, investigate, explain a failure | `debug.json` | Read-only diagnosis |
| Diagnose and fix a defect | `bugfix.json` | Requested defect and its regression protection |
| Modify existing behavior, refactor, configuration, or docs | `change.json` | Requested change |
| Add a new capability or public contract | `new-feature.json` | Accepted feature scope |

When a request contains both diagnosis and an explicit fix request, select
`bugfix`. If a discovered issue is outside the selected graph's scope, report it
instead of silently expanding the task.

## Node execution contract

For every node:

1. Adopt `system_prompt` as the node-specific role, subordinate to the active
   user/system/developer instructions.
2. Confirm every named `input` from accumulated evidence.
3. Follow `guide`; use the listed `skills` and `tools` when applicable and
   available. Tool names describe capabilities and do not bypass approval or
   sandbox policy.
4. Do not edit when `may_edit` is `false`.
5. Produce every named `output` in the task plan, review report, test output, or
   final handoff.
6. Evaluate every `gate.acceptance` item. A failed blocking check prevents the
   transition.
7. Follow exactly one transition outcome, except when its target is an array: all
   listed review branches must run before their convergence node.

Do not create workflow-state files in the repository. Track node status in the
active task plan and retain command output or code references as evidence.

## Strict cross-review

Bug fixes, changes, and features require three independent review dimensions:

- architecture and dependency direction;
- correctness, regression coverage, and failure behavior;
- security, tenancy, concurrency, persistence, and lifecycle risk.

Debugging requires independent evidence and architecture reviews before the
diagnosis is reported. A reviewer must not edit the implementation it reviews.
Use a read-only subagent when delegation is available and authorized. Otherwise,
perform a separate fresh-context pass that begins from the diff and evidence,
not from the implementer's intended design.

Every finding uses the graph's `finding_schema`. `P0`, `P1`, and `P2` findings
block convergence. `P3` must be fixed or explicitly accepted with rationale.
After remediation, focused verification, documentation sync, and every review
branch run again. A reviewer saying “looks good” without examined files,
commands, and evidence does not satisfy the gate.

## Documentation synchronization

The documentation node is mandatory even when the correct result is “no docs
change”. Its output must identify each affected contract and either:

- update the authoritative document, skill, command help, migration note, or ADR;
  or
- record why no current document changes.

Historical plans are never updated to masquerade as current capability. New
cross-cutting or hard-to-reverse decisions follow the
[ADR standard](../../docs/adr/README.md).

## Final gate

The graph's `final_gate` may run only after review convergence. At minimum it
checks unresolved findings, focused verification, `make verify`, documentation
status, and the final diff. Risk-specific gates such as `go test -race ./...`,
fresh/upgrade migration tests, cross-builds, or security negative tests remain
mandatory when the changed subsystem requires them.
