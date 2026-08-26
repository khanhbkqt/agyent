"""
Handler for stateful multi-step agent interactions:
- camoufox_session_start
- camoufox_inspect_dom
- camoufox_act
- camoufox_session_close
"""

import time
from typing import Any, Dict, List, Optional, Union

from core.browser_manager import BrowserManager
from kinematics.keyboard_dynamics import human_type
from kinematics.mouse_dynamics import human_click, human_move_mouse
from kinematics.scroll_controller import human_scroll
from perception.a11y_tree import extract_interactive_elements, format_a11y_tree
from perception.readability_cleaner import dismiss_cookie_banners
from perception.visual_markers import clear_visual_marks, inject_visual_marks


def handle_session_start(
    profile_name: str = "default",
    headless: bool = True,
    locale: str = "en-US",
    initial_url: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Initializes a stateful browser session connected to the Profile Vault.
    """
    mgr = BrowserManager.get_instance()
    try:
        session_id = mgr.create_session(
            profile_name=profile_name,
            headless=headless,
            locale=locale,
            enable_sniffer=True,
        )
        session = mgr.get_session(session_id)
        if initial_url and session:
            session.page.goto(initial_url, wait_until="domcontentloaded", timeout=30000)
            dismiss_cookie_banners(session.page)

        return {
            "session_id": session_id,
            "profile_name": profile_name,
            "status": "active",
            "current_url": session.page.url if session else "",
        }
    except Exception as e:
        return {
            "session_id": "",
            "profile_name": profile_name,
            "status": "error",
            "error": str(e),
        }


def handle_inspect_dom(
    session_id: str,
    mode: str = "a11y_tree",
) -> Dict[str, Any]:
    """
    Extracts the current page's interactive element tree (AOM) or injects visual marks.
    """
    mgr = BrowserManager.get_instance()
    session = mgr.get_session(session_id)
    if not session:
        return {"error": f"Session '{session_id}' not found or already closed."}

    try:
        page = session.page
        dismiss_cookie_banners(page)
        elements = extract_interactive_elements(page)

        if mode == "visual_marks":
            inject_visual_marks(page)
        else:
            clear_visual_marks(page)

        title = page.title()
        url = page.url
        tree_text = format_a11y_tree(elements, page_title=title, current_url=url)

        return {
            "session_id": session_id,
            "page_title": title,
            "current_url": url,
            "element_count": len(elements),
            "a11y_tree": tree_text,
            "elements": [e.to_dict() for e in elements],
        }
    except Exception as e:
        return {
            "session_id": session_id,
            "error": str(e),
        }


def handle_act(
    session_id: str,
    action: str,
    target_id: Optional[Union[str, int]] = None,
    value: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Executes human-like user actions on interactive page elements.
    """
    mgr = BrowserManager.get_instance()
    session = mgr.get_session(session_id)
    if not session:
        return {"error": f"Session '{session_id}' not found or already closed."}

    page = session.page
    action_clean = action.lower().strip()

    try:
        if action_clean == "navigate":
            if not value:
                return {"error": "Missing URL 'value' for navigate action"}
            page.goto(value, wait_until="domcontentloaded", timeout=30000)
            dismiss_cookie_banners(page)

        elif action_clean == "click":
            # Resolve target element
            locator = None
            if target_id is not None:
                tid = str(target_id).strip()
                if tid.isdigit():
                    locator = page.locator(f'[data-agy-id="{tid}"]')
                else:
                    locator = page.locator(tid)

            if locator and locator.count() > 0:
                box = locator.first.bounding_box()
                if box:
                    cx = box["x"] + box["width"] / 2
                    cy = box["y"] + box["height"] / 2
                    human_click(page, cx, cy)
                else:
                    locator.first.click()
            else:
                # Fallback center click
                human_click(page, 400, 300)

            # Wait a short moment for possible page transition
            time.sleep(0.5)

        elif action_clean == "type":
            if value is None:
                return {"error": "Missing 'value' text to type"}
            locator = None
            if target_id is not None:
                tid = str(target_id).strip()
                if tid.isdigit():
                    locator = page.locator(f'[data-agy-id="{tid}"]')
                else:
                    locator = page.locator(tid)

            if locator and locator.count() > 0:
                box = locator.first.bounding_box()
                if box:
                    cx = box["x"] + box["width"] / 2
                    cy = box["y"] + box["height"] / 2
                    human_click(page, cx, cy)
                else:
                    locator.first.click()
                human_type(locator.first, value)
            else:
                human_type(page, value)

        elif action_clean == "hover":
            locator = None
            if target_id is not None:
                tid = str(target_id).strip()
                if tid.isdigit():
                    locator = page.locator(f'[data-agy-id="{tid}"]')
                else:
                    locator = page.locator(tid)

            if locator and locator.count() > 0:
                box = locator.first.bounding_box()
                if box:
                    cx = box["x"] + box["width"] / 2
                    cy = box["y"] + box["height"] / 2
                    human_move_mouse(page, cx, cy)
                else:
                    locator.first.hover()

        elif action_clean == "scroll":
            delta_y = 500
            if value:
                try:
                    delta_y = int(value)
                except ValueError:
                    pass
            human_scroll(page, delta_y)

        elif action_clean == "press_key":
            key = value or "Enter"
            page.keyboard.press(key)
            time.sleep(0.3)

        elif action_clean == "wait":
            wait_ms = 2000
            if value:
                try:
                    wait_ms = int(value)
                except ValueError:
                    pass
            page.wait_for_timeout(wait_ms)

        else:
            return {"error": f"Unsupported action '{action}'"}

        return {
            "status": "ok",
            "session_id": session_id,
            "action": action_clean,
            "target_id": target_id,
            "page_title": page.title(),
            "current_url": page.url,
        }

    except Exception as e:
        return {
            "status": "error",
            "session_id": session_id,
            "action": action_clean,
            "error": str(e),
            "page_title": page.title() if page else "",
            "current_url": page.url if page else "",
        }


def handle_session_close(session_id: str) -> Dict[str, Any]:
    """
    Closes the active session and persists profile state.
    """
    mgr = BrowserManager.get_instance()
    success = mgr.close_session(session_id)
    return {
        "session_id": session_id,
        "status": "closed" if success else "not_found",
    }
