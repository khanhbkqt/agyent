#!/usr/bin/env python3
"""
Camoufox Browser Background Daemon Server.
Provides long-lived, detached browser session hosting across agent turns and subagent invocations.
Zero-dependency implementation using Python standard library http.server.
"""

import http.server
import json
import os
import signal
import sys
import threading
import time
import traceback
from typing import Any, Dict, Optional

# Ensure plugin root is in python path
current_dir = os.path.dirname(os.path.abspath(__file__))
if current_dir not in sys.path:
    sys.path.insert(0, current_dir)

try:
    from core.browser_manager import BrowserManager
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
except Exception as e:
    CAMOUFOX_AVAILABLE = False

START_TIME = time.time()
DAEMON_DIR = os.path.join(os.path.expanduser("~"), ".agyent", "camoufox")
os.makedirs(DAEMON_DIR, exist_ok=True)
PORT_FILE = os.path.join(DAEMON_DIR, "daemon.port")
PID_FILE = os.path.join(DAEMON_DIR, "daemon.pid")
LOG_FILE = os.path.join(DAEMON_DIR, "daemon.log")


def log(msg: str) -> None:
    """Logs timestamped messages to stderr and daemon.log."""
    ts = time.strftime("%Y-%m-%d %H:%M:%S")
    formatted = f"[{ts}][CAMOUFOX_DAEMON] {msg}\n"
    sys.stderr.write(formatted)
    sys.stderr.flush()
    try:
        with open(LOG_FILE, "a", encoding="utf-8") as f:
            f.write(formatted)
    except Exception:
        pass


