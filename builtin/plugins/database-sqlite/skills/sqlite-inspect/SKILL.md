---
name: sqlite-inspect
description: Inspect an authorized local SQLite database with read-only schema, metadata, integrity, or SELECT queries. Do not use for writes, migrations, or databases outside the active workspace.
---

# SQLite inspection

Use `sqlite_query_readonly` with an absolute `db_path` inside the authorized
workspace and one read-only query.

Before calling the tool:

- Confirm which database the user means when more than one plausible file exists.
- Prefer narrow `SELECT`, `WITH ... SELECT`, or read-only `PRAGMA` queries with a
  useful `LIMIT`.
- Do not include secrets or unrelated tenant rows in the query/result.
- Do not attempt `INSERT`, `UPDATE`, `DELETE`, DDL, `ATTACH`, writable pragmas, or
  extension loading. This plugin remains read-only even if a user asks it to
  write; use an explicitly authorized implementation path for mutations.

Present the relevant columns and row count. For large results, summarize and show
a small representative table rather than dumping every row.
