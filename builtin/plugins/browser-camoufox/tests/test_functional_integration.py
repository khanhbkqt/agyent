import os
import sys
import unittest

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from handlers.extraction_handler import (
    handle_extract_json_ld,
    handle_fetch_page,
    handle_scrape_selector,
)
from handlers.interactive_handler import (
    handle_act,
    handle_inspect_dom,
    handle_session_close,
    handle_session_start,
)
from handlers.visual_handler import handle_screenshot


class TestFunctionalIntegration(unittest.TestCase):

    def test_fetch_page_stateless(self):
        res = handle_fetch_page("https://example.com", extract_mode="markdown")
        self.assertIn("Example Domain", res["title"])
        self.assertIn("Example Domain", res["content"])
        self.assertGreater(res["word_count"], 0)
        self.assertFalse(res.get("error"))

    def test_extract_json_ld(self):
        res = handle_extract_json_ld("https://example.com")
        self.assertIn("Example Domain", res["title"])
        self.assertIsInstance(res.get("json_ld"), list)
        self.assertIsInstance(res.get("meta"), dict)

    def test_scrape_selector(self):
        res = handle_scrape_selector(
            "https://example.com",
            selector="h1",
            fields={"title": "h1"},
        )
        self.assertEqual(res["count"], 1)
        self.assertEqual(res["items"][0]["title"], "Example Domain")

    def test_stateful_session_lifecycle(self):
        start_res = handle_session_start(
            profile_name="integration_test_profile",
            headless=True,
            initial_url="https://example.com",
        )
        self.assertEqual(start_res["status"], "active")
        session_id = start_res["session_id"]
        self.assertTrue(session_id.startswith("sess_"))

        try:
            # Inspect DOM
            dom_res = handle_inspect_dom(session_id=session_id, mode="a11y_tree")
            self.assertIn("Example Domain", dom_res["page_title"])
            self.assertGreater(dom_res["element_count"], 0)
            self.assertIn("a11y_tree", dom_res)

            # Act (hover or click)
            act_res = handle_act(session_id=session_id, action="hover", target_id=1)
            self.assertEqual(act_res["status"], "ok")

            # Screenshot
            shot_res = handle_screenshot(session_id=session_id)
            self.assertIn("file_path", shot_res)
            self.assertTrue(os.path.exists(shot_res["file_path"]))
            # Clean screenshot file
            try:
                os.remove(shot_res["file_path"])
            except Exception:
                pass

        finally:
            close_res = handle_session_close(session_id=session_id)
            self.assertEqual(close_res["status"], "closed")


if __name__ == "__main__":
    unittest.main()
