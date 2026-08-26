import os
import sys
import unittest

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from server import TOOL_DEFINITIONS, handle_message


class TestServerRPC(unittest.TestCase):

    def test_initialize(self):
        msg = {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "initialize",
            "params": {}
        }
        resp = handle_message(msg)
        self.assertIsNotNone(resp)
        self.assertEqual(resp["id"], 1)
        self.assertEqual(resp["result"]["serverInfo"]["name"], "camoufox-browser-plugin")
        self.assertIn("tools", resp["result"]["capabilities"])

    def test_tools_list_contains_all_12_tools(self):
        msg = {
            "jsonrpc": "2.0",
            "id": 2,
            "method": "tools/list",
            "params": {}
        }
        resp = handle_message(msg)
        self.assertIsNotNone(resp)
        tools = resp["result"]["tools"]
        tool_names = [t["name"] for t in tools]

        expected_12 = [
            "camoufox_search",
            "camoufox_discover_trends",
            "camoufox_fetch_page",
            "camoufox_extract_json_ld",
            "camoufox_scrape_selector",
            "camoufox_intercept_api",
            "camoufox_session_start",
            "camoufox_inspect_dom",
            "camoufox_act",
            "camoufox_session_close",
            "camoufox_screenshot",
            "camoufox_pdf_export",
        ]

        self.assertEqual(len(tools), 12)
        for expected in expected_12:
            self.assertIn(expected, tool_names)

    def test_tools_call_unknown(self):
        msg = {
            "jsonrpc": "2.0",
            "id": 3,
            "method": "tools/call",
            "params": {
                "name": "non_existent_tool",
                "arguments": {}
            }
        }
        resp = handle_message(msg)
        self.assertIsNotNone(resp)
        self.assertTrue(resp["result"]["isError"])
        self.assertIn("Tool 'non_existent_tool' not found", resp["result"]["content"][0]["text"])


if __name__ == "__main__":
    unittest.main()
