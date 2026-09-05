# Documentation instructions

These instructions apply below `docs/`.

- Declare `Document status`, `Code authority`, and `Last verified` near the top.
- Use only the statuses defined in `docs/README.md`.
- Describe current capability in Canonical/Reference docs. Keep unshipped work in
  Proposed docs and completed milestone records in Historical docs.
- Link to exact source files, migrations and tests instead of duplicating large
  structs, schemas or command output.
- Use repository-relative links. Mark diagrams as conceptual when nodes do not map
  directly to packages.
- Avoid unqualified performance guarantees. A measurement needs the command,
  environment, date and result artifact.
- Update the owning document with the code change; do not create another
  overlapping architecture overview.
- Documentation-only work still follows the `change` workflow graph. Its
  documentation node must record every affected authority and its review branches
  remain read-only.
- Run `make docs-check` during editing and `make verify` after review convergence.
