"""
Browser lifecycle manager for Camoufox.
Handles stateful multi-step sessions with native Firefox user_data_dir persistence,
multi-tab popup tracking, auto-rehydration, thread-safe session locks, and idle garbage collection.
"""

import atexit
import sys
import threading
import time
import uuid
from typing import Any, Callable, Dict, List, Optional

from camoufox.sync_api import Camoufox

from .fingerprint import build_camoufox_launch_options, build_context_options
from .network_sniffer import NetworkSniffer
from .profile_vault import ProfileVault


class TabSession:
    """Represents an active stateful browser session with multi-tab support."""

    def __init__(
        self,
        session_id: str,
        profile_name: str,
        context: Any,
        browser_cm: Any,
        sniffer: Optional[NetworkSniffer] = None,
    ):
        self.session_id = session_id
        self.profile_name = profile_name
        self.context = context
        self._browser_cm = browser_cm
        self.sniffer = sniffer
        self.active_page_index = 0
        self.created_at = time.time()
        self.last_active = time.time()

        # Listen for popup pages / new tabs
        try:
            self.context.on("page", self._on_new_page_created)
        except Exception:
            pass

    def _on_new_page_created(self, page: Any) -> None:
        """Callback triggered whenever a new tab or popup window opens."""
        sys.stderr.write(f"[CAMOUFOX_SESSION] Detected new tab/popup in session {self.session_id}\n")
        self.touch()
        # Automatically focus the new popup tab
        try:
            pages = self.context.pages
            if page in pages:
                self.active_page_index = pages.index(page)
        except Exception:
            pass

    @property
    def pages(self) -> List[Any]:
        """Returns list of open pages/tabs in the context."""
        try:
            return self.context.pages
        except Exception:
            return []

    @property
    def page(self) -> Any:
        """Returns the current active page/tab."""
        pages = self.pages
        if not pages:
            try:
                p = self.context.new_page()
                return p
            except Exception:
                return None

        if 0 <= self.active_page_index < len(pages):
            return pages[self.active_page_index]
        self.active_page_index = len(pages) - 1
        return pages[self.active_page_index]

    def set_active_page_index(self, index: int) -> bool:
        """Switches the active tab focus to the given index."""
        pages = self.pages
        if 0 <= index < len(pages):
            self.active_page_index = index
            self.touch()
            return True
        return False

    def touch(self) -> None:
        """Updates last active timestamp."""
        self.last_active = time.time()

    def get_tabs_info(self) -> List[Dict[str, Any]]:
        """Returns metadata for all open tabs."""
        tabs = []
        for idx, p in enumerate(self.pages):
            try:
                title = p.title()
                url = p.url
            except Exception:
                title, url = "Unknown", ""
            tabs.append({
                "index": idx,
                "title": title,
                "url": url,
                "is_active": (idx == self.active_page_index),
            })
        return tabs

    def sync_to_vault(self, vault: ProfileVault) -> None:
        """Flushes storage state and metadata to profile vault on disk."""
        try:
            tabs_info = self.get_tabs_info()
            curr_url = self.page.url if self.page else ""
            curr_title = self.page.title() if self.page else ""
            vault.save_session_meta(self.profile_name, {
                "session_id": self.session_id,
                "profile_name": self.profile_name,
                "last_url": curr_url,
                "page_title": curr_title,
                "tabs": tabs_info,
                "active_tab_index": self.active_page_index,
            })
            # Also try saving storage_state.json backup
            state_file = vault.get_storage_state_path(self.profile_name)
            self.context.storage_state(path=state_file)
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_SESSION] Warning: State sync error: {e}\n")


