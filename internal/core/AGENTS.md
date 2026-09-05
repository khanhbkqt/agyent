# Core package instructions

These instructions apply below `internal/core/` and supplement the repository
root `AGENTS.md`.

- Do not import `agyent/internal/adapters` or `agyent/cmd`.
- Keep `domain` free of infrastructure dependencies. Domain types should express
  stable business concepts rather than Telegram, SQLite, AGY JSON, or MCP wire
  shapes.
- Put interfaces in `ports` only when the application core owns the contract.
- Authorization and tenant scoping are policy, not adapter convenience. Preserve
  `Principal`, `Resource`, role, agent, session, project, user and turn identity.
- AGY work routes through `execution.Service` after wiring. Any exception must be
  explicit, fail-closed, documented, and tested.
- Every goroutine, timer, queue and lock needs an owner and cancellation/shutdown
  path. Do not hold locks across network, channel or subprocess waits.
- Tests should use fakes at ports and assert externally visible state transitions,
  denied paths, event ordering, cancellation, or lifecycle behavior.

Run `make architecture-check` after changing imports and focused package tests
before the full repository gate.
