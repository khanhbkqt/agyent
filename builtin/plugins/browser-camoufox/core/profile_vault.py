"""
Profile Vault for persistent session storage (cookies, localStorage, IndexedDB, auth tokens).
Profiles are stored under ~/.agyent/camoufox/profiles/{profile_name}/
"""

import json
import os
import shutil
import time
from typing import Any, Dict, List, Optional


class ProfileVault:
    """Manages persistent browser profiles, native user_data_dir, and storage states across sessions."""

    def __init__(self, base_dir: Optional[str] = None):
        if base_dir:
            self.base_dir = os.path.abspath(base_dir)
        else:
            home = os.path.expanduser("~")
            self.base_dir = os.path.join(home, ".agyent", "camoufox", "profiles")
        os.makedirs(self.base_dir, exist_ok=True)

    def clean_profile_name(self, profile_name: str) -> str:
        """Sanitizes profile name to prevent path traversal."""
        clean = "".join(c for c in profile_name if c.isalnum() or c in ("-", "_")).strip()
        return clean if clean else "default"

    def get_profile_dir(self, profile_name: str) -> str:
        """Returns the absolute directory path for a named profile."""
        clean_name = self.clean_profile_name(profile_name)
        profile_path = os.path.join(self.base_dir, clean_name)
        os.makedirs(profile_path, exist_ok=True)
        return profile_path

    def get_user_data_dir(self, profile_name: str) -> str:
        """Returns the path to Firefox native user_data_dir for persistent context."""
        profile_dir = self.get_profile_dir(profile_name)
        data_dir = os.path.join(profile_dir, "data_dir")
        os.makedirs(data_dir, exist_ok=True)
        return data_dir

    def is_profile_locked(self, profile_name: str) -> bool:
        """
        Checks if the Firefox profile is currently locked by another active process.
        Firefox creates parent.lock (Windows/Linux) or .parentlock in user_data_dir.
        """
        data_dir = self.get_user_data_dir(profile_name)
        lock_files = [
            os.path.join(data_dir, "parent.lock"),
            os.path.join(data_dir, ".parentlock"),
            os.path.join(data_dir, "lock"),
        ]
        for lock_file in lock_files:
            if os.path.exists(lock_file):
                # On Windows, try opening in append mode to check if it's locked by another process
                try:
                    with open(lock_file, "a"):
                        pass
                except (IOError, OSError, PermissionError):
                    return True
        return False

    def get_session_meta_path(self, profile_name: str) -> str:
        """Returns the path to session_meta.json for tracking live/last known tab state."""
        profile_dir = self.get_profile_dir(profile_name)
        return os.path.join(profile_dir, "session_meta.json")

    def save_session_meta(self, profile_name: str, meta_data: Dict[str, Any]) -> str:
        """Persists session metadata (last URL, tab list, timestamps) to disk."""
        meta_path = self.get_session_meta_path(profile_name)
        data = dict(meta_data)
        data["updated_at"] = time.time()
        with open(meta_path, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=2, ensure_ascii=False)
        return meta_path

    def load_session_meta(self, profile_name: str) -> Optional[Dict[str, Any]]:
        """Loads session metadata from session_meta.json if present."""
        meta_path = self.get_session_meta_path(profile_name)
        if not os.path.exists(meta_path):
            return None
        try:
            with open(meta_path, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            return None

    def get_storage_state_path(self, profile_name: str) -> str:
        """Returns the path to storage_state.json for the specified profile."""
        profile_dir = self.get_profile_dir(profile_name)
        return os.path.join(profile_dir, "storage_state.json")

    def has_storage_state(self, profile_name: str) -> bool:
        """Checks if a valid storage state or native user_data_dir exists for the profile."""
        data_dir = os.path.join(self.get_profile_dir(profile_name), "data_dir")
        if os.path.exists(data_dir):
            try:
                with os.scandir(data_dir) as it:
                    if any(it):
                        return True
            except Exception:
                pass
        state_path = self.get_storage_state_path(profile_name)
        return os.path.exists(state_path) and os.path.getsize(state_path) > 0

    def load_storage_state(self, profile_name: str) -> Optional[Dict[str, Any]]:
        """Loads and parses storage state dictionary if it exists."""
        state_path = self.get_storage_state_path(profile_name)
        if not os.path.exists(state_path):
            return None
        try:
            with open(state_path, "r", encoding="utf-8") as f:
                return json.load(f)
        except Exception:
            return None

    def save_storage_state(self, profile_name: str, state_data: Dict[str, Any]) -> str:
        """Saves storage state dictionary to file."""
        state_path = self.get_storage_state_path(profile_name)
        with open(state_path, "w", encoding="utf-8") as f:
            json.dump(state_data, f, indent=2, ensure_ascii=False)
        return state_path

    def list_profiles(self) -> List[Dict[str, Any]]:
        """Lists all existing profiles in the vault with metadata and lock status."""
        profiles = []
        if not os.path.exists(self.base_dir):
            return profiles

        for entry in os.listdir(self.base_dir):
            full_path = os.path.join(self.base_dir, entry)
            if os.path.isdir(full_path):
                has_state = self.has_storage_state(entry)
                is_locked = self.is_profile_locked(entry)
                meta = self.load_session_meta(entry) or {}
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

    def delete_profile(self, profile_name: str) -> bool:
        """Deletes a profile directory if not locked."""
        if self.is_profile_locked(profile_name):
            return False
        profile_dir = self.get_profile_dir(profile_name)
        if os.path.exists(profile_dir):
            shutil.rmtree(profile_dir, ignore_errors=True)
            return True
        return False
