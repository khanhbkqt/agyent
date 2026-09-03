"""
Profile Vault for persistent session storage (cookies, localStorage, IndexedDB, auth tokens).
Profiles are stored under ~/.agyent/camoufox/agents/{agent_name}/profiles/{profile_name}/
or in workspace <workspace>/.plugins/camoufox/profiles/{profile_name}/.
"""

import json
import os
import shutil
import sys
import time
from typing import Any, Dict, List, Optional


class ProfileVault:
    """Manages persistent browser profiles, native user_data_dir, and storage states across multi-tenant sessions."""

    def __init__(
        self,
        base_dir: Optional[str] = None,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ):
        self.custom_base_dir = bool(base_dir)
        self.agent_name = agent_name or os.environ.get("AGYENT_AGENT_NAME", "default")
        self.workspace_dir = workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
        if base_dir:
            self.base_dir = os.path.abspath(base_dir)
        else:
            self.base_dir = self._resolve_base_dir(self.agent_name, self.workspace_dir)
        os.makedirs(self.base_dir, exist_ok=True)

    def clean_agent_name(self, agent_name: Optional[str] = None) -> str:
        """Sanitizes agent name to prevent path traversal."""
        if not agent_name:
            return "default"
        base = os.path.basename(str(agent_name).strip())
        clean = "".join(c for c in base if c.isalnum() or c in ("-", "_")).strip()
        return clean if clean else "default"

    def clean_profile_name(self, profile_name: Optional[str] = None) -> str:
        """Sanitizes profile name to prevent path traversal."""
        if not profile_name:
            return "default"
        base = os.path.basename(str(profile_name).strip())
        clean = "".join(c for c in base if c.isalnum() or c in ("-", "_")).strip()
        return clean if clean else "default"

    def _resolve_base_dir(self, agent_name: Optional[str] = None, workspace_dir: Optional[str] = None) -> str:
        """Resolves profile root directory based on workspace or global agent namespace."""
        ws = workspace_dir or self.workspace_dir or os.environ.get("AGYENT_AGENT_WORKSPACE")
        if ws and os.path.exists(ws):
            return os.path.join(os.path.abspath(ws), ".plugins", "camoufox", "profiles")

        clean_agent = self.clean_agent_name(agent_name or self.agent_name or os.environ.get("AGYENT_AGENT_NAME", "default"))
        home = os.path.expanduser("~")
        return os.path.join(home, ".agyent", "camoufox", "agents", clean_agent, "profiles")

    def get_profile_dir(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> str:
        """Returns the absolute directory path for a named profile."""
        clean_name = self.clean_profile_name(profile_name)
        if workspace_dir:
            base = self._resolve_base_dir(agent_name, workspace_dir)
            profile_path = os.path.join(base, clean_name)
        elif self.custom_base_dir:
            profile_path = os.path.join(self.base_dir, clean_name)
        else:
            base = self._resolve_base_dir(agent_name, workspace_dir)
            profile_path = os.path.join(base, clean_name)
        os.makedirs(profile_path, exist_ok=True)
        return profile_path

    def get_user_data_dir(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> str:
        """Returns the path to Firefox native user_data_dir for persistent context."""
        profile_dir = self.get_profile_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        data_dir = os.path.join(profile_dir, "data_dir")
        os.makedirs(data_dir, exist_ok=True)
        return data_dir

    def is_profile_locked(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> bool:
        """
        Checks if the Firefox profile is currently locked by another active process.
        Firefox creates parent.lock (Windows/Linux) or .parentlock in user_data_dir.
        """
        data_dir = self.get_user_data_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        lock_files = [
            os.path.join(data_dir, "parent.lock"),
            os.path.join(data_dir, ".parentlock"),
            os.path.join(data_dir, "lock"),
        ]
        for lock_file in lock_files:
            if os.path.exists(lock_file):
                if sys.platform != "win32":
                    try:
                        import fcntl
                        with open(lock_file, "r+") as f:
                            try:
                                fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                                fcntl.flock(f.fileno(), fcntl.LOCK_UN)
                            except (IOError, BlockingIOError, PermissionError, OSError):
                                return True
                    except (IOError, BlockingIOError, PermissionError, OSError):
                        return True
                else:
                    # On Windows, try opening in append mode to check if it's locked by another process
                    try:
                        with open(lock_file, "a"):
                            pass
                    except (IOError, OSError, PermissionError):
                        return True
        return False

    def clean_stale_locks(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> bool:
        """
        Detects and removes orphan/stale Firefox lock files if no active process holds the file lock.
        Returns True if any stale lock was cleaned.
        """
        data_dir = self.get_user_data_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        lock_files = [
            os.path.join(data_dir, "parent.lock"),
            os.path.join(data_dir, ".parentlock"),
            os.path.join(data_dir, "lock"),
        ]
        cleaned = False
        for lock_file in lock_files:
            if not os.path.exists(lock_file):
                continue
            if sys.platform != "win32":
                try:
                    import fcntl
                    with open(lock_file, "r+") as f:
                        try:
                            # Try non-blocking exclusive flock
                            fcntl.flock(f.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                            fcntl.flock(f.fileno(), fcntl.LOCK_UN)
                            # If we could acquire flock, no active process holds it -> safe to delete!
                            try:
                                os.remove(lock_file)
                                cleaned = True
                            except OSError:
                                pass
                        except (IOError, BlockingIOError, PermissionError, OSError):
                            # Actively locked by running process
                            pass
                except Exception:
                    pass
            else:
                try:
                    # On Windows, try opening in append mode
                    with open(lock_file, "a"):
                        pass
                    # If open succeeds, file is not locked by another process
                    try:
                        os.remove(lock_file)
                        cleaned = True
                    except OSError:
                        pass
                except (IOError, OSError, PermissionError):
                    pass
        return cleaned

    def get_session_meta_path(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> str:
        """Returns the path to session_meta.json for tracking live/last known tab state."""
        profile_dir = self.get_profile_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        return os.path.join(profile_dir, "session_meta.json")

    def save_session_meta(
        self,
        profile_name: str,
        meta_data: Dict[str, Any],
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> str:
        """Persists session metadata (last URL, tab list, timestamps) to disk."""
        meta_path = self.get_session_meta_path(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        data = dict(meta_data)
        data["updated_at"] = time.time()
        with open(meta_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
        return meta_path

    def load_session_meta(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> Optional[Dict[str, Any]]:
        """Loads session metadata from session_meta.json if present."""
        meta_path = self.get_session_meta_path(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        if not os.path.exists(meta_path):
            return None
        try:
            with open(meta_path, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            return None

    def get_storage_state_path(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> str:
        """Returns the path to storage_state.json for the specified profile."""
        profile_dir = self.get_profile_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        return os.path.join(profile_dir, "storage_state.json")

    def has_storage_state(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> bool:
        """Checks if a valid storage state or native user_data_dir exists for the profile."""
        data_dir = os.path.join(self.get_profile_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir), "data_dir")
        if os.path.exists(data_dir):
            try:
                with os.scandir(data_dir) as it:
                    if any(it):
                        return True
            except Exception:
                pass
        state_path = self.get_storage_state_path(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        return os.path.exists(state_path) and os.path.getsize(state_path) > 0

    def load_storage_state(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> Optional[Dict[str, Any]]:
        """Loads and parses storage state dictionary if it exists."""
        state_path = self.get_storage_state_path(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        if not os.path.exists(state_path):
            return None
        try:
            with open(state_path, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            return None

    def save_storage_state(
        self,
        profile_name: str,
        state_data: Dict[str, Any],
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> str:
        """Saves storage state dictionary to file."""
        state_path = self.get_storage_state_path(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        with open(state_path, "w", encoding="utf-8") as f:
            json.dump(state_data, f, indent=2, ensure_ascii=False)
        return state_path

    def list_profiles(
        self,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> List[Dict[str, Any]]:
        """Lists all existing profiles in the vault with metadata and lock status."""
        profiles = []
        target_dir = self.base_dir if self.custom_base_dir else self._resolve_base_dir(agent_name, workspace_dir)
        if not os.path.exists(target_dir):
            return profiles

        for entry in os.listdir(target_dir):
            full_path = os.path.join(target_dir, entry)
            if os.path.isdir(full_path):
                has_state = self.has_storage_state(entry, agent_name=agent_name, workspace_dir=workspace_dir)
                is_locked = self.is_profile_locked(entry, agent_name=agent_name, workspace_dir=workspace_dir)
                meta = self.load_session_meta(entry, agent_name=agent_name, workspace_dir=workspace_dir) or {}
                profiles.append({
                    "name": entry,
                    "path": full_path,
                    "has_storage_state": has_state,
                    "is_locked": is_locked,
                    "last_url": meta.get("last_url", ""),
                    "last_active": meta.get("updated_at", 0),
                    "open_tabs_count": len(meta.get("tabs", [])),
                })
        return profiles

    def delete_profile(
        self,
        profile_name: str,
        agent_name: Optional[str] = None,
        workspace_dir: Optional[str] = None,
    ) -> bool:
        """Deletes a profile directory if not locked."""
        if self.is_profile_locked(profile_name, agent_name=agent_name, workspace_dir=workspace_dir):
            return False
        profile_dir = self.get_profile_dir(profile_name, agent_name=agent_name, workspace_dir=workspace_dir)
        if os.path.exists(profile_dir):
            shutil.rmtree(profile_dir, ignore_errors=True)
            return True
        return False