def execute_tool(
    name: str,
    args: Dict[str, Any],
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Any:
    """Dispatches tool execution to the appropriate domain handler with multi-tenant isolation."""
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    if name == "camoufox_search":
        return handle_search(
            query=args.get("query", ""),
            engine=args.get("engine", "duckduckgo"),
            locale=args.get("locale", "en-US"),
            max_results=args.get("max_results", 5),
            profile_name=args.get("profile_name"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_discover_trends":
        return handle_discover_trends(
            platform=args.get("platform", "tiktok"),
            country=args.get("country", "US"),
            category=args.get("category"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_fetch_page":
        return handle_fetch_page(
            url=args.get("url", ""),
            extract_mode=args.get("extract_mode", "markdown"),
            auto_dismiss_banners=args.get("auto_dismiss_banners", True),
            timeout_ms=args.get("timeout_ms", 30000),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent,
            workspace_dir=ws_dir,
            headless=args.get("headless"),
        )
    elif name == "camoufox_extract_json_ld":
        return handle_extract_json_ld(
            url=args.get("url", ""),
            timeout_ms=args.get("timeout_ms", 30000),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_scrape_selector":
        return handle_scrape_selector(
            url=args.get("url", ""),
            selector=args.get("selector", ""),
            fields=args.get("fields"),
            limit=args.get("limit", 20),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_intercept_api":
        return handle_intercept_api(
            url=args.get("url", ""),
            url_pattern=args.get("url_pattern"),
            wait_time_ms=args.get("wait_time_ms", 5000),
            profile_name=args.get("profile_name"),
            session_id=args.get("session_id"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_session_start":
        return handle_session_start(
            profile_name=args.get("profile_name", "default"),
            headless=args.get("headless"),
            locale=args.get("locale", "en-US"),
            initial_url=args.get("initial_url"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_inspect_dom":
        return handle_inspect_dom(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            mode=args.get("mode", "a11y_tree"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_act":
        return handle_act(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            action=args.get("action", ""),
            target_id=args.get("target_id"),
            value=args.get("value"),
            expects_popup=args.get("expects_popup", False),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_session_list":
        return handle_session_list(agent_name=agent, workspace_dir=ws_dir)
    elif name == "camoufox_session_save":
        return handle_session_save(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_session_close":
        return handle_session_close(
            session_id=args.get("session_id", ""),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_screenshot":
        return handle_screenshot(
            url=args.get("url"),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            selector=args.get("selector"),
            full_page=args.get("full_page", False),
            output_path=args.get("output_path"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_pdf_export":
        return handle_pdf_export(
            url=args.get("url"),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            output_path=args.get("output_path"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_sniff_media":
        return handle_sniff_media(
            url=args.get("url", ""),
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            target_quality=args.get("target_quality", "highest"),
            wait_time_ms=args.get("wait_time_ms", 4000),
            timeout_ms=args.get("timeout_ms", 30000),
            agent_name=agent,
            workspace_dir=ws_dir,
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
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_solve_captcha":
        return handle_solve_captcha(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            captcha_type=args.get("captcha_type", "auto"),
            timeout_ms=args.get("timeout_ms", 15000),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_session_export_state":
        return handle_session_export_state(
            session_id=args.get("session_id"),
            profile_name=args.get("profile_name"),
            output_path=args.get("output_path"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    elif name == "camoufox_session_import_state":
        return handle_session_import_state(
            profile_name=args.get("profile_name", ""),
            state_path=args.get("state_path"),
            state_json=args.get("state_json"),
            agent_name=agent,
            workspace_dir=ws_dir,
        )
    else:
        raise ValueError(f"Tool '{name}' not found")


class DaemonHTTPHandler(http.server.BaseHTTPRequestHandler):
    """Handles JSON-RPC HTTP requests strictly on localhost."""

    def log_message(self, format: str, *args: Any) -> None:
        """Suppress default stdout access logs to keep log file clean."""
        pass

    def _send_json_response(self, status_code: int, data: Any) -> None:
        payload = json.dumps(data, ensure_ascii=False).encode("utf-8")
        self.send_response(status_code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self) -> None:
        if self.path in ("/health", "/ping", "/"):
            mgr = BrowserManager.get_instance()
            uptime = time.time() - START_TIME
            active_sessions = mgr.list_active_sessions()
            self._send_json_response(200, {
                "status": "ok",
                "service": "camoufox-daemon",
                "pid": os.getpid(),
                "uptime_seconds": round(uptime, 2),
                "active_sessions_count": len(active_sessions),
                "active_sessions": active_sessions,
            })
        else:
            self._send_json_response(404, {"error": "Not Found"})

    def do_POST(self) -> None:
        if self.path == "/rpc":
            try:
                length = int(self.headers.get("Content-Length", 0))
                body = self.rfile.read(length).decode("utf-8")
                req = json.loads(body)

                tool_name = req.get("name", "")
                args = req.get("args", {})
                context = req.get("context", {})
                agent_name = context.get("agent_name") or os.environ.get("AGYENT_AGENT_NAME", "default")
                workspace_dir = context.get("workspace_dir") or os.environ.get("AGYENT_AGENT_WORKSPACE")
                log(f"Executing RPC tool: '{tool_name}' for agent: '{agent_name}'")

                res = execute_tool(tool_name, args, agent_name=agent_name, workspace_dir=workspace_dir)
                self._send_json_response(200, {"result": res, "error": None})
            except Exception as e:
                log(f"RPC execution error: {e}\n{traceback.format_exc()}")
                self._send_json_response(200, {"result": None, "error": str(e)})

        elif self.path == "/shutdown":
            log("Received shutdown request via HTTP endpoint.")
            self._send_json_response(200, {"status": "shutting_down"})

            def async_shutdown():
                time.sleep(0.5)
                cleanup_and_exit()

            threading.Thread(target=async_shutdown, daemon=True).start()
        else:
            self._send_json_response(404, {"error": "Not Found"})


def periodic_idle_watchdog(interval_sec: float = 60.0, idle_timeout_sec: float = 1200.0) -> None:
    """Background thread to periodically reap idle sessions and flush metadata."""
    while True:
        try:
            time.sleep(interval_sec)
            mgr = BrowserManager.get_instance()
            reaped = mgr.reap_idle_sessions(idle_timeout_sec=idle_timeout_sec)
            if reaped > 0:
                log(f"Watchdog reaped {reaped} idle session(s)")
        except Exception as e:
            log(f"Watchdog error: {e}")


def cleanup_and_exit(*args: Any) -> None:
    """Closes all browsers, cleans lock/port files, and exits cleanly."""
    log("Shutting down Camoufox Daemon...")
    try:
        mgr = BrowserManager.get_instance()
        mgr.close_all()
    except Exception as e:
        log(f"Error during close_all: {e}")

    try:
        if os.path.exists(PORT_FILE):
            os.remove(PORT_FILE)
        if os.path.exists(PID_FILE):
            os.remove(PID_FILE)
    except Exception:
        pass

    log("Camoufox Daemon shutdown complete.")
    sys.exit(0)


def run_daemon() -> None:
    """Binds to localhost and runs HTTP server."""
    # Register signal handlers
    try:
        signal.signal(signal.SIGINT, cleanup_and_exit)
        signal.signal(signal.SIGTERM, cleanup_and_exit)
    except Exception:
        pass

    # Determine port: try preferred port 28822, fallback to dynamic 0
    preferred_port = int(os.environ.get("CAMOUFOX_DAEMON_PORT", "28822"))
    server = None
    actual_port = preferred_port

    try:
        server = http.server.HTTPServer(("127.0.0.1", preferred_port), DaemonHTTPHandler)
    except OSError:
        log(f"Port {preferred_port} in use. Binding to dynamic port (0)...")
        server = http.server.HTTPServer(("127.0.0.1", 0), DaemonHTTPHandler)
        actual_port = server.server_address[1]

    # Write port and PID files
    with open(PORT_FILE, "w", encoding="utf-8") as f:
        f.write(str(actual_port))

    with open(PID_FILE, "w", encoding="utf-8") as f:
        f.write(str(os.getpid()))

    log(f"Camoufox Background Daemon listening on http://127.0.0.1:{actual_port} (PID: {os.getpid()})")

    # Start idle watchdog
    watchdog = threading.Thread(target=periodic_idle_watchdog, daemon=True)
    watchdog.start()

    try:
        server.serve_forever()
    except Exception as e:
        log(f"Server loop exited: {e}")
    finally:
        cleanup_and_exit()


if __name__ == "__main__":
    run_daemon()
