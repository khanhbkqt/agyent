---
name: sqlite-inspect
description: >-
  Use this skill when the user asks to inspect SQLite database tables, examine schemas, or run read-only queries.
---

# SQLite Database Inspection

1. Call tool `sqlite_query_readonly` with `db_path` and a query (e.g. `SELECT name FROM sqlite_master WHERE type='table'`).
2. Format column headers and returned rows into a Markdown table.
