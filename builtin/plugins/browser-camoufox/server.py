#!/usr/bin/env python3
"""
Camoufox Stealth Browser MCP Server.
Implements Model Context Protocol (MCP) JSON-RPC 2.0 interface providing 14 autonomous web perception tools.
Supports high-performance HTTP Daemon proxying with automatic detached background process management
and seamless in-process fallback.
"""

import json
import os
import subprocess
import sys
import time
import traceback
import urllib.error
import urllib.request
from typing import Any, Dict, Optional

# Ensure plugin root is in python path
current_dir = os.path.dirname(os.path.abspath(__file__))
if current_dir not in sys.path:
    sys.path.insert(0, current_dir)

try:
    from handlers.extraction_handler import (
        handle_extract_json_ld,
        handle_fetch_page,
        handle_scrape_selector,
    )
    from handlers.interactive_handler import (
        handle_act,
        handle_inspect_dom,
        handle_session_close,
        handle_session_export_state,
        handle_session_import_state,
        handle_session_list,
        handle_session_save,
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
    from handlers.media_handler import (
        handle_download_media,
        handle_sniff_media,
    )
    from handlers.captcha_handler import handle_solve_captcha
    CAMOUFOX_AVAILABLE = True
    CAMOUFOX_IMPORT_ERROR = None
except Exception as e:
    CAMOUFOX_AVAILABLE = False
    CAMOUFOX_IMPORT_ERROR = str(e)

DAEMON_DIR = os.path.join(os.path.expanduser("~"), ".agyent", "camoufox")
PORT_FILE = os.path.join(DAEMON_DIR, "daemon.port")
LOG_FILE = os.path.join(DAEMON_DIR, "daemon.log")


def log(msg: str) -> None:
    """Logs diagnostics to stderr to keep stdout strictly JSON-RPC clean."""
    sys.stderr.write(f"[CAMOUFOX_MCP] {msg}\n")
    sys.stderr.flush()


def get_active_daemon_port() -> Optional[int]:
    """Reads active port from daemon.port and checks /health."""
    if not os.path.exists(PORT_FILE):
        return None
    try:
        with open(PORT_FILE, "r", encoding="utf-8") as f:
            port_str = f.read().strip()
        if not port_str.isdigit():
            return None
        port = int(port_str)

        # Health check
        req = urllib.request.Request(f"http://127.0.0.1:{port}/health")
        with urllib.request.urlopen(req, timeout=1.5) as resp:
            if resp.status == 200:
                return port
    except Exception:
        pass
    return None


def ensure_daemon_running() -> Optional[int]:
    """Ensures the background daemon is running and returns its port."""
    if os.environ.get("CAMOUFOX_NO_DAEMON", "0") == "1":
        return None

    # Check if already running
    port = get_active_daemon_port()
    if port:
        return port

    # Spawn daemon in detached background process
    log("Spawning Camoufox background daemon...")
    daemon_script = os.path.join(current_dir, "daemon.py")
    os.makedirs(DAEMON_DIR, exist_ok=True)

    try:
        log_f = open(LOG_FILE, "a", encoding="utf-8")
        if sys.platform == "win32":
            creation_flags = subprocess.DETACHED_PROCESS | subprocess.CREATE_NEW_PROCESS_GROUP
            if hasattr(subprocess, "CREATE_NO_WINDOW"):
                creation_flags |= subprocess.CREATE_NO_WINDOW
            subprocess.Popen(
                [sys.executable, daemon_script],
                creationflags=creation_flags,
                stdout=log_f,
                stderr=log_f,
                stdin=subprocess.DEVNULL,
                cwd=current_dir,
            )
        else:
            subprocess.Popen(
                [sys.executable, daemon_script],
                start_new_session=True,
                stdout=log_f,
                stderr=log_f,
                stdin=subprocess.DEVNULL,
                cwd=current_dir,
            )

        # Poll for daemon startup
        start_wait = time.time()
        while time.time() - start_wait < 6.0:
            time.sleep(0.2)
            port = get_active_daemon_port()
            if port:
                log(f"Connected to Camoufox background daemon on port {port}")
                return port

    except Exception as e:
        log(f"Could not spawn daemon: {e}. Falling back to in-process mode.")

    return None


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
                },
                "profile_name": {
                    "type": "string",
                    "description": "Optional persistent profile to reuse search history / cookies"
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
        "description": "Loads a webpage with full stealth fingerprinting, dismisses cookie/consent banners, and converts content into token-optimized Markdown. Supports authenticated profiles.",
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
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name to fetch behind authentication or with persistent cookies"
                },
                "session_id": {
                    "type": "string",
                    "description": "Optional active session ID to fetch within"
                },
                "timeout_ms": {
                    "type": "integer",
                    "default": 30000,
                    "description": "Page navigation timeout in milliseconds"
                },
                "headless": {
                    "type": "boolean",
                    "default": True,
                    "description": "Whether to run browser in headless mode. Set to false to launch a visible GUI window."
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
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name"
                },
                "session_id": {
                    "type": "string",
                    "description": "Optional active session ID"
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
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name for authenticated scraping"
                },
                "session_id": {
                    "type": "string",
                    "description": "Optional active session ID"
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
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name"
                },
                "session_id": {
                    "type": "string",
                    "description": "Optional active session ID"
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
        "description": "Starts or reuses a persistent browser session with native Firefox profile storage (cookies, localStorage, IndexedDB) in ~/.agyent/camoufox/profiles/.",
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
                    "description": "Whether to run browser in headless mode (default: true). Set to false to launch a visible GUI browser window on desktop (Windows, macOS, or Linux) for manual user interaction, login, or 2FA."
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
        "description": "Inspects current page DOM and returns a numbered Accessibility Tree (AOM) [1], [2] or injects visual marks for AI grounding. Supports auto-rehydration across turns.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Active session ID from camoufox_session_start"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name if session_id is omitted"
                },
                "mode": {
                    "type": "string",
                    "enum": ["a11y_tree", "visual_marks"],
                    "default": "a11y_tree",
                    "description": "Inspection mode"
                }
            }
        }
    },
    {
        "name": "camoufox_act",
        "description": "Executes human-like user actions on the page (click, type, hover, scroll, select_option, check, upload_file, switch_tab, new_tab, close_tab, go_back, reload, bring_to_front, wait_for_url).",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Active session ID"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name if session_id is omitted"
                },
                "action": {
                    "type": "string",
                    "enum": [
                        "click", "type", "hover", "scroll", "press_key", "navigate",
                        "select_option", "check", "uncheck", "upload_file",
                        "switch_tab", "new_tab", "close_tab", "go_back", "go_forward", "reload",
                        "bring_to_front", "wait_for_url", "wait_for_selector", "wait"
                    ],
                    "description": "Action type to perform"
                },
                "target_id": {
                    "type": ["string", "integer"],
                    "description": "Numbered element ID from a11y_tree (e.g. 1, '2') or CSS selector"
                },
                "value": {
                    "type": "string",
                    "description": "Text to type, key to press, URL to navigate, file path to upload, tab index to switch, or scroll delta"
                },
                "expects_popup": {
                    "type": "boolean",
                    "default": False,
                    "description": "Set to true if this click action is expected to open a new tab or OAuth login popup"
                }
            },
            "required": ["action"]
        }
    },
    {
        "name": "camoufox_session_list",
        "description": "Lists all active live browser sessions, open tabs, current URLs, and saved profiles in Profile Vault.",
        "inputSchema": {
            "type": "object",
            "properties": {}
        }
    },
    {
        "name": "camoufox_session_save",
        "description": "Explicitly checkpoints active session cookies, DOM metadata, and state to Profile Vault without closing.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Session ID to checkpoint"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Profile name to checkpoint"
                }
            }
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
        "description": "Captures a high-resolution screenshot of a URL, active session, or profile, saving PNG to disk.",
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
                "profile_name": {
                    "type": "string",
                    "description": "Profile name (for stateful screenshot)"
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
        "description": "Exports a web page, active session, or profile as a formatted PDF document.",
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
                "profile_name": {
                    "type": "string",
                    "description": "Profile name (for stateful export)"
                },
                "output_path": {
                    "type": "string",
                    "description": "Optional target PDF file path"
                }
            }
        }
    },
    {
        "name": "camoufox_sniff_media",
        "description": "Stealthily inspects and intercepts video and audio streams from the target URL or active session. Escalates quality to 1080p/4K via player DOM APIs and captures decrypted CDN streams, adaptive tracks, and HLS/DASH manifests.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "url": {
                    "type": "string",
                    "description": "Target webpage URL containing video/audio content"
                },
                "session_id": {
                    "type": "string",
                    "description": "Optional active session ID to sniff media from"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Optional persistent profile name for authenticated media inspection"
                },
                "target_quality": {
                    "type": "string",
                    "enum": ["highest", "4k", "1440p", "1080p", "720p", "480p", "audio_only"],
                    "default": "highest",
                    "description": "Target quality level to trigger on the video player"
                },
                "wait_time_ms": {
                    "type": "integer",
                    "default": 4000,
                    "description": "Duration in milliseconds to allow player buffer playback and CDN request capture"
                },
                "timeout_ms": {
                    "type": "integer",
                    "default": 30000,
                    "description": "Overall navigation and inspection timeout in milliseconds"
                },
                "headless": {
                    "type": "boolean",
                    "default": True,
                    "description": "Whether to run browser in headless mode. Set to false to launch a visible GUI window."
                }
            },
            "required": ["url"]
        }
    },
    {
        "name": "camoufox_download_media",
        "description": "Downloads, multiplexes (audio+video), or trims media streams captured via camoufox_sniff_media. Executes FFmpeg with full Camoufox stealth session headers (cookies, User-Agent) and precise timestamp synchronization.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "video_url": {
                    "type": "string",
                    "description": "Direct URL or manifest URL of the video stream"
                },
                "audio_url": {
                    "type": "string",
                    "description": "Direct URL of the audio stream (required for dual_adaptive streams)"
                },
                "output_filename": {
                    "type": "string",
                    "description": "Target output filename (e.g. 'highlight.mp4'). If not provided, automatically generated."
                },
                "output_dir": {
                    "type": "string",
                    "description": "Target directory. Defaults to agent workspace downloads directory."
                },
                "start_time": {
                    "type": "string",
                    "description": "Optional trimming start time in HH:MM:SS or integer seconds (e.g. '00:01:15' or '75')"
                },
                "duration": {
                    "type": "string",
                    "description": "Optional clipping duration in HH:MM:SS or integer seconds (e.g. '00:00:30' or '30')"
                },
                "accurate_trim": {
                    "type": "boolean",
                    "default": False,
                    "description": "If true, uses ultrafast re-encoding for frame-accurate cuts. If false, uses lossless stream copy."
                },
                "custom_headers": {
                    "type": "object",
                    "additionalProperties": { "type": "string" },
                    "description": "Optional custom HTTP headers to pass to FFmpeg"
                },
                "session_id": {
                    "type": "string",
                    "description": "Optional session ID to inherit User-Agent and Cookies from"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name to inherit cookies from"
                }
            },
            "required": ["video_url"]
        }
    },
    {
        "name": "camoufox_solve_captcha",
        "description": "Autonomously detects, targets, and clicks Cloudflare Turnstile, hCaptcha, or reCAPTCHA verification checkboxes using human Bézier kinematics and dwell-time simulation.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Active session ID containing the challenge"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Profile name"
                },
                "captcha_type": {
                    "type": "string",
                    "enum": ["auto", "turnstile", "hcaptcha", "recaptcha"],
                    "default": "auto",
                    "description": "Challenge type to search for"
                },
                "timeout_ms": {
                    "type": "integer",
                    "default": 15000,
                    "description": "Maximum detection and resolution timeout in milliseconds"
                }
            }
        }
    },
    {
        "name": "camoufox_session_export_state",
        "description": "Exports lightweight session authentication state (cookies, localStorage, sessionStorage) as a compact JSON file (<50KB) with OS/fingerprint metadata for cross-device portability.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "session_id": {
                    "type": "string",
                    "description": "Optional active session ID to export"
                },
                "profile_name": {
                    "type": "string",
                    "description": "Optional profile name to export from vault"
                },
                "output_path": {
                    "type": "string",
                    "description": "Optional target destination file path for storage_state.json"
                }
            }
        }
    },
    {
        "name": "camoufox_session_import_state",
        "description": "Imports a lightweight storage_state JSON file or JSON payload directly into a profile in ProfileVault, instantly restoring logins across machines.",
        "inputSchema": {
            "type": "object",
            "properties": {
                "profile_name": {
                    "type": "string",
                    "description": "Target profile name in ProfileVault"
                },
                "state_path": {
                    "type": "string",
                    "description": "Local file path to storage_state.json"
                },
                "state_json": {
                    "type": "string",
                    "description": "Direct JSON string containing storage state payload"
                }
            },
            "required": ["profile_name"]
        }
    }
]


