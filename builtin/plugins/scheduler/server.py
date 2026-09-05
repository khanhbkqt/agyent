import sys
import json
import socket
import os

DEFAULT_IPC_ADDR = "127.0.0.1:49215"

def send_ipc_action(action, params=None, timeout=10.0):
    ipc_addr = os.environ.get("AGYENT_SECURITY_IPC_ADDR", DEFAULT_IPC_ADDR)
    token = os.environ.get("AGYENT_IPC_TOKEN", "").strip()
    turn_id = os.environ.get("AGYENT_TURN_ID", "").strip()
    host, port_str = ipc_addr.split(":", 1)
    port = int(port_str)

    payload = {
        "action": action,
        "token": token,
        "turn_id": turn_id,
        "params": params or {}
    }

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        sock.settimeout(timeout)
        sock.connect((host, port))
        
        req_line = json.dumps(payload) + "\n"
        sock.sendall(req_line.encode("utf-8"))

        f = sock.makefile("r", encoding="utf-8")
        resp_line = f.readline()
        sock.close()

        if not resp_line:
            return {"success": False, "error": "Empty response from agyent gateway daemon"}

        return json.loads(resp_line.strip())
    except Exception as e:
        return {"success": False, "error": f"Failed to communicate with agyent daemon IPC: {str(e)}"}

