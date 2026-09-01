"""
Handler for stateful multi-step agent interactions:
- camoufox_session_start
- camoufox_inspect_dom
- camoufox_act
- camoufox_session_list
- camoufox_session_save
- camoufox_session_close
"""

import os
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
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Initializes or reuses a stateful browser session connected to the persistent Profile Vault.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    try:
        session = mgr.get_or_create_session(
            profile_name=profile_name,
            agent_name=agent,
            workspace_dir=ws_dir,
            headless=headless,
            locale=locale,
            initial_url=initial_url,
            enable_sniffer=True,
        )
        if session and initial_url and session.page:
            try:
                with session.busy_guard():
                    dismiss_cookie_banners(session.page)
            except Exception:
                pass

        return {
            "session_id": session.session_id if session else "",
            "profile_name": profile_name,
            "agent_name": session.agent_name if session else agent,
            "status": "active",
            "current_url": session.page.url if session and session.page else "",
            "current_title": session.page.title() if session and session.page else "",
            "open_tabs_count": len(session.pages) if session else 0,
            "open_tabs": session.get_tabs_info() if session else [],
        }
    except Exception as e:
        return {
            "session_id": "",
            "profile_name": profile_name,
            "agent_name": agent,
            "status": "error",
            "error": str(e),
        }


def handle_inspect_dom(
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    mode: str = "a11y_tree",
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Extracts the current page's interactive element tree (AOM) or injects visual marks.
    Supports auto-rehydration by session_id or profile_name for a specific agent.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    lookup_key = session_id or profile_name or "default"
    session = mgr.get_session(lookup_key, agent_name=agent, workspace_dir=ws_dir, auto_rehydrate=True)
    if not session or not session.page:
        return {"error": f"Session for '{lookup_key}' not found or could not be rehydrated for agent '{agent}'."}

    try:
        with session.busy_guard():
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
                "session_id": session.session_id,
                "profile_name": session.profile_name,
                "page_title": title,
                "current_url": url,
                "active_tab_index": session.active_page_index,
                "open_tabs_count": len(session.pages),
                "element_count": len(elements),
                "a11y_tree": tree_text,
                "elements": [e.to_dict() for e in elements],
            }
    except Exception as e:
        return {
            "session_id": session.session_id if session else "",
            "error": str(e),
        }