def execute_tool_local(name: str, args: Dict[str, Any]) -> Any:
    """Local in-process fallback tool execution with multi-tenant isolation context."""
    if not CAMOUFOX_AVAILABLE:
        err_detail = f": {CAMOUFOX_IMPORT_ERROR}" if CAMOUFOX_IMPORT_ERROR else ""
        return {
            "error": f"Camoufox stealth browser dependencies are not available on this host{err_detail}. "
                     f"Please install via: pip install camoufox playwright && camoufox fetch"
        }

    agent_name = os.environ.get("AGYENT_AGENT_NAME", "default")
    workspace_dir = os.environ.get("AGYENT_AGENT_WORKSPACE")
    if name == "camoufox_search":
        return handle_search(
            query=args.get("query", ""),
            engine=args.get("engine", "duckduckgo"),
            locale=args.get("locale", "en-US"),
            max_results=args.get("max_results", 5),
            profile_name=args.get("profile_name"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_discover_trends":
        return handle_discover_trends(
            platform=args.get("platform", "tiktok"),
            country=args.get("country", "US"),
            category=args.get("category"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_fetch_page":
        return handle_fetch_page(
            url=args.get("url", ""),
            extract_mode=args.get("extract_mode", "markdown"),
            auto_dismiss_banners=args.get("auto_dismiss_banners", True),
            timeout_ms=args.get("timeout_ms", 30000),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
            headless=args.get("headless"),
        )
    elif name == "camoufox_extract_json_ld":
        return handle_extract_json_ld(
            url=args.get("url", ""),
            timeout_ms=args.get("timeout_ms", 30000),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_scrape_selector":
        return handle_scrape_selector(
            url=args.get("url", ""),
            selector=args.get("selector", ""),
            fields=args.get("fields"),
            limit=args.get("limit", 20),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_intercept_api":
        return handle_intercept_api(
            url=args.get("url", ""),
            url_pattern=args.get("url_pattern"),
            wait_time_ms=args.get("wait_time_ms", 5000),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_session_start":
        return handle_session_start(
            profile_name=args.get("profile_name", "default"),
            headless=args.get("headless"),
            locale=args.get("locale", "en-US"),
            initial_url=args.get("initial_url"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_inspect_dom":
        return handle_inspect_dom(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            mode=args.get("mode", "a11y_tree"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_act":
        return handle_act(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            action=args.get("action", ""),
            target_id=args.get("target_id"),
            value=args.get("value"),
            expects_popup=args.get("expects_popup", False),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_session_list":
        return handle_session_list(agent_name=agent_name, workspace_dir=workspace_dir)
    elif name == "camoufox_session_save":
        return handle_session_save(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_session_close":
        return handle_session_close(
            session_id=args.get("session_id", ""),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_screenshot":
        return handle_screenshot(
            url=args.get("url"),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            selector=args.get("selector"),
            full_page=args.get("full_page", False),
            output_path=args.get("output_path"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_pdf_export":
        return handle_pdf_export(
            url=args.get("url"),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            output_path=args.get("output_path"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_sniff_media":
        return handle_sniff_media(
            url=args.get("url", ""),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            target_quality=args.get("target_quality", "highest"),
            wait_time_ms=args.get("wait_time_ms", 4000),
            timeout_ms=args.get("timeout_ms", 30000),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
            headless=args.get("headless"),
        )
    elif name == "camoufox_download_media":
        return handle_download_media(
            video_url=args.get("video_url", ""),
            audio_url=args.get("audio_url"),
            output_filename=args.get("output_filename"),
            output_dir=args.get("output_dir"),
            start_time=args.get("start_time"),
            duration=args.get("duration"),
            accurate_trim=args.get("accurate_trim", False),
            custom_headers=args.get("custom_headers"),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_solve_captcha":
        return handle_solve_captcha(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            captcha_type=args.get("captcha_type", "auto"),
            timeout_ms=args.get("timeout_ms", 15000),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_session_export_state":
        return handle_session_export_state(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            output_path=args.get("output_path"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    elif name == "camoufox_session_import_state":
        return handle_session_import_state(
            profile_name=args.get("profile_name", ""),
            state_path=args.get("state_path"),
            state_json=args.get("state_json"),
            agent_name=agent_name,
            workspace_dir=workspace_dir,
        )
    else:
        raise ValueError(f"Tool '{name}' not found")


def execute_tool_via_daemon(port: int, name: str, args: Dict[str, Any]) -> Any:
    """Executes tool via background HTTP daemon using APIS-4D Payload Envelope 2.0."""
    context = {
        "agent_name": os.environ.get("AGYENT_AGENT_NAME", "default"),
        "workspace_dir": os.environ.get("AGYENT_AGENT_WORKSPACE", ""),
        "session_key": os.environ.get("AGYENT_SESSION_KEY", ""),
        "user_id": os.environ.get("AGYENT_USER_ID", ""),
    }
    payload = json.dumps({"context": context, "name": name, "args": args}, ensure_ascii=False).encode("utf-8")
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}/rpc",
        data=payload,
        headers={"Content-Type": "application/json; charset=utf-8"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=70.0) as resp:
            body = resp.read().decode("utf-8")
            data = json.loads(body)
            if data.get("error"):
                raise RuntimeError(data["error"])
            return data.get("result")
    except urllib.error.HTTPError as he:
        try:
            err_body = he.read().decode("utf-8", errors="replace")
            err_data = json.loads(err_body)
            if err_data.get("error"):
                raise RuntimeError(err_data["error"])
        except Exception:
            pass
        raise RuntimeError(f"HTTP Error {he.code}: {he.reason}")


def dispatch_tool(name: str, args: Dict[str, Any]) -> Any:
    """Dispatches tool to daemon or in-process fallback."""
    if not CAMOUFOX_AVAILABLE:
        return execute_tool_local(name, args)

    port = ensure_daemon_running()
    if port:
        try:
            return execute_tool_via_daemon(port, name, args)
        except Exception as e:
            log(f"Daemon RPC failed ({e}). Returning error directly to prevent pipeline hang.")
            return {
                "error": str(e),
                "tool": name,
            }
    return execute_tool_local(name, args)


def handle_message(msg: Dict[str, Any]) -> Optional[Dict[str, Any]]:
    """Handles an incoming JSON-RPC request and returns the JSON-RPC response."""
    req_id = msg.get("id")
    method = msg.get("method")

    if method == "initialize":
        # Return initialize immediately without blocking on background daemon
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
                    "version": "1.2.3"
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
            res = dispatch_tool(tool_name, args)
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


def check_health() -> Dict[str, Any]:
    """Fast health probe verifying imports, typing definitions, and Camoufox core engine."""
    return {
        "status": "ok" if CAMOUFOX_AVAILABLE else "missing_dependency",
        "camoufox_available": CAMOUFOX_AVAILABLE,
        "import_error": CAMOUFOX_IMPORT_ERROR,
        "python": sys.executable,
        "version": "1.4.0",
        "tools_count": len(TOOL_DEFINITIONS),
    }


def main() -> None:
    if "--check" in sys.argv or "--health" in sys.argv:
        status = check_health()
        sys.stdout.write(json.dumps(status, ensure_ascii=False) + "\n")
        sys.stdout.flush()
        sys.exit(0 if status["camoufox_available"] else 1)

    log("Starting Camoufox Full Capabilities Engine MCP Server v1.4.0...")
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
