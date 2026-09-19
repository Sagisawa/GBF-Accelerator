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
            update_manager.get_asset_filename("https://github.com/Sagisawa/GBF-Accelerator/releases/download/v1.7.3/GBF_Accelerator_v1.7.3_macOS_universal2.zip"),
            "GBF_Accelerator_v1.7.3_macOS_universal2.zip"
        )
        with patch("sys.platform", "win32"):
            self.assertEqual(
                update_manager.get_asset_filename("", fallback_version="1.7.0"),
                "GBF_Accelerator_v1.7.0_GUI.zip"
            )
            self.assertEqual(
                update_manager.get_asset_filename(""),
                "GBF_Accelerator_latest_GUI.zip"
            )
        with patch("sys.platform", "darwin"):
            self.assertEqual(
                update_manager.get_asset_filename("", fallback_version="1.7.3"),
                "GBF_Accelerator_v1.7.3_macOS_universal2.zip"
            )
            self.assertEqual(
                update_manager.get_asset_filename(""),
                "GBF_Accelerator_latest_macOS_universal2.zip"
            )

    def test_download_empty_url(self):
        from pathlib import Path
        ok, msg, path = update_manager.download_release_asset("", Path("dummy.zip"))
        self.assertFalse(ok)
        self.assertIn("为空", msg)

    def test_platform_aware_asset_filtering(self):
        mock_release = {
            "tag_name": "v1.8.0",
            "name": "v1.8.0 - Release",
            "body": "notes",
            "html_url": "https://github.com/Sagisawa/GBF-Accelerator/releases/tag/v1.8.0",
            "published_at": "2026-09-18T00:00:00Z",
            "assets": [
                {
                    "name": "GBF_Accelerator_v1.8.0_mac_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.8.0_mac_GUI.zip",
                },
                {
                    "name": "GBF_Accelerator_v1.8.0_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.8.0_GUI.zip",
                },
            ],
        }
        with patch("httpx.Client") as mock_client:
            mock_resp = MagicMock()
            mock_resp.status_code = 200
            mock_resp.json.return_value = mock_release
            mock_client.return_value.__enter__.return_value.get.return_value = mock_resp

            # Case 1: macOS platform matches macOS-labeled package
            with patch("sys.platform", "darwin"):
                info_mac = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertTrue(info_mac.has_update)
                self.assertEqual(info_mac.download_url, "https://example.com/GBF_Accelerator_v1.8.0_mac_GUI.zip")

            # Case 2: Windows platform matches Windows-labeled package and excludes macOS package
            with patch("sys.platform", "win32"):
                info_win = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertTrue(info_win.has_update)
                self.assertEqual(info_win.download_url, "https://example.com/GBF_Accelerator_v1.8.0_GUI.zip")

            # Case 3: On Windows with only macOS package available -> download_url is None
            mock_release_mac_only = dict(mock_release, assets=[mock_release["assets"][0]])
            mock_resp.json.return_value = mock_release_mac_only
            with patch("sys.platform", "win32"):
                info_win_none = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertIsNone(info_win_none.download_url)

            # Case 4: On macOS with only Windows package available -> download_url is None
            mock_release_win_only = dict(mock_release, assets=[mock_release["assets"][1]])
            mock_resp.json.return_value = mock_release_win_only
            with patch("sys.platform", "darwin"):
                info_mac_none = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertIsNone(info_mac_none.download_url)

    def test_macos_universal2_asset_filtering(self):
        mock_release = {
            "tag_name": "v1.7.3",
            "name": "v1.7.3 - macOS Universal 2 Release",
            "body": "Universal 2 build notes",
            "html_url": "https://github.com/Sagisawa/GBF-Accelerator/releases/tag/v1.7.3",
            "published_at": "2026-09-18T00:00:00Z",
            "assets": [
                {
                    "name": "GBF_Accelerator_v1.7.3_macOS_universal2.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.7.3_macOS_universal2.zip",
                },
                {
                    "name": "GBF_Accelerator_v1.7.3_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.7.3_GUI.zip",
                },
            ],
        }
        with patch("httpx.Client") as mock_client:
            mock_resp = MagicMock()
            mock_resp.status_code = 200
            mock_resp.json.return_value = mock_release
            mock_client.return_value.__enter__.return_value.get.return_value = mock_resp

            # Case 1: macOS platform matches GBF_Accelerator_v1.7.3_macOS_universal2.zip
            with patch("sys.platform", "darwin"):
                info_mac = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertTrue(info_mac.has_update)
                self.assertEqual(info_mac.latest_version, "1.7.3")
                self.assertEqual(info_mac.download_url, "https://example.com/GBF_Accelerator_v1.7.3_macOS_universal2.zip")

            # Case 2: Windows platform ignores macOS_universal2 and picks Windows GUI package
            with patch("sys.platform", "win32"):
                info_win = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertTrue(info_win.has_update)
                self.assertEqual(info_win.latest_version, "1.7.3")
                self.assertEqual(info_win.download_url, "https://example.com/GBF_Accelerator_v1.7.3_GUI.zip")

            # Case 3: Reverse asset order in release list (Windows asset first, macOS asset second)
            mock_release_reversed = dict(mock_release, assets=[mock_release["assets"][1], mock_release["assets"][0]])
            mock_resp.json.return_value = mock_release_reversed
            with patch("sys.platform", "darwin"):
                info_mac_rev = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertEqual(info_mac_rev.download_url, "https://example.com/GBF_Accelerator_v1.7.3_macOS_universal2.zip")
            with patch("sys.platform", "win32"):
                info_win_rev = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertEqual(info_win_rev.download_url, "https://example.com/GBF_Accelerator_v1.7.3_GUI.zip")

            # Case 4: Windows platform with only macOS universal2 package -> download_url is None
            mock_release_mac_only = dict(mock_release, assets=[mock_release["assets"][0]])
            mock_resp.json.return_value = mock_release_mac_only
            with patch("sys.platform", "win32"):
                info_win_none = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertIsNone(info_win_none.download_url)

            # Case 5: Release has BOTH legacy mac_GUI.zip (first) and macOS_universal2.zip (second)
            # macOS MUST prioritize universal2 over legacy mac_GUI even if mac_GUI appears earlier
            mock_release_multi_mac = dict(mock_release, assets=[
                {
                    "name": "GBF_Accelerator_v1.7.3_mac_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.7.3_mac_GUI.zip",
                },
                {
                    "name": "GBF_Accelerator_v1.7.3_macOS_universal2.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.7.3_macOS_universal2.zip",
                },
                {
                    "name": "GBF_Accelerator_v1.7.3_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.7.3_GUI.zip",
                },
            ])
            mock_resp.json.return_value = mock_release_multi_mac
            with patch("sys.platform", "darwin"):
                info_mac_prio = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertEqual(info_mac_prio.download_url, "https://example.com/GBF_Accelerator_v1.7.3_macOS_universal2.zip")
            with patch("sys.platform", "win32"):
                info_win_prio = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertEqual(info_win_prio.download_url, "https://example.com/GBF_Accelerator_v1.7.3_GUI.zip")

            # Case 6: Fallback when only legacy mac_GUI.zip is available on macOS (no universal2 package)
            mock_release_legacy_mac = dict(mock_release, assets=[
                {
                    "name": "GBF_Accelerator_v1.7.3_mac_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.7.3_mac_GUI.zip",
                },
            ])
            mock_resp.json.return_value = mock_release_legacy_mac
            with patch("sys.platform", "darwin"):
                info_mac_legacy = update_manager.check_for_updates(current_ver="1.7.2")
                self.assertEqual(info_mac_legacy.download_url, "https://example.com/GBF_Accelerator_v1.7.3_mac_GUI.zip")

    def test_extract_sha256(self):
        hash_win = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        hash_mac = "1111111111111111111111111111111111111111111111111111111111111111"

        # 1. Standard label pattern
        body_label = f"Some notes\nSHA256: {hash_win}\nMore notes"
        self.assertEqual(update_manager.extract_sha256(body_label), hash_win)

        # 2. Markdown backticks and multi-platform isolation
        body_markdown = f"""### Checksums
- `GBF_Accelerator_v1.9.0_GUI.zip`: `{hash_win}`
- `GBF_Accelerator_v1.9.0_macOS_universal2.zip`: `{hash_mac}`
"""
        self.assertEqual(update_manager.extract_sha256(body_markdown, "GBF_Accelerator_v1.9.0_GUI.zip"), hash_win)
        self.assertEqual(update_manager.extract_sha256(body_markdown, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"), hash_mac)

        # 3. sha256sum multi-line format with binary (*) and text flags (no cross-line bleeding)
        body_sha256sum = f"""
{hash_win}  GBF_Accelerator_v1.9.0_GUI.zip
{hash_mac} *GBF_Accelerator_v1.9.0_macOS_universal2.zip
"""
        self.assertEqual(update_manager.extract_sha256(body_sha256sum, "GBF_Accelerator_v1.9.0_GUI.zip"), hash_win)
        self.assertEqual(update_manager.extract_sha256(body_sha256sum, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"), hash_mac)

        # 4. Markdown table format
        body_table = f"""| Asset | SHA-256 |
| :--- | :--- |
| `GBF_Accelerator_v1.9.0_GUI.zip` | `{hash_win}` |
| `GBF_Accelerator_v1.9.0_macOS_universal2.zip` | `{hash_mac}` |
"""
        self.assertEqual(update_manager.extract_sha256(body_table, "GBF_Accelerator_v1.9.0_GUI.zip"), hash_win)
        self.assertEqual(update_manager.extract_sha256(body_table, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"), hash_mac)

        # 5. BSD format
        body_bsd = f"""
SHA256 (GBF_Accelerator_v1.9.0_GUI.zip) = {hash_win}
SHA256 (GBF_Accelerator_v1.9.0_macOS_universal2.zip) = {hash_mac}
"""
        self.assertEqual(update_manager.extract_sha256(body_bsd, "GBF_Accelerator_v1.9.0_GUI.zip"), hash_win)
        self.assertEqual(update_manager.extract_sha256(body_bsd, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"), hash_mac)

        # 6. Indented / sub-item format
        body_indented = f"""- **GBF_Accelerator_v1.9.0_GUI.zip**
  - SHA-256: `{hash_win}`
- **GBF_Accelerator_v1.9.0_macOS_universal2.zip**
  - SHA-256: `{hash_mac}`
"""
        self.assertEqual(update_manager.extract_sha256(body_indented, "GBF_Accelerator_v1.9.0_GUI.zip"), hash_win)
        self.assertEqual(update_manager.extract_sha256(body_indented, "GBF_Accelerator_v1.9.0_macOS_universal2.zip"), hash_mac)

        # 7. Single hash release notes fallback
        body_single = f"Release v1.9.0\nSHA-256: {hash_win}"
        self.assertEqual(update_manager.extract_sha256(body_single, "GBF_Accelerator_v1.9.0_GUI.zip"), hash_win)
        self.assertEqual(update_manager.extract_sha256(body_single, ""), hash_win)

        # 8. Multiple hashes with unmatched filename returns None (safe fallback, avoid cross-matching)
        self.assertIsNone(update_manager.extract_sha256(body_markdown, "unmatched_other_file.zip"))

        # 9. Uppercase hash normalization to lowercase
        body_upper = f"SHA-256: {hash_win.upper()}"
        self.assertEqual(update_manager.extract_sha256(body_upper), hash_win)

        # 10. Empty or not found
        self.assertIsNone(update_manager.extract_sha256(""))
        self.assertIsNone(update_manager.extract_sha256("no hash here"))

    def test_download_release_asset_sha256_verification(self):
        import io
        import zipfile
        import hashlib
        import tempfile
        from pathlib import Path

        # Create an in-memory valid zip file
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as zf:
            zf.writestr("test.txt", "hello")
        valid_zip_bytes = buf.getvalue()
        correct_hash = hashlib.sha256(valid_zip_bytes).hexdigest()
        wrong_hash = "0" * 64

        with tempfile.TemporaryDirectory() as tmpdir:
            dest = Path(tmpdir) / "output.zip"

            # 1. Successful download with matching SHA-256
            with patch("httpx.Client") as mock_client:
                mock_resp = MagicMock()
                mock_resp.status_code = 200
                mock_resp.headers = {"content-length": str(len(valid_zip_bytes))}
                mock_resp.iter_bytes.return_value = [valid_zip_bytes]
                mock_client.return_value.__enter__.return_value.stream.return_value.__enter__.return_value = mock_resp

                ok, msg, path = update_manager.download_release_asset(
                    url="https://example.com/test.zip",
                    dest_path=dest,
                    expected_sha256=correct_hash,
                )
                self.assertTrue(ok)
                self.assertEqual(msg, "下载完成")
                self.assertTrue(dest.is_file())

            # Clean up dest for next subtest
            if dest.is_file():
                dest.unlink()

            # 2. Failed download with mismatched SHA-256
            with patch("httpx.Client") as mock_client:
                mock_resp = MagicMock()
                mock_resp.status_code = 200
                mock_resp.headers = {"content-length": str(len(valid_zip_bytes))}
                mock_resp.iter_bytes.return_value = [valid_zip_bytes]
                mock_client.return_value.__enter__.return_value.stream.return_value.__enter__.return_value = mock_resp

                ok, msg, path = update_manager.download_release_asset(
                    url="https://example.com/test.zip",
                    dest_path=dest,
                    expected_sha256=wrong_hash,
                )
                self.assertFalse(ok)
                self.assertIn("SHA-256", msg)
                self.assertFalse(dest.is_file())
                self.assertFalse(dest.with_name(dest.name + ".part").exists())

            # 3. Successful download with expected_sha256=None (backward compatibility)
            with patch("httpx.Client") as mock_client:
                mock_resp = MagicMock()
                mock_resp.status_code = 200
                mock_resp.headers = {"content-length": str(len(valid_zip_bytes))}
                mock_resp.iter_bytes.return_value = [valid_zip_bytes]
                mock_client.return_value.__enter__.return_value.stream.return_value.__enter__.return_value = mock_resp

                ok, msg, path = update_manager.download_release_asset(
                    url="https://example.com/test.zip",
                    dest_path=dest,
                    expected_sha256=None,
                )
                self.assertTrue(ok)
                self.assertEqual(msg, "下载完成")
                self.assertTrue(dest.is_file())

            if dest.is_file():
                dest.unlink()

            # 4. Successful download with whitespace expected_sha256 treated as None
            with patch("httpx.Client") as mock_client:
                mock_resp = MagicMock()
                mock_resp.status_code = 200
                mock_resp.headers = {"content-length": str(len(valid_zip_bytes))}
                mock_resp.iter_bytes.return_value = [valid_zip_bytes]
                mock_client.return_value.__enter__.return_value.stream.return_value.__enter__.return_value = mock_resp

                ok, msg, path = update_manager.download_release_asset(
                    url="https://example.com/test.zip",
                    dest_path=dest,
                    expected_sha256="   ",
                )
                self.assertTrue(ok)
                self.assertTrue(dest.is_file())

            if dest.is_file():
                dest.unlink()

            # 5. Successful download with uppercase expected_sha256
            with patch("httpx.Client") as mock_client:
                mock_resp = MagicMock()
                mock_resp.status_code = 200
                mock_resp.headers = {"content-length": str(len(valid_zip_bytes))}
                mock_resp.iter_bytes.return_value = [valid_zip_bytes]
                mock_client.return_value.__enter__.return_value.stream.return_value.__enter__.return_value = mock_resp

                ok, msg, path = update_manager.download_release_asset(
                    url="https://example.com/test.zip",
                    dest_path=dest,
                    expected_sha256=correct_hash.upper(),
                )
                self.assertTrue(ok)
                self.assertTrue(dest.is_file())

    def test_check_for_updates_sha256_integration(self):
        sample_hash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        mock_release = {
            "tag_name": "v1.9.0",
            "name": "v1.9.0 - Test",
            "body": f"Release with hash\nSHA-256: {sample_hash}",
            "html_url": "https://github.com/Sagisawa/GBF-Accelerator/releases/tag/v1.9.0",
            "published_at": "2026-09-18T00:00:00Z",
            "assets": [
                {
                    "name": "GBF_Accelerator_v1.9.0_GUI.zip",
                    "browser_download_url": "https://example.com/GBF_Accelerator_v1.9.0_GUI.zip",
                },
            ],
        }
        with patch("httpx.Client") as mock_client:
            mock_resp = MagicMock()
            mock_resp.status_code = 200
            mock_resp.json.return_value = mock_release
            mock_client.return_value.__enter__.return_value.get.return_value = mock_resp

            with patch("sys.platform", "win32"):
                info = update_manager.check_for_updates(current_ver="1.8.1")
                self.assertTrue(info.has_update)
                self.assertEqual(info.sha256, sample_hash)

    def test_cache_manager_ext_version_guard(self):
        import json
        import tempfile
        from pathlib import Path
        from cache_manager import CacheManager
        with tempfile.TemporaryDirectory() as tmpdir:
            cm = CacheManager(cache_base_dir=tmpdir)
            test_file = Path(tmpdir) / "test.png"
            test_ext = Path(tmpdir) / "test.png.ext"
            test_file.write_bytes(b"\x89PNG\r\n\x1a\n" + b"\x00" * 20)

            # Case 1: valid v: 1 metadata
            test_ext.write_text(json.dumps({"v": 1, "ct": "image/png", "ETag": '"test-etag"'}))
            res = cm.get_disk_cache("/test.png")
            self.assertIsNotNone(res)
            self.assertEqual(res[0]["Content-Type"], "image/png")
            self.assertEqual(res[0]["ETag"], '"test-etag"')
            meta_res = cm.peek_cache_meta("/test.png")
            self.assertIsNotNone(meta_res)
            self.assertEqual(meta_res[0], '"test-etag"')

            # Case 2: outdated or unsupported v != 1 (e.g. v: 99 or missing v)
            test_ext.write_text(json.dumps({"v": 99, "ct": "custom/unknown", "ETag": '"bad-etag"'}))
            res = cm.get_disk_cache("/test.png")
            self.assertIsNotNone(res)
            # Must fallback safely to extension-based MIME
            self.assertEqual(res[0]["Content-Type"], "image/png")
            # Must fallback to generated etag instead of bad-etag
            self.assertNotEqual(res[0]["ETag"], '"bad-etag"')
            meta_res = cm.peek_cache_meta("/test.png")
            self.assertIsNotNone(meta_res)
            self.assertNotEqual(meta_res[0], '"bad-etag"')

            # Case 3: corrupt non-dict metadata
            test_ext.write_text("invalid json or list [1, 2]")
            res = cm.get_disk_cache("/test.png")
            self.assertIsNotNone(res)
            self.assertEqual(res[0]["Content-Type"], "image/png")
            meta_res = cm.peek_cache_meta("/test.png")
            self.assertIsNotNone(meta_res)
            self.assertNotEqual(meta_res[0], "")

if __name__ == "__main__":
    unittest.main()
