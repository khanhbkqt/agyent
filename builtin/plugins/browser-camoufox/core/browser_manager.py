"""
Browser lifecycle manager for Camoufox.
Handles stateful multi-step sessions with native Firefox user_data_dir persistence,
multi-tenant agent isolation, multi-tab popup tracking, auto-rehydration, thread-safe session locks,
and idle garbage collection.
"""

import atexit
import contextlib
import os
import sys
import threading
import time
from typing import Any, Callable, Dict, List, Optional, Tuple, Union
import uuid
try:
    from camoufox.sync_api import Camoufox
except Exception:
    Camoufox = None

from .fingerprint import build_camoufox_launch_options, build_context_options, parse_headless_option
from .network_sniffer import NetworkSniffer
from .profile_vault import ProfileVault


class TabSession:
    """Represents an active stateful browser session with multi-tab support and agent isolation."""

    def __init__(
        self,
        session_id: str,
        profile_name: str,
        context: Any,
        browser_cm: Any,
        sniffer: Optional[NetworkSniffer] = None,
        agent_name: str = "default",
        workspace_dir: Optional[str] = None,
        headless: Union[bool, str] = True,
    ):
        self.session_id = session_id
        self.profile_name = profile_name
        self.agent_name = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
        self.workspace_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
        self.headless = headless
        self.context = context
        self._browser_cm = browser_cm
        self.sniffer = sniffer
        self.active_page_index = 0
        self.created_at = time.time()
        self.last_active = time.time()
        self.lock = threading.Lock()
        self.is_busy = False

        # Listen for popup pages / new tabs
        try:
            self.context.on("page", self._on_new_page_created)
        except Exception:
            pass

    @contextlib.contextmanager
    def busy_guard(self):
        """Context manager to mark session as busy and protect against concurrent reaping or closing."""
        with self.lock:
            self.is_busy = True
            self.touch()
            try:
                yield self
            finally:
                self.is_busy = False
                self.touch()

    def _on_new_page_created(self, page: Any) -> None:
        """Callback triggered whenever a new tab or popup window opens."""
        sys.stderr.write(f"[CAMOUFOX_SESSION] Detected new tab/popup in session {self.session_id} (agent: {self.agent_name})\n")
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
            vault.save_session_meta(
                self.profile_name,
                {
                    "session_id": self.session_id,
                    "profile_name": self.profile_name,
                    "agent_name": self.agent_name,
                    "last_url": curr_url,
                    "page_title": curr_title,
                    "tabs": tabs_info,
                    "active_tab_index": self.active_page_index,
                },
                agent_name=self.agent_name,
                workspace_dir=self.workspace_dir,
            )
            # Also try saving storage_state.json backup
            state_file = vault.get_storage_state_path(self.profile_name, agent_name=self.agent_name, workspace_dir=self.workspace_dir)
            self.context.storage_state(path=state_file)
        except Exception as e:
            sys.stderr.write(f"[CAMOUFOX_SESSION] Warning: State sync error: {e}\n")


