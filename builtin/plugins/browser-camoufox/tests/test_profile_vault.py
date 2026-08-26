import os
import shutil
import sys
import tempfile
import unittest

plugin_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if plugin_dir not in sys.path:
    sys.path.insert(0, plugin_dir)

from core.profile_vault import ProfileVault


class TestProfileVault(unittest.TestCase):

    def setUp(self):
        self.temp_dir = tempfile.mkdtemp(prefix="camoufox_vault_test_")
        self.vault = ProfileVault(base_dir=self.temp_dir)

    def tearDown(self):
        shutil.rmtree(self.temp_dir, ignore_errors=True)

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


if __name__ == "__main__":
    unittest.main()
