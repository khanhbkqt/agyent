# SQLite adapter instructions

These instructions apply below `internal/adapters/storage/sqlite/`.

- Use only `modernc.org/sqlite`; this repository is CGO-free.
- Use `writer()` for all mutations/transactions and `reader()` for reads.
- Keep WAL, `busy_timeout(5000)`, foreign keys and the one-writer/twenty-reader
  design intact.
- Add the next numbered `*.up.sql` migration for schema changes. Released
  migrations are immutable. Add a down migration unless the change is an
  explicitly documented irreversible hardening step.
- Store timestamps as Unix milliseconds and scan mixed legacy encodings with
  `FlexTime`.
- Tenant-owned records must be scoped in SQL by the appropriate agent, owner,
  session, project or principal key. Post-query filtering is not sufficient.
- Cover fresh databases, upgrades, constraints/indexes, rollback where supported,
  and concurrent read/write behavior affected by the change.