class BrowserManager:
    """Central manager for persistent Camoufox browser sessions with multi-tenant isolation and auto-rehydration."""

    _instance: Optional["BrowserManager"] = None
    _singleton_lock = threading.Lock()

    def __init__(self, profile_vault: Optional[ProfileVault] = None):
        self.profile_vault = profile_vault or ProfileVault()
        self.sessions: Dict[str, TabSession] = {}
        self.profile_to_session: Dict[str, str] = {}  # key: f"{agent_name}:{profile_name}" -> session_id
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
        Checks if session is busy and acquires session lock non-blockingly to prevent reaping active sessions mid-execution.
        """
        now = time.time()
        stale_ids = []
        with self._lock:
            for sid, sess in list(self.sessions.items()):
                if sess.is_busy:
                    continue
                if now - sess.last_active > idle_timeout_sec:
                    # Attempt non-blocking lock acquisition to ensure no background task is using it
                    acquired = sess.lock.acquire(blocking=False)
                    if acquired:
                        try:
                            if not sess.is_busy and (now - sess.last_active > idle_timeout_sec):
                                stale_ids.append(sid)
                        finally:
                            sess.lock.release()

        reaped_count = 0
        for sid in stale_ids:
            sys.stderr.write(f"[CAMOUFOX_MANAGER] Reaping idle session {sid}\n")
            if self.close_session(sid):
                reaped_count += 1

        return reaped_count

    def run_stateless(
        self,
        handler: Callable[[Any], Any],
        headless: Optional[Union[bool, str]] = None,
        locale: str = "en-US",
        timeout_ms: int = 30000,
        profile_name: Optional[str] = None,
        agent_name: str = "default",
        workspace_dir: Optional[str] = None,
        proxy: Optional[Dict[str, str]] = None,
        enable_adblock: bool = True,
        block_images: bool = False,
        os_target: Optional[Union[str, List[str]]] = None,
        **extra_opts: Any,
    ) -> Any:
        """
        Executes a task either with a temporary context or inside a named persistent profile for an agent.
        """
        self.reap_idle_sessions()
        norm_headless = parse_headless_option(headless)

        if profile_name:
            # Execute within stateful profile
            session = self.get_or_create_session(
                profile_name=profile_name,
                agent_name=agent_name,
                workspace_dir=workspace_dir,
                headless=norm_headless,
                locale=locale,
                proxy=proxy,
                enable_adblock=enable_adblock,
                block_images=block_images,
                os_target=os_target,
                **extra_opts,
            )
            with session.busy_guard():
                page = session.page
                page.set_default_timeout(timeout_ms)
                if norm_headless is False:
                    try:
                        page.bring_to_front()
                    except Exception:
                        pass
                res = handler(page)
                session.sync_to_vault(self.profile_vault)
                return res

        # Pure temporary stateless launch
        launch_opts = build_camoufox_launch_options(
            headless=norm_headless,
            locale=locale,
            proxy=proxy,
            enable_adblock=enable_adblock,
            block_images=block_images,
            os_target=os_target,
            extra_options=extra_opts,
        )
        ctx_opts = build_context_options(locale=locale)
        if launch_opts.get("geoip"):
            ctx_opts.pop("timezone_id", None)
            ctx_opts.pop("geolocation", None)

        with Camoufox(**launch_opts) as browser:
            context = browser.new_context(**ctx_opts)
            page = context.new_page()
            page.set_default_timeout(timeout_ms)
            if norm_headless is False:
                try:
                    page.bring_to_front()
                except Exception:
                    pass
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
        agent_name: str = "default",
        workspace_dir: Optional[str] = None,
        headless: Optional[Union[bool, str]] = None,
        locale: str = "en-US",
        initial_url: Optional[str] = None,
        enable_sniffer: bool = False,
        sniffer_pattern: Optional[str] = None,
        proxy: Optional[Dict[str, str]] = None,
        enable_adblock: bool = True,
        block_images: bool = False,
        os_target: Optional[Union[str, List[str]]] = None,
        **extra_opts: Any,
    ) -> TabSession:
        """
        Retrieves existing active session for agent_name:profile_name or creates a new one.
        If existing session's headless mode differs from requested mode (e.g. headless -> headful),
        recycles the session so the visible window appears.
        """
        clean_agent = self.profile_vault.clean_agent_name(agent_name)
        clean_prof = self.profile_vault.clean_profile_name(profile_name)
        lookup_key = f"{clean_agent}:{clean_prof}"
        norm_headless = parse_headless_option(headless)

        existing_sid_to_close = None
        with self._lock:
            existing_sid = self.profile_to_session.get(lookup_key)
            if existing_sid and existing_sid in self.sessions:
                sess = self.sessions[existing_sid]
                active_headless = getattr(sess, "headless", True)
                if active_headless == norm_headless:
                    sess.touch()
                    if initial_url and sess.page:
                        try:
                            with sess.busy_guard():
                                sess.page.goto(initial_url, wait_until="domcontentloaded", timeout=30000)
                        except Exception:
                            pass
                    if norm_headless is False and sess.page:
                        try:
                            sess.page.bring_to_front()
                        except Exception:
                            pass
                    return sess
                else:
                    sys.stderr.write(
                        f"[CAMOUFOX_MANAGER] Mode switch for '{clean_prof}': headless={active_headless} -> {norm_headless}. Recycling session.\n"
                    )
                    existing_sid_to_close = existing_sid

        if existing_sid_to_close:
            self.close_session(existing_sid_to_close, agent_name=clean_agent, workspace_dir=workspace_dir)

        sid = self.create_session(
            profile_name=clean_prof,
            agent_name=clean_agent,
            workspace_dir=workspace_dir,
            headless=norm_headless,
            locale=locale,
            enable_sniffer=enable_sniffer,
            sniffer_pattern=sniffer_pattern,
            proxy=proxy,
            enable_adblock=enable_adblock,
            block_images=block_images,
            os_target=os_target,
            **extra_opts,
        )
        sess = self.get_session(sid, agent_name=clean_agent, workspace_dir=workspace_dir)
        if initial_url and sess and sess.page:
            try:
                with sess.busy_guard():
                    sess.page.goto(initial_url, wait_until="domcontentloaded", timeout=30000)
            except Exception:
                pass
        if norm_headless is False and sess and sess.page:
            try:
                sess.page.bring_to_front()
            except Exception:
                pass
        return sess

    def create_session(
        self,
        profile_name: str = "default",
        agent_name: str = "default",
        workspace_dir: Optional[str] = None,
        headless: Optional[Union[bool, str]] = None,
        locale: str = "en-US",
        enable_sniffer: bool = False,
        sniffer_pattern: Optional[str] = None,
        proxy: Optional[Dict[str, str]] = None,
        enable_adblock: bool = True,
        block_images: bool = False,
        os_target: Optional[Union[str, List[str]]] = None,
        **extra_opts: Any,
    ) -> str:
        """
        Starts a persistent browser session with native Firefox user_data_dir for a specific agent.
        """
        self.reap_idle_sessions()
        clean_agent = self.profile_vault.clean_agent_name(agent_name)
        clean_profile = self.profile_vault.clean_profile_name(profile_name)
        lookup_key = f"{clean_agent}:{clean_profile}"
        norm_headless = parse_headless_option(headless)

        # Check for profile lock conflict and clean orphan locks
        self.profile_vault.clean_stale_locks(clean_profile, agent_name=clean_agent, workspace_dir=workspace_dir)
        if self.profile_vault.is_profile_locked(clean_profile, agent_name=clean_agent, workspace_dir=workspace_dir):
            # Check if we own this session already
            with self._lock:
                if lookup_key in self.profile_to_session:
                    return self.profile_to_session[lookup_key]
            sys.stderr.write(f"[CAMOUFOX_MANAGER] Warning: Profile {clean_profile} (agent: {clean_agent}) actively locked, falling back to ephemeral profile\n")
            clean_profile = f"{clean_profile}_eph_{uuid.uuid4().hex[:6]}"

        session_id = f"sess_{uuid.uuid4().hex[:12]}"
        user_data_dir = self.profile_vault.get_user_data_dir(clean_profile, agent_name=clean_agent, workspace_dir=workspace_dir)
        launch_opts = build_camoufox_launch_options(
            headless=norm_headless,
            locale=locale,
            proxy=proxy,
            enable_adblock=enable_adblock,
            block_images=block_images,
            os_target=os_target,
            extra_options=extra_opts,
        )
        ctx_opts = build_context_options(locale=locale)

        # Merge context options into persistent context launch options
        launch_opts.update({
            "persistent_context": True,
            "user_data_dir": user_data_dir,
            "permissions": ctx_opts.get("permissions", ["geolocation"]),
            "ignore_https_errors": True,
        })
        if not launch_opts.get("geoip"):
            launch_opts.update({
                "locale": ctx_opts.get("locale", "en-US"),
                "timezone_id": ctx_opts.get("timezone_id", "America/New_York"),
                "geolocation": ctx_opts.get("geolocation"),
            })

        # Launch persistent context
        browser_cm = Camoufox(**launch_opts)
        context = browser_cm.__enter__()

        # Ensure at least one tab is open
        if not context.pages:
            context.new_page()

        page = context.pages[0]
        if norm_headless is False and page:
            try:
                page.bring_to_front()
            except Exception:
                pass

        sniffer = None
        if enable_sniffer:
            sniffer = NetworkSniffer(url_pattern=sniffer_pattern)
            sniffer.attach_to_page(page)

        session = TabSession(
            session_id=session_id,
            profile_name=clean_profile,
            agent_name=clean_agent,
            workspace_dir=workspace_dir,
            context=context,
            browser_cm=browser_cm,
            sniffer=sniffer,
            headless=norm_headless,
        )

        with self._lock:
            self.sessions[session_id] = session
            self.profile_to_session[lookup_key] = session_id

        # Sync initial metadata
        session.sync_to_vault(self.profile_vault)
        sys.stderr.write(f"[CAMOUFOX_MANAGER] Created persistent session {session_id} for profile {clean_profile} (agent: {clean_agent})\n")
        return session_id

    def get_session(
        self,
        session_id_or_profile: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
        auto_rehydrate: bool = True,
    ) -> Optional[TabSession]:
        """
        Retrieves active session by session_id or profile_name enforcing agent ownership.
        If missing in memory, auto-rehydrates from profile vault session_meta.json.
        """
        if not session_id_or_profile:
            return None

        clean_agent = self.profile_vault.clean_agent_name(agent_name) if agent_name else None

        with self._lock:
            # 1. Direct session_id match
            if session_id_or_profile in self.sessions:
                sess = self.sessions[session_id_or_profile]
                if clean_agent is not None and sess.agent_name != clean_agent:
                    # Multi-tenant ownership guard: unauthorized agent cannot access another agent's session
                    sys.stderr.write(f"[CAMOUFOX_MANAGER] Access Denied: Agent '{clean_agent}' attempted to access session '{session_id_or_profile}' owned by '{sess.agent_name}'\n")
                    return None
                sess.touch()
                return sess

            # 2. profile_name match with agent namespace
            lookup_agent = clean_agent or "default"
            clean_profile = self.profile_vault.clean_profile_name(session_id_or_profile)
            lookup_key = f"{lookup_agent}:{clean_profile}"
            if lookup_key in self.profile_to_session:
                sid = self.profile_to_session[lookup_key]
                if sid in self.sessions:
                    sess = self.sessions[sid]
                    sess.touch()
                    return sess

        # 3. Auto-Rehydration from disk
        if auto_rehydrate:
            profile_name = session_id_or_profile
            rehydrate_agent = clean_agent or "default"
            # Check if this identifier has session metadata in agent's vault
            meta = self.profile_vault.load_session_meta(profile_name, agent_name=rehydrate_agent, workspace_dir=workspace_dir)
            if not meta:
                # Search across all profiles for this agent to match session_id
                for p in self.profile_vault.list_profiles(agent_name=rehydrate_agent, workspace_dir=workspace_dir):
                    p_meta = self.profile_vault.load_session_meta(p["name"], agent_name=rehydrate_agent, workspace_dir=workspace_dir)
                    if p_meta and p_meta.get("session_id") == session_id_or_profile:
                        profile_name = p["name"]
                        meta = p_meta
                        break

            if meta or self.profile_vault.has_storage_state(profile_name, agent_name=rehydrate_agent, workspace_dir=workspace_dir):
                sys.stderr.write(f"[CAMOUFOX_MANAGER] Auto-rehydrating session for profile {profile_name} (agent: {rehydrate_agent})...\n")
                last_url = meta.get("last_url") if meta else None
                sess = self.get_or_create_session(
                    profile_name=profile_name,
                    agent_name=rehydrate_agent,
                    workspace_dir=workspace_dir,
                    initial_url=last_url,
                )
                return sess

        return None

    def save_session(
        self,
        session_id_or_profile: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> bool:
        """Explicitly checkpoints session state to disk without closing."""
        sess = self.get_session(session_id_or_profile, agent_name=agent_name, workspace_dir=workspace_dir, auto_rehydrate=False)
        if not sess:
            return False
        with sess.busy_guard():
            sess.sync_to_vault(self.profile_vault)
        return True

    def close_session(
        self,
        session_id: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> bool:
        """
        Closes a session, saves storage state and tabs metadata into the profile vault, and frees resources.
        Enforces agent ownership if agent_name is specified.
        """
        clean_agent = self.profile_vault.clean_agent_name(agent_name) if agent_name else None

        with self._lock:
            if session_id in self.sessions:
                sess = self.sessions[session_id]
                if clean_agent is not None and sess.agent_name != clean_agent:
                    sys.stderr.write(f"[CAMOUFOX_MANAGER] Access Denied: Agent '{clean_agent}' attempted to close session '{session_id}' owned by '{sess.agent_name}'\n")
                    return False
                session = self.sessions.pop(session_id, None)
                if session:
                    lookup_key = f"{session.agent_name}:{session.profile_name}"
                    self.profile_to_session.pop(lookup_key, None)
            else:
                session = None

        if not session:
            return False

        with session.lock:
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

                sys.stderr.write(f"[CAMOUFOX_MANAGER] Closed session {session_id} (agent: {session.agent_name})\n")
                return True
            except Exception as e:
                sys.stderr.write(f"[CAMOUFOX_MANAGER] Error closing session {session_id}: {e}\n")
                return False

    def list_active_sessions(
        self,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> List[Dict[str, Any]]:
        """Lists active in-memory browser sessions, optionally filtered by agent_name and workspace_dir."""
        clean_agent = self.profile_vault.clean_agent_name(agent_name) if agent_name else None
        res = []
        with self._lock:
            for sid, sess in self.sessions.items():
                if clean_agent is not None and sess.agent_name != clean_agent:
                    continue
                if workspace_dir is not None and sess.workspace_dir != workspace_dir:
                    continue
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
                    "agent_name": sess.agent_name,
                    "workspace_dir": sess.workspace_dir,
                    "created_at": sess.created_at,
                    "last_active": sess.last_active,
                    "current_url": curr_url,
                    "current_title": curr_title,
                    "open_tabs": sess.get_tabs_info(),
                })
        return res

    def list_sessions(
        self,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> List[Dict[str, Any]]:
        """Alias for list_active_sessions."""
        return self.list_active_sessions(agent_name=agent_name, workspace_dir=workspace_dir)

    def close_all(self) -> None:
        """Closes all active sessions on shutdown."""
        with self._lock:
            session_ids = list(self.sessions.keys())

        for sid in session_ids:
            self.close_session(sid)


