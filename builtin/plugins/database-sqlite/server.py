import json
import os
import pathlib
import sqlite3
import sys
from typing import Optional


def is_safe_path(target_path: str, workspace_root: Optional[str] = None) -> bool:
    """
    Validates that target_path is strictly contained within the active workspace root jail.
    Prevents path traversal, symlink escaping, and access to out-of-bounds filesystem locations.
    """
    if not target_path:
        return False
    if not workspace_root:
        workspace_root = os.environ.get("AGYENT_AGENT_WORKSPACE") or os.getcwd()
    try:
        ws_real = os.path.realpath(os.path.abspath(workspace_root))
        if not os.path.isabs(target_path):
            resolved_target = os.path.join(ws_real, target_path)
        else:
            resolved_target = target_path
        target_real = os.path.realpath(os.path.abspath(resolved_target))
        # Case-folding for case-insensitive OS filesystems (Windows and macOS/Darwin)
        if sys.platform in ("win32", "darwin"):
            ws_real = ws_real.lower()
            target_real = target_real.lower()
        common = os.path.commonpath([ws_real, target_real])
        return common == ws_real
    except Exception:
        return False


def query_sqlite(db_path: str, query: str, workspace_root: Optional[str] = None) -> dict:
    """Executes read-only query against SQLite file guarded by workspace jail."""
    if not db_path:
        return {"error": "Missing database path"}

    ws_root = workspace_root or os.environ.get("AGYENT_AGENT_WORKSPACE") or os.getcwd()
    if not os.path.isabs(db_path):
        full_db_path = os.path.abspath(os.path.join(ws_root, db_path))
    else:
        full_db_path = os.path.abspath(db_path)

    if not is_safe_path(full_db_path, ws_root):
        return {
            "error": f"Security Violation: Database path '{db_path}' is outside the authorized workspace jail ({ws_root}). Access denied under APIS-4D standard."
        }

    if not os.path.exists(full_db_path):
        return {"error": f"Database file not found: {db_path}"}
    try:
        db_uri = pathlib.Path(full_db_path).resolve().as_uri() + "?mode=ro"
        conn = sqlite3.connect(db_uri, uri=True)
        cursor = conn.cursor()
        cursor.execute(query)
        columns = [desc[0] for desc in cursor.description] if cursor.description else []
        rows = cursor.fetchall()
        conn.close()
        return {
            "columns": columns,
            "rows": rows[:100],
            "total_rows_returned": len(rows)
        }
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
                "serverInfo": {"name": "sqlite-inspector-plugin", "version": "1.0.0"}
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
                        "name": "sqlite_query_readonly",
                        "description": "Executes a read-only SQL query against a local SQLite database file.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "db_path": {"type": "string", "description": "Absolute path to SQLite database file"},
                                "query": {"type": "string", "description": "Read-only SQL query (SELECT, PRAGMA, etc.)"}
                            },
                            "required": ["db_path", "query"]
                        }
                    }
                ]
            }
        }
    elif method == "tools/call":
        params = msg.get("params", {})
        tool_name = params.get("name")
        args = params.get("arguments", {})
        if tool_name == "sqlite_query_readonly":
            db_path = args.get("db_path", "")
            query = args.get("query", "")
            res = query_sqlite(db_path, query)
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "content": [{"type": "text", "text": json.dumps(res, indent=2)}],
                    "isError": "error" in res
                }
            }
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "error": {"code": -32601, "message": f"Tool {tool_name} not found"}
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
        except Exception:
            pass

if __name__ == "__main__":
    main()
