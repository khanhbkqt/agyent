"""
Handler for camoufox_intercept_api.
Intercepts and collects background XHR, Fetch, and GraphQL payloads without DOM parsing overhead.
"""

import os
from typing import Any, Dict, List, Optional

from core.browser_manager import BrowserManager
from core.network_sniffer import NetworkSniffer
from kinematics.scroll_controller import human_scroll


def handle_intercept_api(
    url: str,
    url_pattern: Optional[str] = None,
    wait_time_ms: int = 5000,
    timeout_ms: int = 30000,
    profile_name: Optional[str] = None,
    session_id: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Navigates to URL, captures matching network payloads, and returns structured responses.
    Supports persistent profile or live session interception for a specific agent.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    target_profile = profile_name
    if session_id and not target_profile:
        sess = mgr.get_session(session_id, agent_name=agent, workspace_dir=ws_dir)
        if sess:
            target_profile = sess.profile_name

    def run(page: Any) -> Dict[str, Any]:
        sniffer = NetworkSniffer(url_pattern=url_pattern)
        sniffer.attach_to_page(page)

        page.goto(url, wait_until="domcontentloaded", timeout=timeout_ms)

        # Trigger gentle human scrolling to activate lazy-loaded API calls
        human_scroll(page, 400, pause_after=0.2)

        # Wait for requested time or network idle
        wait_sec = max(1.0, wait_time_ms / 1000.0)
        page.wait_for_timeout(int(wait_sec * 1000))

        captured = sniffer.get_captured()
        return {
            "url": page.url,
            "pattern": url_pattern or "*",
            "count": len(captured),
            "payloads": captured,
        }

    try:
        return mgr.run_stateless(run, headless=True, timeout_ms=timeout_ms + wait_time_ms, profile_name=target_profile, agent_name=agent, workspace_dir=ws_dir)
    except Exception as e:
        return {
            "url": url,
            "pattern": url_pattern or "*",
            "count": 0,
            "payloads": [],
            "error": str(e),
        }
