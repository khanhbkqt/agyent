import os
import shutil
import sys
import tempfile
import unittest

import importlib.util

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from core.profile_vault import ProfileVault

sqlite_server_path = os.path.abspath(os.path.join(plugin_dir, "..", "database-sqlite", "server.py"))
spec = importlib.util.spec_from_file_location("sqlite_plugin_server", sqlite_server_path)
sqlite_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sqlite_module)

sqlite_is_safe_path = sqlite_module.is_safe_path
query_sqlite = sqlite_module.query_sqlite


class TestProfileVault(unittest.TestCase):

    def setUp(self):
        self.temp_dir = tempfile.mkdtemp(prefix="camoufox_vault_test_")
        self.vault = ProfileVault(base_dir=self.temp_dir)

    def tearDown(self):
        shutil.rmtree(self.temp_dir, ignore_errors=True)

    def test_name_sanitization_traversal_defense(self):
        pv = ProfileVault()
        # Test agent name traversal defense
        self.assertEqual(pv.clean_agent_name("../../evil_agent"), "evil_agent")
        self.assertEqual(pv.clean_agent_name("..\\..\\evil_agent"), "evil_agent")
        self.assertEqual(pv.clean_agent_name("normal_agent-1"), "normal_agent-1")
        self.assertEqual(pv.clean_agent_name(""), "default")
        self.assertEqual(pv.clean_agent_name(None), "default")

        # Test profile name traversal defense
        self.assertEqual(pv.clean_profile_name("../../evil_profile"), "evil_profile")
        self.assertEqual(pv.clean_profile_name("..\\..\\evil_profile"), "evil_profile")
        self.assertEqual(pv.clean_profile_name("profile_123"), "profile_123")
        self.assertEqual(pv.clean_profile_name(""), "default")
        self.assertEqual(pv.clean_profile_name(None), "default")

    def test_agent_namespace_directory_resolution(self):
        # Global storage resolution without workspace
        pv = ProfileVault()
        dir_alice = pv.get_profile_dir("default", agent_name="alice_agent")
        dir_bob = pv.get_profile_dir("default", agent_name="bob_agent")

        self.assertIn("alice_agent", dir_alice)
        self.assertIn("bob_agent", dir_bob)
        self.assertNotEqual(dir_alice, dir_bob)

        # Workspace-scoped profile resolution
        ws_dir = os.path.join(self.temp_dir, "ws_project_1")
        os.makedirs(ws_dir, exist_ok=True)
        dir_ws = pv.get_profile_dir("default", agent_name="alice_agent", workspace_dir=ws_dir)
        expected_ws_prefix = os.path.join(ws_dir, ".plugins", "camoufox", "profiles")
        self.assertTrue(dir_ws.startswith(expected_ws_prefix))

    def test_save_and_load_storage_state(self):
        profile = "test_user_de"
        self.assertFalse(self.vault.has_storage_state(profile))

        sample_state = {
            "cookies": [{"name": "session_token", "value": "abc123xyz"}],
            "origins": [{"origin": "https://example.com", "localStorage": [{"name": "theme", "value": "dark"}]}],
        }

        saved_path = self.vault.save_storage_state(profile, sample_state)
        self.assertTrue(os.path.exists(saved_path))
        self.assertTrue(self.vault.has_storage_state(profile))

        loaded = self.vault.load_storage_state(profile)
        self.assertIsNotNone(loaded)
        self.assertEqual(loaded["cookies"][0]["value"], "abc123xyz")

    def test_list_and_delete_profiles(self):
        self.vault.save_storage_state("prof_a", {"cookies": []})
        self.vault.save_storage_state("prof_b", {"cookies": []})

        profiles = self.vault.list_profiles()
        names = [p["name"] for p in profiles]
        self.assertIn("prof_a", names)
        self.assertIn("prof_b", names)

        self.vault.delete_profile("prof_a")
        profiles_after = self.vault.list_profiles()
        names_after = [p["name"] for p in profiles_after]
        self.assertNotIn("prof_a", names_after)
        self.assertIn("prof_b", names_after)

    def test_sqlite_pathjail_security_guard(self):
        ws_root = os.path.join(self.temp_dir, "workspace_jail")
        os.makedirs(ws_root, exist_ok=True)
        safe_db = os.path.join(ws_root, "app_data.db")
        outside_db = os.path.join(self.temp_dir, "system_secrets.db")

        # 1. Check is_safe_path
        self.assertTrue(sqlite_is_safe_path(safe_db, workspace_root=ws_root))
        self.assertFalse(sqlite_is_safe_path(outside_db, workspace_root=ws_root))
        self.assertFalse(sqlite_is_safe_path("../../outside.db", workspace_root=ws_root))
        self.assertFalse(sqlite_is_safe_path("", workspace_root=ws_root))

        # Relative paths inside workspace root
        self.assertTrue(sqlite_is_safe_path("app_data.db", workspace_root=ws_root))
        self.assertTrue(sqlite_is_safe_path("./subdir/app_data.db", workspace_root=ws_root))
        self.assertFalse(sqlite_is_safe_path("../outside.db", workspace_root=ws_root))

        # 2. Check query_sqlite execution guard on out-of-jail path
        res = query_sqlite(outside_db, "SELECT 1;", workspace_root=ws_root)
        self.assertIn("error", res)
        self.assertIn("Security Violation", res["error"])
        self.assertIn("outside the authorized workspace jail", res["error"])

    def test_sqlite_relative_path_and_query_execution(self):
        import sqlite3 as py_sqlite
        ws_root = os.path.join(self.temp_dir, "workspace_jail_query")
        os.makedirs(ws_root, exist_ok=True)
        db_file = os.path.join(ws_root, "users.db")

        # Populate SQLite database
        conn = py_sqlite.connect(db_file)
        conn.execute("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT);")
        conn.execute("INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com'), ('Bob', 'bob@example.com');")
        conn.commit()
        conn.close()

        # Query with relative path
        res_rel = query_sqlite("users.db", "SELECT name, email FROM users ORDER BY id ASC;", workspace_root=ws_root)
        self.assertNotIn("error", res_rel)
        self.assertEqual(res_rel["columns"], ["name", "email"])
        self.assertEqual(len(res_rel["rows"]), 2)
        self.assertEqual(res_rel["rows"][0][0], "Alice")
        self.assertEqual(res_rel["rows"][1][0], "Bob")

        # Query with absolute path
        res_abs = query_sqlite(db_file, "SELECT COUNT(*) FROM users;", workspace_root=ws_root)
        self.assertNotIn("error", res_abs)
        self.assertEqual(res_abs["rows"][0][0], 2)

    def test_is_profile_locked_detection(self):
        profile = "lock_test_profile"
        self.assertFalse(self.vault.is_profile_locked(profile))

        # Create lock file
        user_data = self.vault.get_user_data_dir(profile)
        lock_file = os.path.join(user_data, "parent.lock")
        with open(lock_file, "w") as f:
            f.write("test_lock")

        # On Windows/POSIX when unheld, is_profile_locked should be False
        self.assertFalse(self.vault.is_profile_locked(profile))


if __name__ == "__main__":
    unittest.main()
