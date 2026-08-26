#!/usr/bin/env python3
"""
Camoufox Stealth Browser MCP Server.
Implements Model Context Protocol (MCP) JSON-RPC 2.0 interface providing 12 autonomous web perception tools.
"""

import json
import os
import sys
import traceback
from typing import Any, Dict, Optional

# Ensure plugin root is in python path
current_dir = os.path.dirname(os.path.abspath(__file__))
if current_dir not in sys.path:
    sys.path.insert(0, current_dir)

from handlers.extraction_handler import (
    handle_extract_json_ld,
    handle_fetch_page,
    handle_scrape_selector,
)
from handlers.interactive_handler import (
    handle_act,
    handle_inspect_dom,
    handle_session_close,
    handle_session_start,
)
from handlers.network_handler import handle_intercept_api
from handlers.search_handler import (
    handle_discover_trends,
    handle_search,
)
from handlers.visual_handler import (
    handle_pdf_export,
    handle_screenshot,
)


def log(msg: str) -> None:
    """Logs diagnostics to stderr to keep stdout strictly JSON-RPC clean."""
    sys.stderr.write(f"[CAMOUFOX_MCP] {msg}\n")
    sys.stderr.flush()


# Full Tool Definitions and JSON Schemas
TOOL_DEFINITIONS = [
    {
        "name": "camoufox_search",
        "description": "Performs a stealth search using DuckDuckGo, Google, Startpage, or Bing without getting blocked or triggering bot challenges. Replaces legacy web_search.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {
                    "type": "string",
                    "description": "Search query keywords"
                },
                "engine": {
                    "type": "string",
                    "enum": ["duckduckgo", "google", "startpage", "bing"],
                    "default": "duckduckgo",
                    "description": "Search engine provider"
                },
                "locale": {
                    "type": "string",
                    "default": "en-US",
                    "description": "Search locale/region (e.g., 'de-DE', 'en-US', 'vi-VN')"
                },
                "max_results": {
                    "type": "integer",
                    "default": 5,
                    "description": "Maximum number of search results to return"
                }
            },
            "required": ["query"]
        }
    },
    {
        "name": "camoufox_discover_trends",
        "description": "Discovers trending keywords, topics, and hashtags from TikTok Creative Center, Pinterest Trends, or Google Trends.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "platform": {
                    "type": "string",
                    "enum": ["tiktok", "pinterest", "google_trends"],
                    "description": "Trend discovery platform"
                },
                "country": {
                    "type": "string",
                    "default": "US",
                    "description": "Target country code (e.g., 'DE', 'US', 'VN')"
                },
                "category": {
                    "type": "string",
                    "description": "Optional category filter"
                }
            },
            "required": ["platform"]
        }
    },
    {
        "name": "camoufox_fetch_page",
        "description": "Loads a webpage with full stealth fingerprinting, dismisses cookie/consent banners, and converts content into token-optimized Markdown.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "The full HTTP/HTTPS URL to fetch"
                },
                "extract_mode": {
                    "type": "string",
                    "enum": ["markdown", "text", "raw_html"],
                    "default": "markdown",
                    "description": "Extraction formatting mode"
                },
                "auto_dismiss_banners": {
                    "type": "boolean",
                    "default": True,
                    "description": "Automatically dismiss cookie and consent dialogs"
                },
                "timeout_ms": {
                    "type": "integer",
                    "default": 30000,
                    "description": "Page navigation timeout in milliseconds"
                }
            },
            "required": ["url"]
        }
    },
    {
        "name": "camoufox_extract_json_ld",
        "description": "Extracts Schema.org JSON-LD scripts, OpenGraph, Twitter Cards, and structured meta from a web page.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "Target web page URL"
                },
                "timeout_ms": {
                    "type": "integer",
                    "default": 30000,
                    "description": "Timeout in milliseconds"
                }
            },
            "required": ["url"]
        }
    },
    {
        "name": "camoufox_scrape_selector",
        "description": "Extracts structured data records matching a CSS selector and field mapping dictionary.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "Target web page URL"
                },
                "selector": {
                    "type": "string",
                    "description": "CSS selector for repeating item containers (e.g. '.product-card', 'table tr')"
                },
                "fields": {
                    "type": "object",
                    "additionalProperties": {"type": "string"},
                    "description": "Map of field name to sub-selector or attribute (e.g., {'title': 'h2', 'link': 'a@href', 'price': '.price'})"
                },
                "limit": {
                    "type": "integer",
                    "default": 20,
                    "description": "Maximum number of items to extract"
                }
            },
            "required": ["url", "selector"]
        }
    },
    {
        "name": "camoufox_intercept_api",
        "description": "Intercepts and captures raw background XHR, Fetch, and GraphQL JSON payloads during page load matching a URL regex pattern.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "Web page URL to visit and monitor"
                },
                "url_pattern": {
                    "type": "string",
                    "description": "Regex pattern to match API endpoint URLs (e.g., '/api/v1/|/graphql')"
                },
                "wait_time_ms": {
                    "type": "integer",
                    "default": 5000,
                    "description": "Time in milliseconds to wait for network responses"
                }
            },
            "required": ["url"]
        }
    },
    {
        "name": "camoufox_session_start",
        "description": "Starts a stateful browser session with persistent profile storage (cookies, localStorage) in ~/.agyent/camoufox/profiles/.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "profile_name": {
                    "type": "string",
                    "default": "default",
                    "description": "Profile name for persistent storage state (e.g., 'tiktok_de', 'amazon_us')"
                },
                "headless": {
                    "type": "boolean",
                    "default": True,
                    "description": "Whether to run browser in headless mode"
                },
                "locale": {
                    "type": "string",
                    "default": "en-US",
                    "description": "Browser locale (e.g., 'de-DE', 'en-US')"
                },
                "initial_url": {
                    "type": "string",
                    "description": "Optional initial URL to navigate to"
                }
            }
        }
    },
    {
        "name": "camoufox_inspect_dom",
        "description": "Inspects current page DOM and returns a numbered Accessibility Tree (AOM) [1], [2] or injects visual marks for AI grounding.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Active session ID from camoufox_session_start"
                },
                "mode": {
                    "type": "string",
                    "enum": ["a11y_tree", "visual_marks"],
                    "default": "a11y_tree",
                    "description": "Inspection mode"
                }
            },
            "required": ["session_id"]
        }
    },
    {
        "name": "camoufox_act",
        "description": "Executes human-like user actions on the page using Bézier mouse curves, Gaussian typing delays, or inertial scrolling.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Active session ID"
                },
                "action": {
                    "type": "string",
                    "enum": ["click", "type", "hover", "scroll", "press_key", "navigate", "wait"],
                    "description": "Action type to perform"
                },
                "target_id": {
                    "type": ["string", "integer"],
                    "description": "Numbered element ID from a11y_tree (e.g. 1, '2') or CSS selector"
                },
                "value": {
                    "type": "string",
                    "description": "Text to type, key to press, URL to navigate, or scroll delta in pixels"
                }
            },
            "required": ["session_id", "action"]
        }
    },
    {
        "name": "camoufox_session_close",
        "description": "Closes an active stateful session and persists its cookies and storage state to Profile Vault.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Session ID to close"
                }
            },
            "required": ["session_id"]
        }
    },
    {
        "name": "camoufox_screenshot",
        "description": "Captures a high-resolution screenshot of a URL or active session, saving PNG to disk.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "Target web page URL (for stateless screenshot)"
                },
                "session_id": {
                    "type": "string",
                    "description": "Active session ID (for stateful screenshot)"
                },
                "selector": {
                    "type": "string",
                    "description": "Optional CSS selector to capture a specific element"
                },
                "full_page": {
                    "type": "boolean",
                    "default": False,
                    "description": "Capture the entire scrolling page"
                },
                "output_path": {
                    "type": "string",
                    "description": "Optional target PNG file path"
                }
            }
        }
    },
    {
        "name": "camoufox_pdf_export",
        "description": "Exports a web page or active session as a formatted PDF document.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "Target URL (for stateless export)"
                },
                "session_id": {
                    "type": "string",
                    "description": "Active session ID (for stateful export)"
                },
                "output_path": {
                    "type": "string",
                    "description": "Optional target PDF file path"
                }
            }
        }
    }
]


