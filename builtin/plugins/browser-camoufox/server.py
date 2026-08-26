import sys
import json
import traceback

def log(msg):
    sys.stderr.write(f"[CAMOUFOX_MCP] {msg}\n")
    sys.stderr.flush()

def fetch_url(url):
    log(f"Launching Camoufox stealth browser to fetch: {url}")
    try:
        from camoufox.sync_api import Camoufox
        with Camoufox(headless=True) as browser:
            page = browser.new_page()
            page.goto(url, wait_until="domcontentloaded", timeout=30000)
            title = page.title()
            text = page.inner_text("body")
            if len(text) > 4000:
                text = text[:4000] + "\n... [Content Truncated for Token Efficiency]"
            return {
                "url": url,
                "title": title,
                "content": text
            }
    except Exception as e:
        log(f"Error fetching {url}: {e}\n{traceback.format_exc()}")
        return {
            "error": str(e)
        }

def handle_message(msg):
    req_id = msg.get("id")
    method = msg.get("method")

    if method == "initialize":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {
                    "tools": {}
                },
                "serverInfo": {
                    "name": "camoufox-browser-plugin",
                    "version": "1.0.0"
                }
            }
        }
    elif method == "notifications/initialized" or method == "initialized":
        log("Initialized notification received")
        return None
    elif method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "tools": [
                    {
                        "name": "camoufox_fetch_page",
                        "description": "Fetches a web page using Camoufox stealth anti-detect browser and extracts the text content and title.",
                        "inputSchema": {
                            "type": "object",
                            "properties": {
                                "url": {
                                    "type": "string",
                                    "description": "The full HTTP or HTTPS URL to navigate to"
                                }
                            },
                            "required": ["url"]
                        }
                    }
                ]
            }
        }
    elif method == "tools/call":
        params = msg.get("params", {})
        tool_name = params.get("name")
        args = params.get("arguments", {})
        log(f"Calling tool {tool_name} with args {args}")
        
        if tool_name == "camoufox_fetch_page":
            url = args.get("url", "")
            if not url:
                return {
                    "jsonrpc": "2.0",
                    "id": req_id,
                    "error": {
                        "code": -32602,
                        "message": "Missing 'url' argument"
                    }
                }
            res = fetch_url(url)
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "content": [
                        {
                            "type": "text",
                            "text": json.dumps(res, ensure_ascii=False, indent=2)
                        }
                    ],
                    "isError": "error" in res
                }
            }
        else:
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "error": {
                    "code": -32601,
                    "message": f"Tool {tool_name} not found"
                }
            }
    elif req_id is not None:
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "error": {
                "code": -32601,
                "message": f"Method {method} not supported"
            }
        }
    return None

def main():
    log("Starting Camoufox Browser MCP Server...")
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
                out = json.dumps(resp) + "\n"
                sys.stdout.write(out)
                sys.stdout.flush()
        except Exception as e:
            log(f"Error in main loop: {e}")

if __name__ == "__main__":
    main()
