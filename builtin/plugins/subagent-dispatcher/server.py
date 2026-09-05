import sys
import json
import os
import socket

DEFAULT_IPC_ADDR = "127.0.0.1:49216"

def send_ipc_action(action, params):
    """Sends an authenticated/scoped action to the agyent core IPC daemon via local socket."""
    ipc_addr_str = os.environ.get("AGYENT_ACTION_IPC_ADDR", DEFAULT_IPC_ADDR).strip()
    turn_id = os.environ.get("AGYENT_TURN_ID", "").strip()
    try:
        host, port_str = ipc_addr_str.split(":")
        port = int(port_str)
    except Exception:
        host, port = "127.0.0.1", 49216

    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(10.0)
    try:
        s.connect((host, port))
        action_payload = {
            "action": action,
            "turn_id": turn_id,
            "params": params,
        }
        payload = json.dumps(action_payload) + "\n"
        s.sendall(payload.encode("utf-8"))

        f = s.makefile("r", encoding="utf-8")
        line = f.readline()
        if not line:
            return {"error": "Empty response from agyent IPC daemon"}
        resp = json.loads(line)
        if not resp.get("success", False):
            return {"error": resp.get("error", "Unknown IPC error")}
        return resp.get("data")
    except Exception as e:
        return {"error": f"Failed to communicate with agyent IPC ({ipc_addr_str}): {str(e)}"}
    finally:
        s.close()

def dispatch_task(title, prompt, agent_name="", model="flash", effort="low", workspace_mode="share", callback_mode="notify_user", parent_session_key=""):
    if not title or not prompt:
        return {"error": "title and prompt are required"}
    
    if not parent_session_key:
        parent_session_key = os.environ.get("AGYENT_SESSION_KEY", "").strip()

    if not agent_name or agent_name == "agyent":
        env_agent = os.environ.get("AGYENT_AGENT_NAME", "").strip()
        if env_agent:
            agent_name = env_agent
        elif not agent_name:
            agent_name = "agyent"

    params = {
        "title": title,
        "prompt": prompt,
        "agent_name": agent_name,
        "model": model,
        "effort": effort,
        "workspace_mode": workspace_mode,
        "callback_mode": callback_mode,
        "parent_session_key": parent_session_key,
    }
    return send_ipc_action("dispatch_subagent", params)

def check_progress(task_id):
    if not task_id:
        return {"error": "task_id is required"}
    return send_ipc_action("check_subagent_progress", {"task_id": task_id})

def cancel_task(task_id):
    if not task_id:
        return {"error": "task_id is required"}
    return send_ipc_action("cancel_subagent_task", {"task_id": task_id})

def list_tasks(limit=10, session_key=""):
    if not session_key:
        session_key = os.environ.get("AGYENT_SESSION_KEY", "").strip()
    return send_ipc_action("list_subagents", {"limit": limit, "session_key": session_key})

def handle_message(msg):
    req_id = msg.get("id")
    method = msg.get("method")

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "subagent-dispatcher-plugin", "version": "1.0.0"}
            }
        }
    elif method in ["notifications/initialized", "initialized"]:
        return None
    elif method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "tools": [
                    {
                        "name": "dispatch_subagent",
                        "description": "Delegate a heavy, multi-step, research, or long-running task (e.g. repo audit, web scraping, trend hunting, deep research) to a background sub-agent without blocking the main track. Returns a task ticket immediately (<1ms).",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "title": {"type": "string", "description": "Concise human-readable task title"},
                                "prompt": {"type": "string", "description": "Detailed instructions for the subagent"},
                                "agent_name": {"type": "string", "description": "Target agent persona ('researcher', 'coder', 'agyent')", "default": "agyent"},
                                "model": {"type": "string", "enum": ["flash", "flash_lite", "pro"], "description": "Model tier for execution", "default": "flash"},
                                "effort": {"type": "string", "enum": ["low", "medium", "high"], "description": "Reasoning effort", "default": "low"},
                                "workspace_mode": {"type": "string", "enum": ["share", "scratch"], "description": "Workspace isolation mode", "default": "share"},
                                "callback_mode": {"type": "string", "enum": ["notify_user", "callback_main", "silent"], "description": "Reporting mode upon completion", "default": "notify_user"},
                                "parent_session_key": {"type": "string", "description": "Optional parent session key"}
                            },
                            "required": ["title", "prompt"]
                        }
                    },
                    {
                        "name": "check_subagent_progress",
                        "description": "Query real-time execution state, step progress, and logs for an in-flight sub-agent task.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "task_id": {"type": "string", "description": "Task ID ticket (e.g. 'task-8f92a1bc')"}
                            },
                            "required": ["task_id"]
                        }
                    },
                    {
                        "name": "cancel_subagent_task",
                        "description": "Forcefully terminate a running background sub-agent task and clean up its process tree.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "task_id": {"type": "string", "description": "Sub-agent Task ID to cancel"}
                            },
                            "required": ["task_id"]
                        }
                    },
                    {
                        "name": "list_subagents",
                        "description": "List all active and recent background sub-agent tasks.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "limit": {"type": "integer", "description": "Max number of tasks to return", "default": 10},
                                "session_key": {"type": "string", "description": "Optional session key filter"}
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

        res = None
        if tool_name == "dispatch_subagent":
            res = dispatch_task(
                title=args.get("title", ""),
                prompt=args.get("prompt", ""),
                agent_name=args.get("agent_name", "agyent"),
                model=args.get("model", "flash"),
                effort=args.get("effort", "low"),
                workspace_mode=args.get("workspace_mode", "share"),
                callback_mode=args.get("callback_mode", "notify_user"),
                parent_session_key=args.get("parent_session_key", "")
            )
        elif tool_name == "check_subagent_progress":
            res = check_progress(args.get("task_id", ""))
        elif tool_name == "cancel_subagent_task":
            res = cancel_task(args.get("task_id", ""))
        elif tool_name == "list_subagents":
            res = list_tasks(limit=int(args.get("limit", 10)), session_key=args.get("session_key", ""))
        else:
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "error": {"code": -32601, "message": f"Tool {tool_name} not found"}
            }

        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "content": [{"type": "text", "text": json.dumps(res, indent=2)}],
                "isError": "error" in res if isinstance(res, dict) else False
            }
        }
    elif req_id is not None:
        return {
            "jsonrpc": "2.0",
            "id": req_id,
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
            sys.stderr.write(f"[SUBAGENT_MCP] Error: {e}\n")
            sys.stderr.flush()

if __name__ == "__main__":
    main()
