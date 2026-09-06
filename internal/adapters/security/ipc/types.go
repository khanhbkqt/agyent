package ipc

// HookRequest is the wire format passed between agyent-hook and the IPC server.
type HookRequest struct {
	AuthToken      string       `json:"auth_token,omitempty"`
	TurnID         string       `json:"turn_id,omitempty"`
	HookType       string       `json:"hook_type"` // "pre", "post"
	ToolCall       HookToolCall `json:"toolCall"`
	StepIdx        int          `json:"stepIdx,omitempty"`
	ConversationID string       `json:"conversationId,omitempty"`
	WorkspacePaths []string     `json:"workspacePaths,omitempty"`
	TranscriptPath string       `json:"transcriptPath,omitempty"`
	Error          string       `json:"error,omitempty"`
}

// HookToolCall represents the tool execution descriptor from Antigravity.
type HookToolCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// HookResponse is the wire response returned to agyent-hook.
type HookResponse struct {
	Decision            string                 `json:"decision,omitempty"` // "allow", "deny", "ask"
	Reason              string                 `json:"reason,omitempty"`
	Overwrite           map[string]interface{} `json:"overwrite,omitempty"`
	PermissionOverrides []string               `json:"permissionOverrides,omitempty"`
}
