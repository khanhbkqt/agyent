# Built-in plugin instructions

These instructions apply below `builtin/plugins/`.

- A plugin requires a strict `plugin.json`. Optional capabilities are declared in
  `mcp_config.json`, `rules/AGENTS.md`, and `skills/<skill-name>/SKILL.md`.
- Manifest `name` must equal the plugin directory name. Skill `name` must equal
  its directory name. Tool names and arguments in skills/rules must match the
  server's `tools/list` schema exactly.
- Preserve APIS-4D identity: workspace, agent, session and user. Filesystem or
  stateful tools must enforce canonical path containment and tenant ownership
  inside the plugin.
- Rules contain only always-on, plugin-specific safety constraints. Workflows and
  conditional instructions belong in skills.
- Keep `SKILL.md` concise and discriminating. Put long recipes or tool catalogs in
  `references/` and link them from the skill.
- Do not claim guaranteed bypasses, success rates, performance or compatibility
  unless a maintained test/benchmark proves the claim.
- Validate Python syntax, plugin/context tests, and `make docs-check` after a
  plugin change.