def handle_act(
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    action: str = "",
    target_id: Optional[Union[str, int]] = None,
    value: Optional[str] = None,
    expects_popup: bool = False,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Executes human-like user actions on interactive page elements.
    Supports clicks with synchronous popup capture, tab switching, select, check, file upload, and auto-rehydration.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    lookup_key = session_id or profile_name or "default"
    session = mgr.get_session(lookup_key, agent_name=agent, workspace_dir=ws_dir, auto_rehydrate=True)
    if not session or not session.page:
        return {"error": f"Session for '{lookup_key}' not found or could not be rehydrated for agent '{agent}'."}

    action_clean = action.lower().strip()

    try:
        with session.busy_guard():
            page = session.page
        if action_clean == "navigate":
            if not value:
                return {"error": "Missing URL 'value' for navigate action"}
            page.goto(value, wait_until="domcontentloaded", timeout=30000)
            dismiss_cookie_banners(page)

        elif action_clean == "click":
            # If expects_popup is enabled, wrap click in context.expect_page()
            def do_click():
                if target_id is not None and "," in str(target_id):
                    try:
                        parts = str(target_id).split(",")
                        cx, cy = float(parts[0].strip()), float(parts[1].strip())
                        human_click(page, cx, cy)
                    except Exception:
                        human_click(page, 400, 300)
                elif value is not None and "," in str(value):
                    try:
                        parts = str(value).split(",")
                        cx, cy = float(parts[0].strip()), float(parts[1].strip())
                        human_click(page, cx, cy)
                    except Exception:
                        human_click(page, 400, 300)
                else:
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
                        human_click(page, 400, 300)

            if expects_popup:
                try:
                    with session.context.expect_page(timeout=8000) as new_page_info:
                        do_click()
                    new_page = new_page_info.value
                    new_page.wait_for_load_state("domcontentloaded", timeout=15000)
                    session.touch()
                except Exception:
                    do_click()
            else:
                do_click()

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

        elif action_clean == "select_option":
            locator = None
            if target_id is not None:
                tid = str(target_id).strip()
                if tid.isdigit():
                    locator = page.locator(f'[data-agy-id="{tid}"]')
                else:
                    locator = page.locator(tid)
            if not locator or locator.count() == 0:
                return {"error": f"Element '{target_id}' not found for select_option"}
            locator.first.select_option(value or "")

        elif action_clean in ("check", "uncheck"):
            locator = None
            if target_id is not None:
                tid = str(target_id).strip()
                if tid.isdigit():
                    locator = page.locator(f'[data-agy-id="{tid}"]')
                else:
                    locator = page.locator(tid)
            if not locator or locator.count() == 0:
                return {"error": f"Element '{target_id}' not found for {action_clean}"}
            if action_clean == "check":
                locator.first.check()
            else:
                locator.first.uncheck()

        elif action_clean == "upload_file":
            if not value:
                return {"error": "Missing file path 'value' for upload_file"}
            locator = None
            if target_id is not None:
                tid = str(target_id).strip()
                if tid.isdigit():
                    locator = page.locator(f'[data-agy-id="{tid}"]')
                else:
                    locator = page.locator(tid)
            if locator and locator.count() > 0:
                locator.first.set_input_files(value)
            else:
                page.set_input_files('input[type="file"]', value)

        elif action_clean == "go_back":
            page.go_back(wait_until="domcontentloaded", timeout=15000)

        elif action_clean == "go_forward":
            page.go_forward(wait_until="domcontentloaded", timeout=15000)

        elif action_clean == "reload":
            page.reload(wait_until="domcontentloaded", timeout=20000)

        elif action_clean == "switch_tab":
            tab_idx = 0
            if value:
                try:
                    tab_idx = int(value)
                except ValueError:
                    pass
            success = session.set_active_page_index(tab_idx)
            if not success:
                return {"error": f"Tab index {tab_idx} is out of bounds (open tabs: {len(session.pages)})"}
            page = session.page

        elif action_clean == "new_tab":
            new_p = session.context.new_page()
            if value:
                new_p.goto(value, wait_until="domcontentloaded", timeout=30000)
            session.set_active_page_index(len(session.pages) - 1)
            page = session.page

        elif action_clean == "close_tab":
            if len(session.pages) > 1:
                page.close()
                session.set_active_page_index(max(0, session.active_page_index - 1))
                page = session.page
            else:
                return {"error": "Cannot close the only open tab. Use camoufox_session_close to terminate session."}

        elif action_clean == "wait_for_selector":
            if not target_id and not value:
                return {"error": "Missing selector for wait_for_selector"}
            sel = str(target_id) if target_id else str(value)
            page.wait_for_selector(sel, timeout=15000)

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

        # Touch and update session metadata
        session.touch()
        session.sync_to_vault(mgr.profile_vault)

        return {
            "status": "ok",
            "session_id": session.session_id,
            "profile_name": session.profile_name,
            "action": action_clean,
            "target_id": target_id,
            "active_tab_index": session.active_page_index,
            "open_tabs_count": len(session.pages),
            "page_title": page.title() if page else "",
            "current_url": page.url if page else "",
        }

    except Exception as e:
        return {
            "status": "error",
            "session_id": session.session_id if session else "",
            "profile_name": session.profile_name if session else "",
            "action": action_clean,
            "error": str(e),
            "page_title": page.title() if page else "",
            "current_url": page.url if page else "",
        }


def handle_session_list(
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Lists all active live sessions and persistent profiles stored in Profile Vault for an agent.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    active_sessions = mgr.list_active_sessions(agent_name=agent, workspace_dir=ws_dir)
    saved_profiles = mgr.profile_vault.list_profiles(agent_name=agent, workspace_dir=ws_dir)

    return {
        "active_sessions_count": len(active_sessions),
        "active_sessions": active_sessions,
        "saved_profiles_count": len(saved_profiles),
        "saved_profiles": saved_profiles,
    }


def handle_session_save(
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Explicitly saves active session cookies, DOM metadata, and state to disk without closing.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    lookup_key = session_id or profile_name or "default"
    success = mgr.save_session(lookup_key, agent_name=agent, workspace_dir=ws_dir)
    return {
        "lookup_key": lookup_key,
        "status": "saved" if success else "not_found",
    }


def handle_session_close(
    session_id: str,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Closes the active session and persists profile state enforcing agent ownership.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    success = mgr.close_session(session_id, agent_name=agent, workspace_dir=ws_dir)
    return {
        "session_id": session_id,
        "status": "closed" if success else "not_found",
    }
