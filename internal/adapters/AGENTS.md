# Adapter package instructions

These instructions apply below `internal/adapters/` and supplement the repository
root `AGENTS.md`.

- Adapters implement core-owned contracts and translate technology-specific
  payloads at the boundary. Do not move cross-adapter policy here.
- Add compile-time interface assertions for port implementations where practical.
- Preserve context cancellation, bounded waits, resource closure, and meaningful
  wrapped errors.
- Treat all channel, process, filesystem, MCP, hook and database inputs as
  untrusted. Canonicalize paths and bind operations to authenticated tenant/turn
  identity before acting.
- Keep logs structured and free of tokens, credentials and unredacted outputs.
- SQLite mutations use the writer pool; reads use the reader pool. Do not edit a
  released migration.
- Process adapters must preserve POSIX process groups and Windows Job Object
  cleanup.
- Add focused adapter tests for protocol errors and cancellation, not only happy
  paths.
