import json
import os
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, patch

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from core.fingerprint import (
    build_camoufox_launch_options,
    build_context_options,
    parse_headless_option,
    resolve_locale_config,
)
from handlers.captcha_handler import handle_solve_captcha
from handlers.interactive_handler import (
    handle_act,
    handle_session_export_state,
    handle_session_import_state,
    handle_session_start,
)


class TestFullCapabilitiesEngine(unittest.TestCase):

    def setUp(self):
        self.tmp_dir = tempfile.TemporaryDirectory()
        self.ws_dir = self.tmp_dir.name

    def tearDown(self):
        self.tmp_dir.cleanup()

    # --- 1. Headless & Headful Option Parsing ---

    def test_parse_headless_option_boolean(self):
        self.assertTrue(parse_headless_option(True))
        self.assertFalse(parse_headless_option(False))

    def test_parse_headless_option_string(self):
        self.assertFalse(parse_headless_option("false"))
        self.assertFalse(parse_headless_option("False"))
        self.assertFalse(parse_headless_option("0"))
        self.assertFalse(parse_headless_option("off"))
        self.assertFalse(parse_headless_option("headful"))
        self.assertFalse(parse_headless_option("gui"))
        self.assertEqual(parse_headless_option("virtual"), "virtual")
        self.assertEqual(parse_headless_option("xvfb"), "virtual")
        self.assertTrue(parse_headless_option("true"))
        self.assertTrue(parse_headless_option("1"))

    def test_parse_headless_option_env_var(self):
        with patch.dict(os.environ, {"CAMOUFOX_HEADLESS": "false"}):
            self.assertFalse(parse_headless_option(None))
        with patch.dict(os.environ, {"CAMOUFOX_HEADLESS": "true"}):
            self.assertTrue(parse_headless_option(None))
        with patch.dict(os.environ, {"CAMOUFOX_HEADLESS": "virtual"}):
            self.assertEqual(parse_headless_option(None), "virtual")
        with patch.dict(os.environ, {}, clear=True):
            self.assertTrue(parse_headless_option(None))

    def test_linux_headful_display_fallback(self):
        with patch("sys.platform", "linux"):
            with patch.dict(os.environ, {}, clear=True):
                res = parse_headless_option(False)
                self.assertEqual(res, "virtual")

            with patch.dict(os.environ, {"DISPLAY": ":0"}):
                res = parse_headless_option(False)
                self.assertFalse(res)

    # --- 2. Launch Options & Fingerprint ---

    def test_build_camoufox_launch_options_headful(self):
        opts = build_camoufox_launch_options(headless=False)
        self.assertFalse(opts["headless"])
        self.assertTrue(opts["disable_coop"])
        self.assertTrue(opts["block_webrtc"])

    def test_build_camoufox_launch_options_proxy_geoip(self):
        proxy = {"server": "http://127.0.0.1:8080"}
        opts = build_camoufox_launch_options(proxy=proxy)
        self.assertEqual(opts["proxy"], proxy)
        self.assertTrue(opts["geoip"])

    def test_build_camoufox_launch_options_os_target(self):
        opts = build_camoufox_launch_options(os_target="windows")
        self.assertEqual(opts["os"], "windows")

    def test_build_camoufox_launch_options_adblock(self):
        opts_on = build_camoufox_launch_options(enable_adblock=True)
        self.assertEqual(opts_on["exclude_addons"], [])
        opts_off = build_camoufox_launch_options(enable_adblock=False)
        self.assertTrue(len(opts_off["exclude_addons"]) > 0)

    # --- 3. Captcha Handler ---

    def test_solve_captcha_missing_session(self):
        res = handle_solve_captcha(session_id="non_existent_session_123")
        self.assertEqual(res["status"], "error")
        self.assertIn("not found", res["error"])

    # --- 4. Storage State Export & Import ---

    def test_export_import_storage_state(self):
        state_data = {
            "cookies": [
                {
                    "name": "session_token",
                    "value": "secret_abc_123",
                    "domain": ".example.com",
                    "path": "/",
                    "expires": -1,
                    "httpOnly": True,
                    "secure": True,
                    "sameSite": "Lax",
                }
            ],
            "origins": [
                {
                    "origin": "https://example.com",
                    "localStorage": [{"name": "theme", "value": "dark"}],
                }
            ],
        }

        mock_session = MagicMock()
        mock_session.profile_name = "test_export_profile"
        mock_session.context.storage_state.return_value = state_data

        with patch("handlers.interactive_handler.BrowserManager.get_instance") as mock_mgr_cls:
            mock_mgr = MagicMock()
            mock_mgr_cls.return_value = mock_mgr
            mock_mgr.get_session.return_value = mock_session

            output_file = os.path.join(self.ws_dir, "exported_state.json")
            res_export = handle_session_export_state(
                session_id="sess_mock_001",
                output_path=output_file,
                workspace_dir=self.ws_dir,
            )

            self.assertEqual(res_export["status"], "ok")
            self.assertEqual(res_export["cookies_count"], 1)
            self.assertTrue(os.path.exists(output_file))
            self.assertLess(os.path.getsize(output_file), 50 * 1024)

            res_import = handle_session_import_state(
                profile_name="test_import_profile",
                state_path=output_file,
                workspace_dir=self.ws_dir,
            )

            self.assertEqual(res_import["status"], "ok")
            self.assertEqual(res_import["cookies_restored"], 1)
            self.assertEqual(res_import["origins_restored"], 1)

    # --- 5. Interactive Act Actions (bring_to_front, wait_for_url, wait) ---

    def test_handle_act_interactive_actions(self):
        mock_session = MagicMock()
        mock_session.session_id = "sess_act_001"
        mock_session.profile_name = "test_profile"
        mock_page = MagicMock()
        mock_session.page = mock_page

        with patch("handlers.interactive_handler.BrowserManager.get_instance") as mock_mgr_cls:
            mock_mgr = MagicMock()
            mock_mgr_cls.return_value = mock_mgr
            mock_mgr.get_session.return_value = mock_session

            # 1. bring_to_front
            res_front = handle_act(session_id="sess_act_001", action="bring_to_front")
            self.assertEqual(res_front["status"], "ok")
            self.assertEqual(res_front["action"], "bring_to_front")
            mock_page.bring_to_front.assert_called_once()

            # 2. wait_for_url
            res_url = handle_act(session_id="sess_act_001", action="wait_for_url", value="https://example.com/dashboard")
            self.assertEqual(res_url["status"], "ok")
            self.assertEqual(res_url["action"], "wait_for_url")
            mock_page.wait_for_url.assert_called_with("https://example.com/dashboard", timeout=30000)

            # 3. wait in seconds
            res_wait = handle_act(session_id="sess_act_001", action="wait", value="5")
            self.assertEqual(res_wait["status"], "ok")
            mock_page.wait_for_timeout.assert_called_with(5000)


if __name__ == "__main__":
    unittest.main()
