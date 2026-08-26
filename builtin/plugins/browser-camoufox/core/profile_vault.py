"""
Profile Vault for persistent session storage (cookies, localStorage, auth tokens).
Profiles are stored under ~/.agyent/camoufox/profiles/{profile_name}/
"""

import json
import os
import shutil
from typing import Any, Dict, List, Optional


class ProfileVault:
    """Manages persistent browser profiles and storage states across sessions."""

    def __init__(self, base_dir: Optional[str] = None):
        if base_dir:
            self.base_dir = os.path.abspath(base_dir)
        else:
            home = os.path.expanduser("~")
            self.base_dir = os.path.join(home, ".agyent", "camoufox", "profiles")
        os.makedirs(self.base_dir, exist_ok=True)

    def get_profile_dir(self, profile_name: str) -> str:
        """Returns the absolute directory path for a named profile."""
        clean_name = "".join(c for c in profile_name if c.isalnum() or c in ("-", "_")).strip()
        if not clean_name:
            clean_name = "default"
        profile_path = os.path.join(self.base_dir, clean_name)
        os.makedirs(profile_path, exist_ok=True)
        return profile_path

    def get_storage_state_path(self, profile_name: str) -> str:
        """Returns the path to storage_state.json for the specified profile."""
        profile_dir = self.get_profile_dir(profile_name)
        return os.path.join(profile_dir, "storage_state.json")

    def has_storage_state(self, profile_name: str) -> bool:
        """Checks if a valid storage state exists for the profile."""
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
        """Lists all existing profiles in the vault."""
        profiles = []
        if not os.path.exists(self.base_dir):
            return profiles

        for entry in os.listdir(self.base_dir):
            full_path = os.path.join(self.base_dir, entry)
            if os.path.isdir(full_path):
                has_state = os.path.exists(os.path.join(full_path, "storage_state.json"))
                profiles.append({
                    "name": entry,
                    "path": full_path,
                    "has_storage_state": has_state,
                })
        return profiles

    def delete_profile(self, profile_name: str) -> bool:
        """Deletes a profile directory."""
        profile_dir = self.get_profile_dir(profile_name)
        if os.path.exists(profile_dir):
            shutil.rmtree(profile_dir, ignore_errors=True)
            return True
        return False
