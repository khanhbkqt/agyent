"""
Handler for camoufox_screenshot and camoufox_pdf_export.
Renders high-fidelity visual snapshots and documents from web pages.
"""

import os
import tempfile
import time
from typing import Any, Dict, Optional

from core.browser_manager import BrowserManager
from perception.readability_cleaner import dismiss_cookie_banners


def _get_default_output_file(extension: str) -> str:
    """Creates a uniquely named output path in the system temporary / agyent directory."""
    temp_dir = os.path.join(tempfile.gettempdir(), "agyent_camoufox")
    os.makedirs(temp_dir, exist_ok=True)
    ts = int(time.time() * 1000)
    return os.path.join(temp_dir, f"capture_{ts}.{extension.lstrip('.')}")


def handle_screenshot(
    url: Optional[str] = None,
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    selector: Optional[str] = None,
    full_page: bool = False,
    output_path: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Captures a screenshot of the specified URL, active session, or profile for an agent.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    dest = output_path or _get_default_output_file("png")
    os.makedirs(os.path.dirname(os.path.abspath(dest)), exist_ok=True)

    def capture(page: Any) -> Dict[str, Any]:
        if selector:
            elem = page.locator(selector)
            if elem.count() > 0:
                elem.first.screenshot(path=dest)
            else:
                page.screenshot(path=dest, full_page=full_page)
        else:
            page.screenshot(path=dest, full_page=full_page)

        vp = page.viewport_size or {"width": 1280, "height": 800}
        return {
            "file_path": os.path.abspath(dest),
            "width": vp.get("width", 1280),
            "height": vp.get("height", 800),
            "full_page": full_page,
            "selector": selector,
            "url": page.url,
        }

    try:
        lookup = session_id or profile_name
        if lookup:
            session = mgr.get_session(lookup, agent_name=agent, workspace_dir=ws_dir, auto_rehydrate=True)
            if not session or not session.page:
                return {"error": f"Session '{lookup}' not found for agent '{agent}'."}
            with session.busy_guard():
                return capture(session.page)
        elif url:
            def run(page: Any) -> Dict[str, Any]:
                page.goto(url, wait_until="domcontentloaded", timeout=30000)
                dismiss_cookie_banners(page)
                page.wait_for_timeout(1000)
                return capture(page)

            return mgr.run_stateless(run, headless=True, agent_name=agent, workspace_dir=ws_dir)
        else:
            return {"error": "Either 'url', 'session_id', or 'profile_name' must be provided."}
    except Exception as e:
        return {"error": str(e)}


def handle_pdf_export(
    url: Optional[str] = None,
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    output_path: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Exports a page as a PDF document for an agent.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    dest = output_path or _get_default_output_file("pdf")
    os.makedirs(os.path.dirname(os.path.abspath(dest)), exist_ok=True)

    def export(page: Any) -> Dict[str, Any]:
        page.pdf(path=dest, format="A4", print_background=True)
        size = os.path.getsize(dest) if os.path.exists(dest) else 0
        return {
            "file_path": os.path.abspath(dest),
            "size_bytes": size,
            "url": page.url,
        }

    try:
        lookup = session_id or profile_name
        if lookup:
            session = mgr.get_session(lookup, agent_name=agent, workspace_dir=ws_dir, auto_rehydrate=True)
            if not session or not session.page:
                return {"error": f"Session '{lookup}' not found for agent '{agent}'."}
            with session.busy_guard():
                return export(session.page)
        elif url:
            def run(page: Any) -> Dict[str, Any]:
                page.goto(url, wait_until="domcontentloaded", timeout=30000)
                dismiss_cookie_banners(page)
                page.wait_for_timeout(1000)
                return export(page)

            return mgr.run_stateless(run, headless=True, agent_name=agent, workspace_dir=ws_dir)
        else:
            return {"error": "Either 'url', 'session_id', or 'profile_name' must be provided."}
    except Exception as e:
        return {"error": str(e)}