def execute_tool(name: str, args: Dict[str, Any]) -> Any:
    """Dispatches tool execution to the appropriate domain handler."""
    if name == "camoufox_search":
        return handle_search(
            query=args.get("query", ""),
            engine=args.get("engine", "duckduckgo"),
            locale=args.get("locale", "en-US"),
            max_results=args.get("max_results", 5),
        )
    elif name == "camoufox_discover_trends":
        return handle_discover_trends(
            platform=args.get("platform", "tiktok"),
            country=args.get("country", "US"),
            category=args.get("category"),
        )
    elif name == "camoufox_fetch_page":
        return handle_fetch_page(
            url=args.get("url", ""),
            extract_mode=args.get("extract_mode", "markdown"),
            auto_dismiss_banners=args.get("auto_dismiss_banners", True),
            timeout_ms=args.get("timeout_ms", 30000),
        )
    elif name == "camoufox_extract_json_ld":
        return handle_extract_json_ld(
            url=args.get("url", ""),
            timeout_ms=args.get("timeout_ms", 30000),
        )
    elif name == "camoufox_scrape_selector":
        return handle_scrape_selector(
            url=args.get("url", ""),
            selector=args.get("selector", ""),
            fields=args.get("fields"),
            limit=args.get("limit", 20),
        )
    elif name == "camoufox_intercept_api":
        return handle_intercept_api(
            url=args.get("url", ""),
            url_pattern=args.get("url_pattern"),
            wait_time_ms=args.get("wait_time_ms", 5000),
        )
    elif name == "camoufox_session_start":
        return handle_session_start(
            profile_name=args.get("profile_name", "default"),
            headless=args.get("headless", True),
            locale=args.get("locale", "en-US"),
            initial_url=args.get("initial_url"),
        )
    elif name == "camoufox_inspect_dom":
        return handle_inspect_dom(
            session_id=args.get("session_id", ""),
            mode=args.get("mode", "a11y_tree"),
        )
    elif name == "camoufox_act":
        return handle_act(
            session_id=args.get("session_id", ""),
            action=args.get("action", ""),
            target_id=args.get("target_id"),
            value=args.get("value"),
        )
    elif name == "camoufox_session_close":
        return handle_session_close(
            session_id=args.get("session_id", ""),
        )
    elif name == "camoufox_screenshot":
        return handle_screenshot(
            url=args.get("url"),
            session_id=args.get("session_id"),
            selector=args.get("selector"),
            full_page=args.get("full_page", False),
            output_path=args.get("output_path"),
        )
    elif name == "camoufox_pdf_export":
        return handle_pdf_export(
            url=args.get("url"),
            session_id=args.get("session_id"),
            output_path=args.get("output_path"),
        )
    else:
        raise ValueError(f"Tool '{name}' not found")


