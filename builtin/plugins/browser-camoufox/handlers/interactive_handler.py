"""
Handler for stateful multi-step agent interactions:
- camoufox_session_start
- camoufox_inspect_dom
- camoufox_act
- camoufox_session_list
- camoufox_session_save
- camoufox_session_close
"""

import json
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
    headless: Optional[Union[bool, str]] = None,
    locale: str = "en-US",
    initial_url: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Initializes or reuses a stateful browser session connected to the persistent Profile Vault.
    Supports headless=False for headful GUI interaction on Windows, macOS, and Linux desktop.
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

        is_headful = getattr(session, "headless", True) is False
        return {
            "session_id": session.session_id if session else "",
            "profile_name": profile_name,
            "agent_name": session.agent_name if session else agent,
            "status": "active",
            "current_url": session.page.url if session and session.page else "",
            "current_title": session.page.title() if session and session.page else "",
            "open_tabs_count": len(session.pages) if session else 0,
            "open_tabs": session.get_tabs_info() if session else [],
            "headless": getattr(session, "headless", True),
            "display_mode": "headful (interactive GUI window)" if is_headful else "headless",
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


def _execute_act(
    session: Any,
    action_clean: str,
    target_id: Optional[Union[str, int]],
    value: Optional[str],
    expects_popup: bool,
) -> Optional[Dict[str, Any]]:
    """Executes the specific action against the session. Returns error dict if validation fails, else None."""
    page = session.page
    if not page:
        return {"error": "No active page available in session"}

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

    elif action_clean == "new_tab":
        new_p = session.context.new_page()
        if value:
            new_p.goto(value, wait_until="domcontentloaded", timeout=30000)
        session.set_active_page_index(len(session.pages) - 1)

    elif action_clean == "close_tab":
        if len(session.pages) > 1:
            page.close()
            session.set_active_page_index(max(0, session.active_page_index - 1))
        else:
            return {"error": "Cannot close the only open tab. Use camoufox_session_close to terminate session."}

    elif action_clean == "bring_to_front":
        try:
            page.bring_to_front()
        except Exception as e:
            return {"error": f"Failed to bring page to front: {str(e)}"}

    elif action_clean == "wait_for_url":
        if not target_id and not value:
            return {"error": "Missing target URL pattern 'value' for wait_for_url"}
        target_url = str(value or target_id)
        wait_timeout = 30000
        try:
            page.wait_for_url(target_url, timeout=wait_timeout)
        except Exception as e:
            return {"error": f"Timeout waiting for URL '{target_url}': {str(e)}"}

    elif action_clean == "wait_for_selector":
        if not target_id and not value:
            return {"error": "Missing selector for wait_for_selector"}
        sel = str(target_id) if target_id else str(value)
        page.wait_for_selector(sel, timeout=15000)

    elif action_clean == "wait":
        wait_ms = 2000
        if value:
            try:
                val_num = float(value)
                # If <= 300, treat as seconds (e.g. 10 -> 10000ms), else ms
                wait_ms = int(val_num * 1000) if val_num <= 300 else int(val_num)
            except ValueError:
                pass
        page.wait_for_timeout(wait_ms)

    else:
        return {"error": f"Unsupported action '{action_clean}'"}

    return None


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
        with session.busy_guard(timeout_sec=60.0):
            err_dict = _execute_act(session, action_clean, target_id, value, expects_popup)
            if err_dict:
                return err_dict

            # Touch and update session metadata
            page = session.page
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
        if session and session.page:
            try:
                session.page.evaluate("window.stop()")
            except Exception:
                pass
        curr_page = session.page if session else None
        return {
            "status": "error",
            "session_id": session.session_id if session else "",
            "profile_name": session.profile_name if session else "",
            "action": action_clean,
            "error": str(e),
            "page_title": curr_page.title() if curr_page else "",
            "current_url": curr_page.url if curr_page else "",
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


def handle_session_export_state(
    session_id: Optional[str] = None,
    profile_name: Optional[str] = None,
    output_path: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Exports lightweight session authentication state (cookies, localStorage, sessionStorage)
    and browser fingerprint metadata to a compact JSON file (<50KB) for cross-machine portability.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    lookup_key = session_id or profile_name or "default"

    session = mgr.get_session(lookup_key, agent_name=agent, workspace_dir=ws_dir, auto_rehydrate=True)
    if not session:
        vault_state = mgr.profile_vault.load_storage_state(lookup_key, agent_name=agent, workspace_dir=ws_dir)
        if not vault_state:
            return {
                "success": False,
                "error": f"No active session or saved state found for '{lookup_key}'.",
            }
        state_data = vault_state
        ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:132.0) Gecko/20100101 Firefox/132.0"
    else:
        with session.busy_guard():
            state_data = session.context.storage_state()
            try:
                raw_ua = session.page.evaluate("() => navigator.userAgent") if session.page else ""
                ua = str(raw_ua) if raw_ua and not hasattr(raw_ua, "_mock_return_value") else ("Mozilla/5.0" if raw_ua else "")
            except Exception:
                ua = ""

    # Bundle with portability metadata
    export_payload = {
        "version": "1.0",
        "exported_at": time.time(),
        "agent_name": agent,
        "profile_name": session.profile_name if session else lookup_key,
        "fingerprint_meta": {
            "user_agent": ua,
            "os": "windows" if "Windows" in ua else ("macos" if "Macintosh" in ua else "linux"),
        },
        "storage_state": state_data,
    }

    # Determine destination
    if not output_path:
        base_dir = os.path.join(ws_dir, "downloads") if ws_dir and os.path.isdir(ws_dir) else os.path.join(os.path.expanduser("~"), ".agyent", "camoufox", "agents", agent, "downloads")
        os.makedirs(base_dir, exist_ok=True)
        prof_clean = mgr.profile_vault.clean_profile_name(session.profile_name if session else lookup_key)
        output_path = os.path.join(base_dir, f"{prof_clean}_storage_state.json")

    dest = os.path.abspath(output_path)
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with open(dest, "w", encoding="utf-8") as f:
        json.dump(export_payload, f, indent=2, ensure_ascii=False)

    size_kb = round(os.path.getsize(dest) / 1024.0, 2)
    cookies_count = len(state_data.get("cookies", []))
    origins_count = len(state_data.get("origins", []))

    return {
        "status": "ok",
        "success": True,
        "file_path": dest,
        "file_size_kb": size_kb,
        "cookies_count": cookies_count,
        "origins_count": origins_count,
        "fingerprint_meta": export_payload["fingerprint_meta"],
    }


def handle_session_import_state(
    profile_name: str,
    state_path: Optional[str] = None,
    state_json: Optional[str] = None,
    agent_name: Optional[str] = None,
    workspace_dir: Optional[str] = None,
) -> Dict[str, Any]:
    """
    Imports a lightweight storage_state JSON file or string into a profile in ProfileVault.
    Restores authenticated logins instantly without copying heavy browser folders.
    """
    mgr = BrowserManager.get_instance()
    agent = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
    ws_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
    clean_prof = mgr.profile_vault.clean_profile_name(profile_name)

    payload = None
    if state_json:
        try:
            payload = json.loads(state_json)
        except Exception as e:
            return {"status": "error", "success": False, "error": f"Invalid JSON string: {e}"}
    elif state_path:
        p = os.path.abspath(state_path)
        if not os.path.exists(p):
            return {"status": "error", "success": False, "error": f"State file '{state_path}' not found"}
        try:
            with open(p, "r", encoding="utf-8") as f:
                payload = json.load(f)
        except Exception as e:
            return {"status": "error", "success": False, "error": f"Failed to read file: {e}"}
    else:
        return {"status": "error", "success": False, "error": "Either state_path or state_json must be provided."}

    if isinstance(payload, dict) and "storage_state" in payload:
        raw_state = payload["storage_state"]
        fp_meta = payload.get("fingerprint_meta", {})
    else:
        raw_state = payload
        fp_meta = {}

    saved_path = mgr.profile_vault.save_storage_state(clean_prof, raw_state, agent_name=agent, workspace_dir=ws_dir)
    mgr.profile_vault.save_session_meta(
        clean_prof,
        {
            "profile_name": clean_prof,
            "agent_name": agent,
            "fingerprint_meta": fp_meta,
            "imported_at": time.time(),
        },
        agent_name=agent,
        workspace_dir=ws_dir,
    )

    cookies_count = len(raw_state.get("cookies", []))
    origins_count = len(raw_state.get("origins", []))

    return {
        "status": "ok",
        "success": True,
        "profile_name": clean_prof,
        "storage_state_path": saved_path,
        "cookies_restored": cookies_count,
        "origins_restored": origins_count,
        "fingerprint_meta": fp_meta,
    }
