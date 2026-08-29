"""
Automated Test Suite for Camoufox Continuous Session & Daemon Architecture.
Verifies multi-turn persistence, native Firefox user_data_dir state retention,
daemon JSON-RPC execution, multi-tab switching, and auto-rehydration.
"""

import http.server
import json
import os
import shutil
import sys
import tempfile
import threading
import time
import unittest
import urllib.request

# Add plugin root to sys.path
current_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if current_dir not in sys.path:
    sys.path.insert(0, current_dir)

from core.browser_manager import BrowserManager, TabSession
from core.profile_vault import ProfileVault
from daemon import DaemonHTTPHandler, execute_tool
from handlers.interactive_handler import (
    handle_act,
    handle_inspect_dom,
    handle_session_close,
    handle_session_list,
    handle_session_save,
    handle_session_start,
)


class TestProfileVault(unittest.TestCase):
    """Tests ProfileVault data_dir, locking, and metadata persistence."""

    def setUp(self):
        self.test_dir = tempfile.mkdtemp(prefix="test_vault_")
        self.vault = ProfileVault(base_dir=self.test_dir)

    def tearDown(self):
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def test_profile_paths_and_sanitization(self):
        clean = self.vault.clean_profile_name("../../../malicious-profile!@#")
        self.assertTrue(".." not in clean)
        self.assertTrue("/" not in clean)
        self.assertTrue("malicious-profile" in clean)

        data_dir = self.vault.get_user_data_dir("my_profile")
        self.assertTrue(os.path.exists(data_dir))
        self.assertTrue(data_dir.endswith("data_dir"))

    def test_session_meta_lifecycle(self):
        meta = {
            "session_id": "sess_12345",
            "profile_name": "test_prof",
            "last_url": "https://example.com",
            "page_title": "Example Domain",
            "tabs": [{"index": 0, "title": "Example Domain", "url": "https://example.com"}],
        }
        self.vault.save_session_meta("test_prof", meta)

        loaded = self.vault.load_session_meta("test_prof")
        self.assertIsNotNone(loaded)
        self.assertEqual(loaded["session_id"], "sess_12345")
        self.assertEqual(loaded["last_url"], "https://example.com")
        self.assertTrue("updated_at" in loaded)

    def test_list_profiles(self):
        self.vault.save_session_meta("prof_a", {"last_url": "https://a.com"})
        self.vault.save_session_meta("prof_b", {"last_url": "https://b.com"})

        profiles = self.vault.list_profiles()
        names = [p["name"] for p in profiles]
        self.assertIn("prof_a", names)
        self.assertIn("prof_b", names)


class TestContinuousBrowserSession(unittest.TestCase):
    """Tests BrowserManager state persistence, tab management, and rehydration."""

    @classmethod
    def setUpClass(cls):
        cls.test_base_dir = tempfile.mkdtemp(prefix="test_bm_vault_")
        cls.vault = ProfileVault(base_dir=cls.test_base_dir)
        cls.mgr = BrowserManager(profile_vault=cls.vault)
        BrowserManager._instance = cls.mgr

    @classmethod
    def tearDownClass(cls):
        try:
            cls.mgr.close_all()
        except Exception:
            pass
        BrowserManager._instance = None
        shutil.rmtree(cls.test_base_dir, ignore_errors=True)

    def test_persistent_session_lifecycle(self):
        # 1. Start persistent session
        profile_name = "test_persistence_flow"
        res_start = handle_session_start(profile_name=profile_name, headless=True, initial_url="https://example.com")
        self.assertEqual(res_start["status"], "active")
        session_id = res_start["session_id"]
        self.assertTrue(len(session_id) > 0)

        # 2. Inspect DOM
        res_dom = handle_inspect_dom(session_id=session_id)
        self.assertIn("Example Domain", res_dom.get("page_title", ""))
        self.assertTrue(len(res_dom.get("elements", [])) > 0)

        # 3. Check active session list
        res_list = handle_session_list()
        self.assertTrue(res_list["active_sessions_count"] >= 1)

        # 4. Multi-Tab actions: Open new tab and switch
        res_new_tab = handle_act(session_id=session_id, action="new_tab", value="https://example.com")
        self.assertEqual(res_new_tab["status"], "ok")
        self.assertEqual(res_new_tab["open_tabs_count"], 2)

        # Switch back to tab 0
        res_switch = handle_act(session_id=session_id, action="switch_tab", value="0")
        self.assertEqual(res_switch["status"], "ok")
        self.assertEqual(res_switch["active_tab_index"], 0)

        # 5. Checkpoint session
        res_save = handle_session_save(session_id=session_id)
        self.assertEqual(res_save["status"], "saved")

        # 6. Verify meta on disk
        meta = self.vault.load_session_meta(profile_name)
        self.assertIsNotNone(meta)
        self.assertEqual(meta["profile_name"], profile_name)

        # 7. Close session
        res_close = handle_session_close(session_id=session_id)
        self.assertEqual(res_close["status"], "closed")

    def test_auto_rehydration(self):
        profile_name = "test_rehydration"
        # Start and navigate
        res_start = handle_session_start(profile_name=profile_name, headless=True, initial_url="https://example.com")
        sid = res_start["session_id"]

        # Close session to clear in-memory references
        handle_session_close(session_id=sid)

        # Calling inspect_dom with profile_name should auto-rehydrate!
        res_rehydrate = handle_inspect_dom(profile_name=profile_name)
        self.assertNotIn("error", res_rehydrate)
        self.assertIn("Example Domain", res_rehydrate.get("page_title", ""))

        # Clean up
        if "session_id" in res_rehydrate:
            handle_session_close(session_id=res_rehydrate["session_id"])


class TestDaemonHTTPRPC(unittest.TestCase):
    """Tests the background daemon HTTP JSON-RPC endpoint."""

    @classmethod
    def setUpClass(cls):
        cls.server = http.server.HTTPServer(("127.0.0.1", 0), DaemonHTTPHandler)
        cls.port = cls.server.server_address[1]
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        time.sleep(0.3)

    @classmethod
    def tearDownClass(cls):
        try:
            cls.server.shutdown()
        except Exception:
            pass

    def test_health_check_endpoint(self):
        url = f"http://127.0.0.1:{self.port}/health"
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req, timeout=3.0) as resp:
            self.assertEqual(resp.status, 200)
            data = json.loads(resp.read().decode("utf-8"))
            self.assertEqual(data["status"], "ok")
            self.assertEqual(data["service"], "camoufox-daemon")

    def test_rpc_search_execution(self):
        url = f"http://127.0.0.1:{self.port}/rpc"
        payload = json.dumps({
            "name": "camoufox_search",
            "args": {"query": "python playwright", "max_results": 2}
        }).encode("utf-8")

        req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"}, method="POST")
        with urllib.request.urlopen(req, timeout=30.0) as resp:
            self.assertEqual(resp.status, 200)
            data = json.loads(resp.read().decode("utf-8"))
            self.assertIsNone(data["error"])
            res = data["result"]
            self.assertIn("results", res)
            self.assertTrue(len(res["results"]) > 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
