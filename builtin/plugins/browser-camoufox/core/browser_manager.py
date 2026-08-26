"""
Browser lifecycle manager for Camoufox.
Handles both stateless fast executions and stateful multi-step sessions with profile persistence,
thread-safe session locks, and automatic idle session garbage collection.
"""

import atexit
import sys
import threading
import time
import uuid
from typing import Any, Callable, Dict, Optional

from camoufox.sync_api import Camoufox

from .fingerprint import build_camoufox_launch_options, build_context_options
from .network_sniffer import NetworkSniffer
from .profile_vault import ProfileVault


class TabSession:
    """Represents an active stateful browser session with a dedicated tab."""

    def __init__(
        self,
        session_id: str,
        profile_name: str,
        browser: Any,
        context: Any,
        page: Any,
        sniffer: Optional[NetworkSniffer] = None,
    ):
        self.session_id = session_id
        self.profile_name = profile_name
        self.browser = browser
        self.context = context
        self.page = page
        self.sniffer = sniffer
        self.created_at = time.time()
        self.last_active = time.time()

    def touch(self) -> None:
        """Updates last active timestamp."""
        self.last_active = time.time()


class BrowserManager:
    """Central singleton/manager for Camoufox browser instances and stateful tab sessions."""

    _instance: Optional["BrowserManager"] = None
    _singleton_lock = threading.Lock()

    def __init__(self, profile_vault: Optional[ProfileVault] = None):
        self.profile_vault = profile_vault or ProfileVault()
        self.sessions: Dict[str, TabSession] = {}
        self._lock = threading.Lock()
        atexit.register(self.close_all)

    @classmethod
    def get_instance(cls) -> "BrowserManager":
        """Returns the thread-safe singleton BrowserManager instance."""
        with cls._singleton_lock:
            if cls._instance is None:
                cls._instance = cls()
            return cls._instance

    def reap_idle_sessions(self, idle_timeout_sec: float = 900.0) -> int:
        """
        Closes sessions that have remained inactive longer than idle_timeout_sec (default: 15 mins).
        Returns number of reaped sessions.
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
    ) -> Any:
        """
        Executes a one-off task using a temporary Camoufox instance with guaranteed teardown.
        """
        self.reap_idle_sessions()
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

    def create_session(
        self,
        profile_name: str = "default",
        headless: bool = True,
        locale: str = "en-US",
        enable_sniffer: bool = False,
        sniffer_pattern: Optional[str] = None,
    ) -> str:
        """
        Starts a stateful browser session with profile vault restoration.
        Returns unique session_id.
        """
        self.reap_idle_sessions()
        session_id = f"sess_{uuid.uuid4().hex[:12]}"
        launch_opts = build_camoufox_launch_options(headless=headless, locale=locale)

        # Storage state from Profile Vault
        storage_state = None
        if self.profile_vault.has_storage_state(profile_name):
            storage_state = self.profile_vault.get_storage_state_path(profile_name)

        ctx_opts = build_context_options(locale=locale, storage_state=storage_state)

        # Launch browser
        browser_cm = Camoufox(**launch_opts)
        browser = browser_cm.__enter__()

        # Setup context with storage state if present
        context = browser.new_context(**ctx_opts)
        page = context.new_page()

        sniffer = None
        if enable_sniffer:
            sniffer = NetworkSniffer(url_pattern=sniffer_pattern)
            sniffer.attach_to_page(page)

        # Store session with thread lock
        session = TabSession(
            session_id=session_id,
            profile_name=profile_name,
            browser=browser,
            context=context,
            page=page,
            sniffer=sniffer,
        )
        session._browser_cm = browser_cm

        with self._lock:
            self.sessions[session_id] = session

        sys.stderr.write(f"[CAMOUFOX_MANAGER] Created session {session_id} for profile {profile_name}\n")
        return session_id

    def get_session(self, session_id: str) -> Optional[TabSession]:
        """Retrieves an active session by ID in a thread-safe manner."""
        with self._lock:
            session = self.sessions.get(session_id)
            if session:
                session.touch()
            return session

    def close_session(self, session_id: str) -> bool:
        """
        Closes a session, saves storage state into the profile vault, and frees resources.
        """
        with self._lock:
            session = self.sessions.pop(session_id, None)

        if not session:
            return False

        try:
            # Persist cookies & storage state
            try:
                state_file = self.profile_vault.get_storage_state_path(session.profile_name)
                session.context.storage_state(path=state_file)
                sys.stderr.write(f"[CAMOUFOX_MANAGER] Saved storage state to {state_file}\n")
            except Exception as e:
                sys.stderr.write(f"[CAMOUFOX_MANAGER] Warning: Could not save storage state: {e}\n")

            # Teardown page and context
            try:
                session.page.close()
            except Exception:
                pass
            try:
                session.context.close()
            except Exception:
                pass

            # Exit browser context manager
            if hasattr(session, "_browser_cm"):
                session._browser_cm.__exit__(None, None, None)
            else:
                session.browser.close()

            sys.stderr.write(f"[CAMOUFOX_MANAGER] Closed session {session_id}\n")
            return True
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_MANAGER] Error closing session {session_id}: {e}\n")
            return False

    def close_all(self) -> None:
        """Closes all active sessions on shutdown."""
        with self._lock:
            session_ids = list(self.sessions.keys())

        for sid in session_ids:
            self.close_session(sid)
