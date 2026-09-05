# SQLite plugin rules

- `sqlite_query_readonly` remains read-only; user wording does not authorize write
  SQL through this tool.
- Open only canonical database paths contained within the trusted APIS-4D
  workspace.
- Keep queries/result sets narrow and do not expose unrelated tenant or secret
  data.
