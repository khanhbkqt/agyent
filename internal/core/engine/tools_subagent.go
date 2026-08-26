package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agyent/internal/core/domain"
	"agyent/internal/core/ports"
)

// SubagentToolDefinitions returns standard AGY-compatible tool definitions for subagent dispatching.
func SubagentToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "dispatch_subagent",
			"description": "Delegate a long-running, multi-file, or heavy research task to a background sub-agent without blocking the main track.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title": map[string]any{
						"type":        "string",
						"description": "Concise summary title of the task",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Comprehensive instructions for the sub-agent",
					},
					"agent_name": map[string]any{
						"type":        "string",
						"description": "Target persona ('researcher', 'coder', 'agyent')",
						"default":     "agyent",
					},
					"model": map[string]any{
						"type":        "string",
						"enum":        []string{"flash", "flash_lite", "pro"},
						"description": "Model tier to execute the subagent",
						"default":     "flash",
					},
					"effort": map[string]any{
						"type":        "string",
						"enum":        []string{"low", "medium", "high"},
						"description": "Reasoning effort for the subagent",
						"default":     "low",
					},
					"workspace_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"share", "scratch", "persona"},
						"description": "Workspace isolation mode",
						"default":     "share",
					},
					"callback_mode": map[string]any{
						"type":        "string",
						"enum":        []string{"notify_user", "callback_main", "silent"},
						"description": "Completion reporting mode",
						"default":     "notify_user",
					},
				},
				"required": []string{"title", "prompt"},
			},
		},
		{
			"name":        "check_subagent_progress",
			"description": "Query real-time progress, active step, duration, and recent tool logs for a sub-agent task.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "Sub-agent Task ID (e.g. 'task-5b1297cd')",
					},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "send_subagent_input",
			"description": "Send an answer or directive to a sub-agent currently in WAITING_FOR_INPUT status to resume its execution.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "Sub-agent Task ID",
					},
					"input": map[string]any{
						"type":        "string",
						"description": "Answer or follow-up instruction to resume the task",
					},
				},
				"required": []string{"task_id", "input"},
			},
		},
		{
			"name":        "cancel_subagent_task",
			"description": "Forcefully terminate a running background sub-agent task and clean up its process tree.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{
						"type":        "string",
						"description": "Sub-agent Task ID to cancel",
					},
				},
				"required": []string{"task_id"},
			},
		},
		{
			"name":        "list_subagents",
			"description": "List all active and recent background sub-agent tasks for the current session.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of tasks to return (default: 10)",
						"default":     10,
					},
				},
			},
		},
	}
}

// HandleSubagentToolCall processes an internal subagent tool invocation from the LLM turn.
func HandleSubagentToolCall(
	ctx context.Context,
	dispatcher ports.SubagentDispatcherPort,
	sessionKey string,
	parentConvID string,
	toolName string,
	params map[string]any,
) (string, error) {
	if dispatcher == nil {
		return "", errors.New("subagent dispatcher is not initialized")
	}

	switch toolName {
	case "dispatch_subagent":
		title, _ := params["title"].(string)
		prompt, _ := params["prompt"].(string)
		if strings.TrimSpace(title) == "" || strings.TrimSpace(prompt) == "" {
			return "", errors.New("title and prompt are required for dispatch_subagent")
		}

		agentName, _ := params["agent_name"].(string)
		model, _ := params["model"].(string)
		effort, _ := params["effort"].(string)
		wsMode, _ := params["workspace_mode"].(string)
		cbMode, _ := params["callback_mode"].(string)

		task := domain.SubagentTask{
			ParentSessionKey:     sessionKey,
			ParentConversationID: parentConvID,
			AgentName:            agentName,
			Title:                title,
			Prompt:               prompt,
			Model:                model,
			Effort:               effort,
			WorkspaceMode:        wsMode,
			CallbackMode:         domain.SubagentCallbackMode(cbMode),
		}

		taskID, err := dispatcher.DispatchTask(ctx, task)
		if err != nil {
			return "", fmt.Errorf("failed to dispatch task: %w", err)
		}

		res := map[string]any{
			"task_id": taskID,
			"status":  "PENDING",
			"message": fmt.Sprintf("🚀 Task %s successfully dispatched to background worker.", taskID),
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "check_subagent_progress":
		taskID, _ := params["task_id"].(string)
		if strings.TrimSpace(taskID) == "" {
			return "", errors.New("task_id is required for check_subagent_progress")
		}

		task, err := dispatcher.GetTask(ctx, taskID)
		if err != nil {
			return "", fmt.Errorf("failed to get task %s: %w", taskID, err)
		}

		res := map[string]any{
			"task_id":          task.ID,
			"title":            task.Title,
			"agent_name":       task.AgentName,
			"status":           task.Status,
			"current_step":     task.CurrentStep,
			"current_tool":     task.CurrentTool,
			"progress_message": task.ProgressMessage,
			"pending_question": task.PendingQuestion,
			"result_summary":   task.ResultSummary,
			"duration_seconds": task.DurationSeconds,
			"total_tokens":     task.Usage.TotalTokens,
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "send_subagent_input":
		taskID, _ := params["task_id"].(string)
		input, _ := params["input"].(string)
		if strings.TrimSpace(taskID) == "" || strings.TrimSpace(input) == "" {
			return "", errors.New("task_id and input are required for send_subagent_input")
		}

		if err := dispatcher.SendTaskInput(ctx, taskID, input); err != nil {
			return "", fmt.Errorf("failed to send input to task %s: %w", taskID, err)
		}

		res := map[string]any{
			"task_id": taskID,
			"status":  "RESUMING",
			"message": fmt.Sprintf("✅ Input sent to task %s. Worker resumed execution.", taskID),
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "cancel_subagent_task":
		taskID, _ := params["task_id"].(string)
		if strings.TrimSpace(taskID) == "" {
			return "", errors.New("task_id is required for cancel_subagent_task")
		}

		if err := dispatcher.CancelTask(ctx, taskID); err != nil {
			return "", fmt.Errorf("failed to cancel task %s: %w", taskID, err)
		}

		res := map[string]any{
			"task_id": taskID,
			"status":  "CANCELLED",
			"message": fmt.Sprintf("🛑 Task %s has been cancelled and its process tree terminated.", taskID),
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "list_subagents":
		limit := 10
		if l, ok := params["limit"].(float64); ok && int(l) > 0 {
			limit = int(l)
		}

		tasks, total, err := dispatcher.ListTasks(ctx, sessionKey, limit, 0)
		if err != nil {
			return "", fmt.Errorf("failed to list tasks: %w", err)
		}

		var summaryList []map[string]any
		for _, t := range tasks {
			summaryList = append(summaryList, map[string]any{
				"task_id":          t.ID,
				"title":            t.Title,
				"agent_name":       t.AgentName,
				"status":           t.Status,
				"duration_seconds": t.DurationSeconds,
				"created_at":       t.CreatedAt.Format("15:04:05"),
			})
		}

		res := map[string]any{
			"total": total,
			"tasks": summaryList,
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	default:
		return "", fmt.Errorf("unknown subagent tool %s", toolName)
	}
}
