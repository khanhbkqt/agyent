import sys
import json
import sqlite3
import os

def query_sqlite(db_path, query):
    if not os.path.exists(db_path):
        return {"error": f"Database file not found: {db_path}"}
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
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
