import os
import sys
import unittest

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from core.fingerprint import (
    build_camoufox_launch_options,
    build_context_options,
    resolve_locale_config,
)


class TestFingerprint(unittest.TestCase):

    def test_resolve_locale_config(self):
        de_cfg = resolve_locale_config("de-DE")
        self.assertEqual(de_cfg["locale"], "de-DE")
        self.assertEqual(de_cfg["timezone"], "Europe/Berlin")

        us_country_cfg = resolve_locale_config("US")
        self.assertEqual(us_country_cfg["locale"], "en-US")
        self.assertEqual(us_country_cfg["timezone"], "America/New_York")

        fallback_cfg = resolve_locale_config("unknown-XYZ")
        self.assertEqual(fallback_cfg["locale"], "en-US")

    def test_build_camoufox_launch_options(self):
        opts = build_camoufox_launch_options(headless=True, locale="de-DE")
        self.assertTrue(opts["headless"])
        self.assertEqual(opts["locale"], "de-DE")
        self.assertTrue(opts["enable_cache"])

    def test_build_context_options(self):
        ctx_opts = build_context_options(locale="de-DE")
        self.assertEqual(ctx_opts["timezone_id"], "Europe/Berlin")
        self.assertIn("permissions", ctx_opts)


if __name__ == "__main__":
    unittest.main()
