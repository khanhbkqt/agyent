# Subagent dispatcher rules

- Delegate only concrete independent work and preserve the parent's authorized
  agent, workspace, session and caller scope.
- A task ticket is not a completed result. Validate terminal state and artifacts
  before presenting completion.
- Do not use subagents to widen permissions or run conflicting concurrent edits in
  one shared workspace.
- Cancel only the exact task selected by the user or owning workflow.
