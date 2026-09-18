"""
Test Suite for GBF-Accelerator Control Plane Server (control_server.py)
Validates:
1. Server startup and shutdown on an isolated test port (8129).
2. All REST API endpoints (status, config, cache, prefetch, telemetry, logs).
3. SSE (Server-Sent Events) event streaming (/api/events).
4. CORS preflight (OPTIONS).
5. SPA / Built-in fallback static landing page.
6. Thread isolation: proxy start/stop does not terminate the control server.
"""

import asyncio
import json
import os
import sys
import time
import unittest

import httpx

import control_server
import gbf_proxy
from config_manager import config_manager


class TestControlServer(unittest.IsolatedAsyncioTestCase):
    TEST_CONTROL_PORT = 8129

    @classmethod
    def setUpClass(cls):
        # Start control server on isolated port
        started = control_server.start_control_server(
            port=cls.TEST_CONTROL_PORT,
            host="127.0.0.1",
            timeout=3.0,
        )
        assert started, f"Failed to start control server on port {cls.TEST_CONTROL_PORT}"
        assert control_server.is_control_server_running()

    @classmethod
    def tearDownClass(cls):
        control_server.stop_control_server()
        assert not control_server.is_control_server_running()

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.TEST_CONTROL_PORT}"

    async def test_01_cors_preflight(self):
        """OPTIONS preflight must return 204 No Content with CORS headers."""
        async with httpx.AsyncClient() as client:
            resp = await client.options(f"{self.base_url}/api/status")
            self.assertEqual(resp.status_code, 204)
            self.assertIn("access-control-allow-origin", resp.headers)
            self.assertEqual(resp.headers["access-control-allow-origin"], "*")
            self.assertIn("access-control-allow-methods", resp.headers)

    async def test_02_get_status(self):
        """GET /api/status returns valid system status JSON."""
        async with httpx.AsyncClient() as client:
            resp = await client.get(f"{self.base_url}/api/status")
            self.assertEqual(resp.status_code, 200)
            data = resp.json()
            self.assertIn("version", data)
            self.assertIn("proxy_running", data)
            self.assertIn("listen_port", data)
            self.assertIn("control_port", data)
            self.assertIn("active_api_count", data)
            self.assertIn("active_foreground_assets", data)
            self.assertIn("requests", data)
            self.assertIn("cache", data)

    async def test_03_get_and_apply_config(self):
        """GET /api/config and POST /api/config/apply."""
        async with httpx.AsyncClient() as client:
            # 1. Read config
            resp = await client.get(f"{self.base_url}/api/config")
            self.assertEqual(resp.status_code, 200)
            data = resp.json()
            self.assertTrue(data.get("ok"))
            self.assertIn("config", data)

            orig_mb = data["config"].get("ram_cache_max_mb", 256)

            # 2. Update config
            patch_resp = await client.post(
                f"{self.base_url}/api/config/apply",
                json={"ram_cache_max_mb": 300},
            )
            self.assertEqual(patch_resp.status_code, 200)
            self.assertTrue(patch_resp.json().get("ok"))
            self.assertEqual(config_manager.config["ram_cache_max_mb"], 300)

            # Revert to original
            await client.post(
                f"{self.base_url}/api/config/apply",
                json={"ram_cache_max_mb": orig_mb},
            )
            self.assertEqual(config_manager.config["ram_cache_max_mb"], orig_mb)

    async def test_04_cache_stats_and_clear(self):
        """GET /api/cache/stats and POST /api/cache/clear."""
        async with httpx.AsyncClient() as client:
            resp = await client.get(f"{self.base_url}/api/cache/stats")
            self.assertEqual(resp.status_code, 200)
            data = resp.json()
            self.assertTrue(data.get("ok"))
            self.assertIn("ram_items", data)
            self.assertIn("ram_mb", data)
            self.assertIn("hit_ratio_percent", data)

            # Clear RAM cache
            clear_resp = await client.post(f"{self.base_url}/api/cache/clear?ram_only=true")
            self.assertEqual(clear_resp.status_code, 200)
            clear_data = clear_resp.json()
            self.assertTrue(clear_data.get("ok"))
            self.assertTrue(clear_data.get("ram_cleared"))
            self.assertFalse(clear_data.get("disk_cleared"))

    async def test_05_cache_audit_and_slim(self):
        """POST /api/cache/audit and POST /api/cache/slim trigger background jobs."""
        async with httpx.AsyncClient() as client:
            audit_resp = await client.post(f"{self.base_url}/api/cache/audit")
            self.assertEqual(audit_resp.status_code, 200)
            self.assertTrue(audit_resp.json().get("ok"))

            slim_resp = await client.post(f"{self.base_url}/api/cache/slim?keep=10")
            self.assertEqual(slim_resp.status_code, 200)
            self.assertTrue(slim_resp.json().get("ok"))

    async def test_06_prefetch_status(self):
        """GET /api/prefetch/status returns prefetch metrics and yielding state."""
        async with httpx.AsyncClient() as client:
            resp = await client.get(f"{self.base_url}/api/prefetch/status")
            self.assertEqual(resp.status_code, 200)
            data = resp.json()
            self.assertTrue(data.get("ok"))
            self.assertIn("queue_size", data)
            self.assertIn("is_yielding", data)
            self.assertIn("prefetch_requests", data)

    async def test_07_telemetry_and_logs(self):
        """GET /api/telemetry and GET /api/logs."""
        async with httpx.AsyncClient() as client:
            telem_resp = await client.get(f"{self.base_url}/api/telemetry")
            self.assertEqual(telem_resp.status_code, 200)
            telem_data = telem_resp.json()
            self.assertTrue(telem_data.get("ok"))
            self.assertIn("telemetry", telem_data)

            log_resp = await client.get(f"{self.base_url}/api/logs")
            self.assertEqual(log_resp.status_code, 200)
            log_data = log_resp.json()
            self.assertTrue(log_data.get("ok"))
            self.assertIsInstance(log_data.get("logs"), list)

    async def test_08_fallback_landing_page(self):
        """GET / returns HTML landing page with 200 OK."""
        async with httpx.AsyncClient() as client:
            resp = await client.get(f"{self.base_url}/")
            self.assertEqual(resp.status_code, 200)
            self.assertIn("text/html", resp.headers["content-type"])
            self.assertIn("GBF-Accelerator", resp.text)

    async def test_09_sse_events_stream(self):
        """GET /api/events establishes SSE connection and receives initial burst."""
        received_events = []

        async def read_sse():
            async with httpx.AsyncClient(timeout=3.0) as client:
                async with client.stream("GET", f"{self.base_url}/api/events") as stream:
                    self.assertEqual(stream.status_code, 200)
                    self.assertIn("text/event-stream", stream.headers["content-type"])
                    async for line in stream.aiter_lines():
                        if line.startswith("event:"):
                            received_events.append(line.split(":", 1)[1].strip())
                        if len(received_events) >= 1:
                            # Successfully got initial event(s)
                            break

        await asyncio.wait_for(read_sse(), timeout=2.5)
        self.assertTrue(len(received_events) >= 1)
        self.assertIn("status", received_events)

    async def test_10_proxy_lifecycle_thread_isolation(self):
        """POST /api/proxy/stop and POST /api/proxy/start do NOT terminate control server."""
        async with httpx.AsyncClient() as client:
            # 1. Stop proxy via control API
            stop_resp = await client.post(f"{self.base_url}/api/proxy/stop")
            self.assertEqual(stop_resp.status_code, 200)
            self.assertTrue(stop_resp.json().get("ok"))

            # Verify control server is STILL running and answering
            status_resp = await client.get(f"{self.base_url}/api/status")
            self.assertEqual(status_resp.status_code, 200)
            self.assertFalse(status_resp.json().get("proxy_running"))

            # 2. Start proxy via control API
            start_resp = await client.post(f"{self.base_url}/api/proxy/start")
            self.assertEqual(start_resp.status_code, 200)
            self.assertTrue(start_resp.json().get("ok"))

            # Stop it again cleanly
            await client.post(f"{self.base_url}/api/proxy/stop")


if __name__ == "__main__":
    unittest.main()