class BrowserManager:
    """Central manager for persistent Camoufox browser sessions with auto-rehydration."""

    _instance: Optional["BrowserManager"] = None
    _singleton_lock = threading.Lock()

    def __init__(self, profile_vault: Optional[ProfileVault] = None):
        self.profile_vault = profile_vault or ProfileVault()
        self.sessions: Dict[str, TabSession] = {}
        self.profile_to_session: Dict[str, str] = {}
        self._lock = threading.Lock()
        atexit.register(self.close_all)

    @classmethod
    def get_instance(cls) -> "BrowserManager":
        """Returns the thread-safe singleton BrowserManager instance."""
        with cls._singleton_lock:
            if cls._instance is None:
                cls._instance = cls()
            return cls._instance

    def reap_idle_sessions(self, idle_timeout_sec: float = 1200.0) -> int:
        """
        Closes sessions that have remained inactive longer than idle_timeout_sec (default: 20 mins).
        Saves metadata before tearing down memory references.
        """
        now = time.time()
        stale_ids = []
        with self._lock:
            for sid, sess in self.sessions.items():
                if now - sess.last_active > idle_timeout_sec:
                    stale_ids.append(sid)

        for sid in stale_ids:
            sys.stderr.write(f"[CAMOUFOX_MANAGER] Reaping idle session {sid}\n")
            self.close_session(sid)

        return len(stale_ids)

    def run_stateless(
        self,
        handler: Callable[[Any], Any],
        headless: bool = True,
        locale: str = "en-US",
        timeout_ms: int = 30000,
        profile_name: Optional[str] = None,
    ) -> Any:
        """
        Executes a task either with a temporary context or inside a named persistent profile.
        """
        self.reap_idle_sessions()
        if profile_name:
            # Execute within stateful profile
            session = self.get_or_create_session(profile_name=profile_name, headless=headless, locale=locale)
            page = session.page
            page.set_default_timeout(timeout_ms)
            session.touch()
            res = handler(page)
            session.sync_to_vault(self.profile_vault)
            return res

        # Pure temporary stateless launch
        launch_opts = build_camoufox_launch_options(headless=headless, locale=locale)
        ctx_opts = build_context_options(locale=locale)

        with Camoufox(**launch_opts) as browser:
            context = browser.new_context(**ctx_opts)
            page = context.new_page()
            page.set_default_timeout(timeout_ms)
            try:
                return handler(page)
            finally:
                try:
                    page.close()
                except Exception:
                    pass
                try:
                    context.close()
                except Exception:
                    pass

    def get_or_create_session(
        self,
        profile_name: str = "default",
        headless: bool = True,
        locale: str = "en-US",
        initial_url: Optional[str] = None,
        enable_sniffer: bool = False,
        sniffer_pattern: Optional[str] = None,
    ) -> TabSession:
        """
        Retrieves existing active session for profile_name or creates a new one.
        """
        with self._lock:
            existing_sid = self.profile_to_session.get(profile_name)
            if existing_sid and existing_sid in self.sessions:
                sess = self.sessions[existing_sid]
                sess.touch()
                if initial_url and sess.page:
                    try:
                        sess.page.goto(initial_url, wait_until="domcontentloaded", timeout=30000)
                    except Exception:
                        pass
                return sess

        sid = self.create_session(
            profile_name=profile_name,
            headless=headless,
            locale=locale,
            enable_sniffer=enable_sniffer,
            sniffer_pattern=sniffer_pattern,
        )
        sess = self.get_session(sid)
        if initial_url and sess and sess.page:
            try:
                sess.page.goto(initial_url, wait_until="domcontentloaded", timeout=30000)
            except Exception:
                pass
        return sess

    def create_session(
        self,
        profile_name: str = "default",
        headless: bool = True,
        locale: str = "en-US",
        enable_sniffer: bool = False,
        sniffer_pattern: Optional[str] = None,
    ) -> str:
        """
        Starts a persistent browser session with native Firefox user_data_dir.
        """
        self.reap_idle_sessions()
        clean_profile = self.profile_vault.clean_profile_name(profile_name)

        # Check for profile lock conflict
        if self.profile_vault.is_profile_locked(clean_profile):
            # Check if we own this session already
            with self._lock:
                if clean_profile in self.profile_to_session:
                    return self.profile_to_session[clean_profile]
            sys.stderr.write(f"[CAMOUFOX_MANAGER] Warning: Profile {clean_profile} parent.lock detected\n")

        session_id = f"sess_{uuid.uuid4().hex[:12]}"
        user_data_dir = self.profile_vault.get_user_data_dir(clean_profile)
        launch_opts = build_camoufox_launch_options(headless=headless, locale=locale)
        ctx_opts = build_context_options(locale=locale)

        # Merge context options into persistent context launch options
        launch_opts.update({
            "persistent_context": True,
            "user_data_dir": user_data_dir,
            "locale": ctx_opts.get("locale", "en-US"),
            "timezone_id": ctx_opts.get("timezone_id", "America/New_York"),
            "geolocation": ctx_opts.get("geolocation"),
            "permissions": ctx_opts.get("permissions", ["geolocation"]),
        })

        # Launch persistent context
        browser_cm = Camoufox(**launch_opts)
        context = browser_cm.__enter__()

        # Ensure at least one tab is open
        if not context.pages:
            context.new_page()

        page = context.pages[0]

        sniffer = None
        if enable_sniffer:
            sniffer = NetworkSniffer(url_pattern=sniffer_pattern)
            sniffer.attach_to_page(page)

        session = TabSession(
            session_id=session_id,
            profile_name=clean_profile,
            context=context,
            browser_cm=browser_cm,
            sniffer=sniffer,
        )

        with self._lock:
            self.sessions[session_id] = session
            self.profile_to_session[clean_profile] = session_id

        # Sync initial metadata
        session.sync_to_vault(self.profile_vault)
        sys.stderr.write(f"[CAMOUFOX_MANAGER] Created persistent session {session_id} for profile {clean_profile}\n")
        return session_id

    def get_session(self, session_id_or_profile: str, auto_rehydrate: bool = True) -> Optional[TabSession]:
        """
        Retrieves active session by session_id or profile_name.
        If missing in memory, auto-rehydrates from profile vault session_meta.json.
        """
        if not session_id_or_profile:
            return None

        with self._lock:
            # 1. Direct session_id match
            if session_id_or_profile in self.sessions:
                sess = self.sessions[session_id_or_profile]
                sess.touch()
                return sess

            # 2. profile_name match
            if session_id_or_profile in self.profile_to_session:
                sid = self.profile_to_session[session_id_or_profile]
                if sid in self.sessions:
                    sess = self.sessions[sid]
                    sess.touch()
                    return sess

        # 3. Auto-Rehydration from disk
        if auto_rehydrate:
            profile_name = session_id_or_profile
            # Check if this identifier has session metadata
            meta = self.profile_vault.load_session_meta(profile_name)
            if not meta:
                # Try search across all profiles to match session_id
                for p in self.profile_vault.list_profiles():
                    p_meta = self.profile_vault.load_session_meta(p["name"])
                    if p_meta and p_meta.get("session_id") == session_id_or_profile:
                        profile_name = p["name"]
                        meta = p_meta
                        break

            if meta or self.profile_vault.has_storage_state(profile_name):
                sys.stderr.write(f"[CAMOUFOX_MANAGER] Auto-rehydrating session for profile {profile_name}...\n")
                last_url = meta.get("last_url") if meta else None
                sess = self.get_or_create_session(profile_name=profile_name, initial_url=last_url)
                return sess

        return None

    def save_session(self, session_id_or_profile: str) -> bool:
        """Explicitly checkpoints session state to disk without closing."""
        sess = self.get_session(session_id_or_profile, auto_rehydrate=False)
        if not sess:
            return False
        sess.sync_to_vault(self.profile_vault)
        return True

    def close_session(self, session_id: str) -> bool:
        """
        Closes a session, saves storage state and tabs metadata into the profile vault, and frees resources.
        """
        with self._lock:
            session = self.sessions.pop(session_id, None)
            if session:
                self.profile_to_session.pop(session.profile_name, None)

        if not session:
            return False

        try:
            # Persist state
            session.sync_to_vault(self.profile_vault)

            # Teardown context pages
            try:
                for p in session.pages:
                    try:
                        p.close()
                    except Exception:
                        pass
                session.context.close()
            except Exception:
                pass

            # Exit browser context manager
            if hasattr(session, "_browser_cm") and session._browser_cm:
                try:
                    session._browser_cm.__exit__(None, None, None)
                except Exception:
                    pass

            sys.stderr.write(f"[CAMOUFOX_MANAGER] Closed session {session_id}\n")
            return True
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_MANAGER] Error closing session {session_id}: {e}\n")
            return False

    def list_active_sessions(self) -> List[Dict[str, Any]]:
        """Lists all currently active in-memory browser sessions."""
        res = []
        with self._lock:
            for sid, sess in self.sessions.items():
                curr_url = ""
                curr_title = ""
                try:
                    if sess.page:
                        curr_url = sess.page.url
                        curr_title = sess.page.title()
                except Exception:
                    pass
                res.append({
                    "session_id": sid,
                    "profile_name": sess.profile_name,
                    "created_at": sess.created_at,
                    "last_active": sess.last_active,
                    "current_url": curr_url,
                    "current_title": curr_title,
                    "open_tabs": sess.get_tabs_info(),
                })
        return res

    def close_all(self) -> None:
        """Closes all active sessions on shutdown."""
        with self._lock:
            session_ids = list(self.sessions.keys())

        for sid in session_ids:
            self.close_session(sid)

