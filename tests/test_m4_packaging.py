"""
Milestone M4 Verification Test Suite:
Validates single native binary packaging, embedded Web UI serving, SPA fallback,
desktop integration, and distribution release archive integrity.
"""

import os
import shutil
import signal
import subprocess
import sys
import time
import unittest
import zipfile
from pathlib import Path

import httpx

REPO_ROOT = Path(__file__).parent.parent.resolve()
BIN_DIR = REPO_ROOT / "bin"
RELEASE_DIR = REPO_ROOT / "release"


class TestMilestoneM4(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.proxy_port = 8134
        cls.control_port = 8135
        cls.exe_path = BIN_DIR / ("GBF_Accelerator.exe" if sys.platform == "win32" else "GBF_Accelerator")
        if not cls.exe_path.is_file():
            # Fallback to gbf-proxy
            cls.exe_path = BIN_DIR / ("gbf-proxy.exe" if sys.platform == "win32" else "gbf-proxy")

        if not cls.exe_path.is_file():
            raise FileNotFoundError(f"Target binary not found at {cls.exe_path}")

        # Start binary in headless mode for test environment
        cmd = [
            str(cls.exe_path),
            "--proxy-port", str(cls.proxy_port),
            "--control-port", str(cls.control_port),
            "--nogui",
        ]
        cls.proc = subprocess.Popen(
            cmd,
            cwd=str(REPO_ROOT),
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        cls.client = httpx.Client(trust_env=False, timeout=5.0)

        # Wait for control server to become responsive
        cls.control_url = f"http://127.0.0.1:{cls.control_port}"
        start_time = time.time()
        ready = False
        while time.time() - start_time < 8.0:
            if cls.proc.poll() is not None:
                out, err = cls.proc.communicate()
                raise RuntimeError(f"Binary exited prematurely with code {cls.proc.returncode}:\nOUT: {out}\nERR: {err}")
            try:
                r = cls.client.get(f"{cls.control_url}/api/status")
                if r.status_code == 200:
                    ready = True
                    break
            except Exception:
                time.sleep(0.15)

        if not ready:
            cls.proc.kill()
            raise TimeoutError(f"Failed to start binary {cls.exe_path} within 8 seconds")

    @classmethod
    def tearDownClass(cls):
        if hasattr(cls, "client"):
            cls.client.close()
        if cls.proc and cls.proc.poll() is None:
            try:
                if sys.platform == "win32":
                    cls.proc.terminate()
                else:
                    cls.proc.send_signal(signal.SIGTERM)
                cls.proc.wait(timeout=3.0)
            except Exception:
                cls.proc.kill()

    def test_01_embedded_index_html(self):
        """GET / serves embedded React SPA index.html with 200 OK and utf-8 text/html."""
        resp = self.client.get(f"{self.control_url}/")
        self.assertEqual(resp.status_code, 200)
        self.assertIn("text/html", resp.headers.get("content-type", ""))
        self.assertIn("GBF-Accelerator 控制台", resp.text)
        self.assertIn("assets/index-", resp.text)

    def test_02_spa_client_side_routing_fallback(self):
        """GET /dashboard, /cache, /settings, /timeline routes all fall back to embedded index.html."""
        spa_routes = ["/dashboard", "/cache", "/settings", "/timeline"]
        for route in spa_routes:
            with self.subTest(route=route):
                resp = self.client.get(f"{self.control_url}{route}")
                self.assertEqual(resp.status_code, 200)
                self.assertIn("text/html", resp.headers.get("content-type", ""))
                self.assertIn("<div id=\"root\"></div>", resp.text)

    def test_03_embedded_assets_mime_and_caching(self):
        """Embedded JS and CSS assets have correct MIME types and immutable caching."""
        # Find asset names from index.html
        resp = self.client.get(f"{self.control_url}/")
        content = resp.text

        # Test CSS
        self.assertIn("index-BLwYVszU.css", content)
        css_resp = self.client.get(f"{self.control_url}/assets/index-BLwYVszU.css")
        self.assertEqual(css_resp.status_code, 200)
        self.assertIn("text/css", css_resp.headers.get("content-type", ""))
        self.assertIn("immutable", css_resp.headers.get("cache-control", ""))
        self.assertGreater(len(css_resp.content), 100)

        # Test JS
        self.assertIn("index-CbwdOLUq.js", content)
        js_resp = self.client.get(f"{self.control_url}/assets/index-CbwdOLUq.js")
        self.assertEqual(js_resp.status_code, 200)
        self.assertIn("javascript", js_resp.headers.get("content-type", ""))
        self.assertIn("immutable", js_resp.headers.get("cache-control", ""))
        self.assertGreater(len(js_resp.content), 500)

        # Test Favicon
        ico_resp = self.client.get(f"{self.control_url}/favicon.ico")
        self.assertEqual(ico_resp.status_code, 200)
        self.assertIn("image/", ico_resp.headers.get("content-type", ""))

    def test_04_proxy_lifecycle_control(self):
        """Control API /api/proxy/stop and /api/proxy/start dynamically toggle proxy engine state."""
        # Check initial state is running
        st = self.client.get(f"{self.control_url}/api/status").json()
        self.assertTrue(st["proxy_running"])
        self.assertEqual(st["engine"], "go")

        # Stop proxy
        stop_resp = self.client.post(f"{self.control_url}/api/proxy/stop")
        self.assertEqual(stop_resp.status_code, 200)
        st_stopped = self.client.get(f"{self.control_url}/api/status").json()
        self.assertFalse(st_stopped["proxy_running"])

        # Restart proxy
        start_resp = self.client.post(f"{self.control_url}/api/proxy/start")
        self.assertEqual(start_resp.status_code, 200)
        st_restarted = self.client.get(f"{self.control_url}/api/status").json()
        self.assertTrue(st_restarted["proxy_running"])

    def test_05_release_zip_package_integrity(self):
        """Release zip package contains standalone executable and all required auxiliary files."""
        target_zip = RELEASE_DIR / "GBF_Accelerator_v1.8.0_GUI.zip"
        self.assertTrue(target_zip.is_file(), f"Release zip not found at {target_zip}")

        with zipfile.ZipFile(target_zip, "r") as zf:
            namelist = zf.namelist()
            self.assertIn("GBF_Accelerator.exe", namelist)
            self.assertIn("SwitchyOmega_GBF.bak", namelist)
            self.assertIn("proxy.pac", namelist)
            self.assertIn("使用说明.txt", namelist)
            self.assertIn("LICENSE", namelist)

            # Ensure binary inside zip is non-empty
            exe_info = zf.getinfo("GBF_Accelerator.exe")
            self.assertGreater(exe_info.file_size, 5 * 1024 * 1024)

    def test_06_standalone_distribution_isolated_directory(self):
        """Extract and run GBF_Accelerator from an isolated directory with zero external web files."""
        import tempfile
        target_zip = RELEASE_DIR / "GBF_Accelerator_v1.8.0_GUI.zip"
        self.assertTrue(target_zip.is_file(), f"Target zip not found: {target_zip}")

        tmp_dir = tempfile.mkdtemp()
        tmp_path = Path(tmp_dir)

        try:
            with zipfile.ZipFile(target_zip, "r") as zf:
                zf.extractall(tmp_path)

            # Note: Do NOT copy certs directory; the binary must self-initialize Root CA in pure isolated folder
            exe_file = tmp_path / "GBF_Accelerator.exe"
            self.assertTrue(exe_file.is_file())

            isolated_proxy_port = 8138
            isolated_control_port = 8139
            cmd = [
                str(exe_file),
                "--proxy-port", str(isolated_proxy_port),
                "--control-port", str(isolated_control_port),
                "--nogui",
            ]
            proc = subprocess.Popen(
                cmd,
                cwd=str(tmp_path),
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )

            try:
                line = proc.stdout.readline()
                self.assertIn("started", line.lower())

                ctrl_url = f"http://127.0.0.1:{isolated_control_port}"
                start_t = time.time()
                ready = False
                while time.time() - start_t < 6.0:
                    if proc.poll() is not None:
                        out, err = proc.communicate()
                        self.fail(f"Isolated binary exited prematurely:\nOUT: {out}\nERR: {err}")
                    try:
                        r = self.client.get(f"{ctrl_url}/api/status")
                        if r.status_code == 200:
                            ready = True
                            break
                    except Exception:
                        time.sleep(0.1)

                self.assertTrue(ready, "Isolated binary control server did not respond")

                # Verify GET / serves embedded SPA HTML
                resp_dash = self.client.get(f"{ctrl_url}/")
                self.assertEqual(resp_dash.status_code, 200)
                self.assertIn("text/html", resp_dash.headers.get("content-type", ""))
                self.assertIn("GBF-Accelerator 控制台", resp_dash.text)

                # Verify SPA route fallback works from embedded assets
                resp_spa = self.client.get(f"{ctrl_url}/cache")
                self.assertEqual(resp_spa.status_code, 200)
                self.assertIn("text/html", resp_spa.headers.get("content-type", ""))
                self.assertIn("<div id=\"root\"></div>", resp_spa.text)

                # Verify embedded asset serving
                resp_css = self.client.get(f"{ctrl_url}/assets/index-BLwYVszU.css")
                self.assertEqual(resp_css.status_code, 200)
                self.assertIn("text/css", resp_css.headers.get("content-type", ""))
                self.assertIn("immutable", resp_css.headers.get("cache-control", ""))

                # Verify certs directory was auto-generated by the engine
                self.assertTrue((tmp_path / "certs" / "ca.crt").is_file(), "ca.crt was not auto-generated")

            finally:
                if proc.poll() is None:
                    proc.terminate()
                    try:
                        proc.wait(timeout=2.0)
                    except Exception:
                        proc.kill()
                if proc.stdout:
                    proc.stdout.close()
                if proc.stderr:
                    proc.stderr.close()

        finally:
            shutil.rmtree(tmp_path, ignore_errors=True)

    def test_07_cli_version_flag(self):
        """Binary supports -v and --version CLI flags to print version and exit 0."""
        for flag in ["-v", "--version"]:
            with self.subTest(flag=flag):
                res = subprocess.run(
                    [str(self.exe_path), flag],
                    capture_output=True,
                    text=True,
                    timeout=3.0,
                )
                self.assertEqual(res.returncode, 0)
                self.assertIn("GBF-Accelerator v", res.stdout)

    def test_08_missing_asset_returns_404_json(self):
        """Request for missing file with extension in /assets returns 404 JSON, not HTML fallback."""
        resp = self.client.get(f"{self.control_url}/assets/nonexistent_asset.png")
        self.assertEqual(resp.status_code, 404)
        self.assertIn("application/json", resp.headers.get("content-type", ""))
        self.assertIn("File not found", resp.text)


if __name__ == "__main__":
    unittest.main()