def handle_message(msg: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    """Handles an incoming JSON-RPC request and returns the JSON-RPC response."""
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
                    "version": "1.1.0"
                }
            }
        }
    elif method in ("notifications/initialized", "initialized"):
        log("Client initialization confirmed.")
        return None
    elif method == "tools/list":
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {
                "tools": TOOL_DEFINITIONS
            }
        }
    elif method == "tools/call":
        params = msg.get("params", {})
        tool_name = params.get("name", "")
        args = params.get("arguments", {})
        log(f"Calling tool '{tool_name}' with args: {list(args.keys())}")

        try:
            res = execute_tool(tool_name, args)
            text_out = json.dumps(res, ensure_ascii=False, indent=2)
            is_error = isinstance(res, dict) and "error" in res and res.get("error") is not None
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "content": [
                        {
                            "type": "text",
                            "text": text_out
                        }
                    ],
                    "isError": is_error
                }
            }
        except Exception as e:
            log(f"Tool execution failed: {e}\n{traceback.format_exc()}")
            return {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "content": [
                        {
                            "type": "text",
                            "text": json.dumps({"error": str(e)}, ensure_ascii=False)
                        }
                    ],
                    "isError": True
                }
            }
    elif req_id is not None:
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "error": {
                "code": -32601,
                "message": f"Method '{method}' not supported"
            }
        }

    return None


def main() -> None:
    log("Starting Camoufox Full Capabilities Engine MCP Server...")
    while True:
        try:
            line = sys.stdin.readline()
            if not line:
                break
            line = line.strip()
            if not line:
                continue

            msg = json.loads(line)
            resp = handle_message(msg)
            if resp is not None:
                out = json.dumps(resp, ensure_ascii=False) + "\n"
                sys.stdout.write(out)
                sys.stdout.flush()
        except Exception as e:
            log(f"Error in JSON-RPC main loop: {e}")


if __name__ == "__main__":
    main()
