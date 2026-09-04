"""
Autonomous Captcha & Challenge Solver for Camoufox.
Detects, targets, and solves Cloudflare Turnstile, hCaptcha, and reCAPTCHA checkboxes
using human Bézier trajectories, natural dwell-time simulation, and cross-origin iframe coordination.
"""

import os
import sys
import time
from typing import Any, Dict, List, Optional, Union

from core.browser_manager import BrowserManager
from kinematics.mouse_dynamics import human_click, human_move_mouse


TURNSTILE_IFRAME_SELECTORS = [
    'iframe[src*="challenges.cloudflare.com"]',
    'iframe[src*="turnstile"]',
    'iframe[title*="Turnstile"]',
    'iframe[title*="Cloudflare security challenge"]',
    'iframe[title*="Cloudflare"]',
    '.cf-turnstile iframe',
]

HCAPTCHA_IFRAME_SELECTORS = [
    'iframe[src*="hcaptcha.com"]',
    'iframe[title*="hCaptcha"]',
]

RECAPTCHA_IFRAME_SELECTORS = [
    'iframe[src*="recaptcha"]',
    'iframe[title*="reCAPTCHA"]',
]


def handle_solve_captcha(
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    captcha_type: str = "auto",
    timeout_ms: int = 15000,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Autonomously detects and clicks Cloudflare Turnstile or checkbox captchas using human kinematics.
    Requires disable_coop=True (default in Camoufox) to interact with cross-origin challenge iframes.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    lookup_key = session_id or profile_name or "default"

    session = mgr.get_session(lookup_key, agent_name=agent, workspace_dir=ws_dir, auto_rehydrate=True)
    if not session or not session.page:
        return {
            "status": "error",
            "solved": False,
            "error": f"Session for '{lookup_key}' not found or could not be rehydrated.",
        }

    start_time = time.time()
    deadline = start_time + (timeout_ms / 1000.0)

    try:
        with session.busy_guard():
            page = session.page

            # 1. Scan for challenge iframes
            detected_type = None
            target_frame_elem = None

            while time.time() < deadline:
                # Cloudflare Turnstile
                if captcha_type in ("auto", "turnstile"):
                    for sel in TURNSTILE_IFRAME_SELECTORS:
                        locator = page.locator(sel)
                        if locator.count() > 0 and locator.first.is_visible():
                            target_frame_elem = locator.first
                            detected_type = "turnstile"
                            break

                # hCaptcha
                if not target_frame_elem and captcha_type in ("auto", "hcaptcha"):
                    for sel in HCAPTCHA_IFRAME_SELECTORS:
                        locator = page.locator(sel)
                        if locator.count() > 0 and locator.first.is_visible():
                            target_frame_elem = locator.first
                            detected_type = "hcaptcha"
                            break

                # reCAPTCHA
                if not target_frame_elem and captcha_type in ("auto", "recaptcha"):
                    for sel in RECAPTCHA_IFRAME_SELECTORS:
                        locator = page.locator(sel)
                        if locator.count() > 0 and locator.first.is_visible():
                            target_frame_elem = locator.first
                            detected_type = "recaptcha"
                            break

                if target_frame_elem:
                    break

                time.sleep(0.5)

            if not target_frame_elem:
                return {
                    "solved": False,
                    "error": "No visible Turnstile, hCaptcha, or reCAPTCHA challenge detected on page.",
                    "elapsed_seconds": round(time.time() - start_time, 2),
                }

            # 2. Wait for challenge widget to stabilize
            time.sleep(1.0)

            # 3. Calculate target click coordinates
            box = target_frame_elem.bounding_box()
            if not box:
                return {
                    "solved": False,
                    "error": "Could not determine bounding box of challenge frame.",
                    "detected_type": detected_type,
                }

            # On Turnstile: the checkbox is horizontally positioned at ~30px from left edge, vertically centered
            # Standard Turnstile dimensions: width ~300px, height ~65px. Checkbox is at ~28px x ~32px.
            if detected_type == "turnstile":
                target_x = box["x"] + min(32, box["width"] * 0.12)
                target_y = box["y"] + (box["height"] / 2.0)
            else:
                # Standard reCAPTCHA/hCaptcha checkbox is at ~28px from left
                target_x = box["x"] + min(30, box["width"] * 0.1)
                target_y = box["y"] + (box["height"] / 2.0)

            # 4. Human Bézier movement & Dwell simulation
            human_move_mouse(page, target_x, target_y)
            # Natural human hesitation before clicking checkbox (600ms - 1100ms)
            time.sleep(0.8)
            human_click(page, target_x, target_y)

            # 5. Wait for verification token resolution
            solved = False
            token = ""
            verification_deadline = time.time() + 8.0

            while time.time() < verification_deadline:
                try:
                    # Check turnstile response input
                    resp_input = page.locator('input[name="cf-turnstile-response"]')
                    if resp_input.count() > 0:
                        val = resp_input.first.input_value()
                        if val and len(val) > 20:
                            solved = True
                            token = val[:30] + "..."
                            break

                    # Check if iframe disappeared or succeeded
                    if not target_frame_elem.is_visible():
                        solved = True
                        break

                    # Check generic g-recaptcha-response
                    g_input = page.locator('textarea[name="g-recaptcha-response"], input[name="h-captcha-response"]')
                    if g_input.count() > 0:
                        val = g_input.first.input_value()
                        if val and len(val) > 20:
                            solved = True
                            token = val[:30] + "..."
                            break
                except Exception:
                    pass

                time.sleep(0.5)

            elapsed = round(time.time() - start_time, 2)
            return {
                "solved": solved,
                "detected_type": detected_type,
                "token_preview": token if token else None,
                "elapsed_seconds": elapsed,
                "page_url": page.url,
            }

    except Exception as e:
        return {
            "solved": False,
            "error": str(e),
            "elapsed_seconds": round(time.time() - start_time, 2),
        }
