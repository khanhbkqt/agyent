---
name: agyent-engineering-workflow
description: Route agyent repository debugging, bug fixes, behavior changes, refactors, documentation changes, and new features through mandatory evidence, review, documentation-sync, and verification graphs. Use for repository diagnosis or mutation; do not use for simple status questions or explanations unrelated to a concrete agyent task.
---

# agyent engineering workflow

Use this skill to select and execute one workflow graph without loading the other
graphs.

## Select one graph

- Diagnosis only: read the [debug graph](../../workflows/debug.json).
- Explicit defect fix: read the [bugfix graph](../../workflows/bugfix.json).
- Existing behavior, refactor, configuration, or documentation change: read
  the [change graph](../../workflows/change.json).
- New capability or public contract: read the
  [new-feature graph](../../workflows/new-feature.json).

First read the [workflow guide](../../workflows/README.md), then read only the
selected graph. The user's request and higher-level instructions override graph
guidance. Selection does not authorize a mutation or external action beyond the
user's request.

## Execute

1. Mirror graph nodes in the active task plan; only one implementation-path node
   is in progress at a time. Parallel review nodes may be grouped into one review
   stage, but each review output remains separate.
2. At each node, adopt its `system_prompt`, follow its guide, use the applicable
   listed skills/tools, and produce its named outputs.
3. Do not transition until every gate acceptance item is supported by evidence.
4. Run every required review branch. Reviews are read-only and independent from
   implementation. Blocking findings loop back through remediation, focused
   verification, documentation sync, and review.
5. Run the final gate only after convergence. Report the selected graph, review
   disposition, docs synchronization, commands run, and any remaining limitation.

For a live Telegram/session incident, also use `agyent-session-debugger` during
the evidence and reproduction nodes. Do not turn a diagnosis-only request into a
fix.