def handle_message(msg):
    msg_id = msg.get("id")
    method = msg.get("method")

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "scheduler-plugin", "version": "1.0.0"}
            }
        }
    elif method in ["notifications/initialized", "initialized"]:
        return None
    elif method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "tools": [
                    {
                        "name": "schedule_task",
                        "description": "Schedule an automated one-off or recurring cron prompt for an agent. When the scheduled time arrives, the agent wakes up, runs the prompt, and reports results to the chat.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "prompt": {
                                    "type": "string",
                                    "description": "The exact prompt instructions for the agent to execute when triggered"
                                },
                                "time_expression": {
                                    "type": "string",
                                    "description": "When to run. Relative delay ('in 15m', 'after 2 hours', 'in 30s'), ISO timestamp ('2026-09-05T08:00:00Z'), or standard 5-field cron ('0 8 * * *', '*/30 * * * *', '@daily')"
                                },
                                "title": {
                                    "type": "string",
                                    "description": "Short human-readable title for the task (e.g. 'Daily Git Summary')"
                                },
                                "schedule_type": {
                                    "type": "string",
                                    "enum": ["auto", "cron", "one_off"],
                                    "description": "Schedule type. Defaults to 'auto' to infer from time_expression",
                                    "default": "auto"
                                },
                                "agent_name": {
                                    "type": "string",
                                    "description": "Target agent persona name (default: active session agent or 'agyent')"
                                },
                                "overlap_policy": {
                                    "type": "string",
                                    "enum": ["skip", "cancel_previous", "queue"],
                                    "description": "Concurrency policy if previous run is still in-flight",
                                    "default": "skip"
                                }
                            },
                            "required": ["prompt", "time_expression"]
                        }
                    },
                    {
                        "name": "list_schedules",
                        "description": "List all active, paused, or completed scheduled tasks for an agent.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "agent_name": {
                                    "type": "string",
                                    "description": "Filter tasks by agent name (optional)"
                                },
                                "status": {
                                    "type": "string",
                                    "enum": ["ACTIVE", "RUNNING", "PAUSED", "COMPLETED", "FAILED", "CANCELLED", ""],
                                    "description": "Filter by status (default: ACTIVE)",
                                    "default": "ACTIVE"
                                }
                            }
                        }
                    },
                    {
                        "name": "cancel_schedule",
                        "description": "Cancel and remove a scheduled or recurring task by its task ID.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "task_id": {
                                    "type": "string",
                                    "description": "Task ID ticket to cancel (e.g. 'sched-cron-a1b2c3' or 'sched-once-1a2b3c')"
                                }
                            },
                            "required": ["task_id"]
                        }
                    },
                    {
                        "name": "configure_heartbeat",
                        "description": "Configure agent periodic heartbeat (enable/disable, set interval, update HEARTBEAT.md instructions).",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "agent_name": {
                                    "type": "string",
                                    "description": "Agent name to configure heartbeat for (default: 'agyent')"
                                },
                                "enabled": {
                                    "type": "boolean",
                                    "description": "Whether heartbeat wakeup is enabled"
                                },
                                "interval": {
                                    "type": "string",
                                    "description": "Heartbeat interval (e.g. '15m', '30m', '1h', '2h', '6h')"
                                },
                                "prompt": {
                                    "type": "string",
                                    "description": "Instructions to write into HEARTBEAT.md executed on each heartbeat pulse"
                                }
                            }
                        }
                    },
                    {
                        "name": "get_heartbeat",
                        "description": "Retrieve the current heartbeat configuration and HEARTBEAT.md prompt for an agent.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "agent_name": {
                                    "type": "string",
                                    "description": "Agent name (default: 'agyent')"
                                }
                            }
                        }
                    },
                    {
                        "name": "trigger_heartbeat",
                        "description": "Manually trigger an immediate heartbeat wakeup pulse for an agent without waiting for the timer.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "agent_name": {
                                    "type": "string",
                                    "description": "Agent name (default: 'agyent')"
                                }
                            }
                        }
                    }
                ]
            }
        }
    elif method == "tools/call":
        params = msg.get("params", {})
        tool_name = params.get("name")
        args = params.get("arguments", {})

        env_agent = os.environ.get("AGYENT_AGENT_NAME", "").strip()
        session_key = os.environ.get("AGYENT_SESSION_KEY", "").strip()
        user_id = os.environ.get("AGYENT_USER_ID", "").strip()

        if not args.get("agent_name") and env_agent:
            args["agent_name"] = env_agent
        if not args.get("session_key") and session_key:
            args["session_key"] = session_key
        if not args.get("user_id") and user_id:
            args["user_id"] = user_id

        resp = send_ipc_action(tool_name, args)

        is_error = not resp.get("success", False)
        content_text = ""
        if is_error:
            content_text = f"❌ Error executing {tool_name}: {resp.get('error', 'Unknown error')}"
        else:
            data = resp.get("data")
            if tool_name == "schedule_task":
                task_id = data.get("id", "unknown")
                next_run = data.get("next_run_at", "")
                content_text = f"⏰ Successfully scheduled task `{task_id}` for agent `{data.get('agent_name', 'agyent')}`.\nNext execution: {next_run}\nPrompt: {data.get('prompt')}"
            elif tool_name == "cancel_schedule":
                content_text = f"🗑️ Schedule `{args.get('task_id')}` cancelled successfully."
            elif tool_name == "configure_heartbeat":
                status = "enabled" if data.get("enabled") else "disabled"
                content_text = f"💓 Heartbeat for agent `{data.get('agent_name')}` is now **{status}** (Interval: {data.get('interval_seconds', 0)}s)."
            elif tool_name == "trigger_heartbeat":
                content_text = f"⚡ Heartbeat triggered immediately for agent `{args.get('agent_name', 'agyent')}`."
            else:
                content_text = json.dumps(data, indent=2, ensure_ascii=False)

        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "result": {
                "content": [{"type": "text", "text": content_text}],
                "isError": is_error
            }
        }
    elif msg_id is not None:
        return {
            "jsonrpc": "2.0",
            "id": msg_id,
            "error": {"code": -32601, "message": f"Method {method} not supported"}
        }
    return None

def main():
    while True:
        line = sys.stdin.readline()
        if not line:
            break
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
            resp = handle_message(msg)
            if resp is not None:
                sys.stdout.write(json.dumps(resp) + "\n")
                sys.stdout.flush()
        except Exception as e:
            sys.stderr.write(f"[SCHEDULER_MCP] Error: {e}\n")
            sys.stderr.flush()

if __name__ == "__main__":
    main()
