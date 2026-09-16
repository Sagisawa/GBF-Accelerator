import unittest
from unittest.mock import patch, MagicMock
import update_manager

class TestUpdateManager(unittest.TestCase):
    def test_parse_version(self):
        self.assertEqual(update_manager.parse_version("v1.4.0"), (1, 4, 0))
        self.assertEqual(update_manager.parse_version("1.4.1"), (1, 4, 1))
        self.assertEqual(update_manager.parse_version("v2.0.0-rc1"), (2, 0, 0))
        self.assertEqual(update_manager.parse_version("v1.10.5"), (1, 10, 5))
        self.assertEqual(update_manager.parse_version(""), (0, 0, 0))

    def test_is_newer_version(self):
        self.assertTrue(update_manager.is_newer_version("v1.4.1", "v1.4.0"))
        self.assertTrue(update_manager.is_newer_version("1.5.0", "1.4.0"))
        self.assertTrue(update_manager.is_newer_version("2.0.0", "1.9.9"))
        self.assertFalse(update_manager.is_newer_version("v1.4.0", "v1.4.0"))
        self.assertFalse(update_manager.is_newer_version("1.3.9", "1.4.0"))
        self.assertFalse(update_manager.is_newer_version("v1.2.0", "1.4.0"))

    def test_check_for_updates_same_version(self):
        info = update_manager.check_for_updates()
        if not info.error:
            same_info = update_manager.check_for_updates(current_ver=info.latest_version)
            self.assertFalse(same_info.has_update)
            self.assertEqual(same_info.latest_version, info.latest_version)
            self.assertEqual(same_info.current_version, info.latest_version)

    def test_check_for_updates_older_version_detected(self):
        # If simulated current version is 1.0.0, remote latest version should be detected as an update
        info = update_manager.check_for_updates(current_ver="1.0.0")
        if not info.error:
            self.assertTrue(info.has_update)
            self.assertTrue(bool(info.latest_version))
            self.assertEqual(info.current_version, "1.0.0")
            self.assertTrue(bool(info.html_url))

    def test_network_failure_graceful_handling(self):
        with patch("httpx.Client") as mock_client:
            mock_client.return_value.__enter__.side_effect = Exception("Simulated network outage")
            info = update_manager.check_for_updates()
            self.assertFalse(info.has_update)
            self.assertIsNotNone(info.error)
    def test_get_default_download_dir(self):
        d = update_manager.get_default_download_dir()
        self.assertTrue(d.exists())

    def test_get_asset_filename(self):
        self.assertEqual(
            update_manager.get_asset_filename("https://github.com/Sagisawa/GBF-Accelerator/releases/download/v1.7.0/GBF_Accelerator_v1.7.0_GUI.zip"),
            "GBF_Accelerator_v1.7.0_GUI.zip"
        )
        self.assertEqual(
            update_manager.get_asset_filename("", fallback_version="1.7.0"),
            "GBF_Accelerator_v1.7.0_GUI.zip"
        )

    def test_download_empty_url(self):
        from pathlib import Path
        ok, msg, path = update_manager.download_release_asset("", Path("dummy.zip"))
        self.assertFalse(ok)
        self.assertIn("为空", msg)

if __name__ == "__main__":
    unittest.main()
