---
name: system-health
description: Read current host OS, CPU, memory, and disk telemetry with the system-diagnostics plugin. Use for resource or host-health questions; not for process mutation or application-specific incident diagnosis.
---

# System health

Call `get_system_health` once, then report the observation time and the metrics
relevant to the user's question. Distinguish capacity from current utilization and
flag missing/unavailable fields instead of inventing values.

This tool is read-only. Telemetry alone does not authorize killing processes,
deleting files, changing limits, or restarting services.
