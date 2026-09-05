# System diagnostics plugin rules

- `get_system_health` is read-only telemetry. It does not authorize process,
  service, filesystem, or configuration mutations.
- Report observation time and unavailable fields; do not infer a root cause from a
  single telemetry snapshot without supporting evidence.
