import sys
import json
import sqlite3
import os
import secrets
import time

def get_db_path():
    home = os.path.expanduser("~")
    return os.path.join(home, ".agyent", "agyent.db")

def get_db_connection():
    db_path = get_db_path()
    if not os.path.exists(db_path):
        os.makedirs(os.path.dirname(db_path), exist_ok=True)
    conn = sqlite3.connect(f"file:{db_path}?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", uri=True)
    conn.row_factory = sqlite3.Row
    return conn

def dispatch_task(title, prompt, agent_name="agyent", model="flash", effort="low", workspace_mode="share", callback_mode="notify_user"):
    if not title or not prompt:
        return {"error": "title and prompt are required"}
    
    task_id = f"task-{secrets.token_hex(4)}"
    now_ms = int(time.time() * 1000)

    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        
        # Get active session from sessions table if exists
        cursor.execute("SELECT session_key, global_conversation_id, active_project FROM sessions ORDER BY updated_at DESC LIMIT 1")
        session_row = cursor.fetchone()
        parent_session_key = session_row["session_key"] if session_row else "telegram:default"
        parent_conv_id = session_row["global_conversation_id"] if session_row and session_row["global_conversation_id"] else ""
        project_name = session_row["active_project"] if session_row and session_row["active_project"] else ""

        insert_sql = """
            INSERT INTO subagent_tasks (
                id, parent_session_key, parent_conversation_id, sub_conversation_id,
                agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
                status, current_step, current_tool, progress_message, pending_question,
                result_summary, artifacts_json, error_message, total_tokens, duration_seconds,
                created_at, updated_at
            ) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING', 0, '', '', '', '', '[]', '', 0, 0, ?, ?)
        """
        cursor.execute(insert_sql, (
            task_id, parent_session_key, parent_conv_id,
            agent_name, project_name, title, prompt, model, effort, workspace_mode, callback_mode,
            now_ms, now_ms
        ))
        conn.commit()
        conn.close()

        return {
            "task_id": task_id,
            "status": "PENDING",
            "agent_name": agent_name,
            "title": title,
            "model": model,
            "message": f"🚀 Successfully dispatched background sub-agent task: {task_id} ({title}). The background worker pool has enqueued it. Inform the user and conclude your turn immediately without waiting."
        }
    except Exception as e:
        return {"error": f"Failed to dispatch task: {str(e)}"}

def check_progress(task_id):
    if not task_id:
        return {"error": "task_id is required"}
    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        cursor.execute("""
            SELECT id, agent_name, title, status, current_step, current_tool,
                   progress_message, pending_question, duration_seconds, total_tokens,
                   result_summary, error_message, updated_at
            FROM subagent_tasks WHERE id = ?
        """, (task_id,))
        row = cursor.fetchone()
        conn.close()
        if not row:
            return {"error": f"Task {task_id} not found"}
        return dict(row)
    except Exception as e:
        return {"error": str(e)}

def cancel_task(task_id):
    if not task_id:
        return {"error": "task_id is required"}
    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        now_ms = int(time.time() * 1000)
        cursor.execute("UPDATE subagent_tasks SET status = 'CANCELLED', updated_at = ? WHERE id = ?", (now_ms, task_id))
        conn.commit()
        conn.close()
        return {"task_id": task_id, "status": "CANCELLED", "message": f"🛑 Task {task_id} has been marked as cancelled."}
    except Exception as e:
        return {"error": str(e)}

def list_tasks(limit=10):
    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        cursor.execute("""
            SELECT id, agent_name, title, status, duration_seconds, total_tokens, created_at
            FROM subagent_tasks ORDER BY created_at DESC LIMIT ?
        """, (limit,))
        rows = [dict(r) for r in cursor.fetchall()]
        conn.close()
        return {"tasks": rows, "count": len(rows)}
    except Exception as e:
        return {"error": str(e)}

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
                                "callback_mode": {"type": "string", "enum": ["notify_user", "callback_main", "silent"], "description": "Reporting mode upon completion", "default": "notify_user"}
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
                                "limit": {"type": "integer", "description": "Max number of tasks to return", "default": 10}
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
                callback_mode=args.get("callback_mode", "notify_user")
            )
        elif tool_name == "check_subagent_progress":
            res = check_progress(args.get("task_id", ""))
        elif tool_name == "cancel_subagent_task":
            res = cancel_task(args.get("task_id", ""))
        elif tool_name == "list_subagents":
            res = list_tasks(limit=int(args.get("limit", 10)))
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
