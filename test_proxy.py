import asyncio
import httpx
import sys

import gbf_proxy
from config_manager import is_port_open

async def run_test():
    started_here = False
    if not is_port_open("127.0.0.1", 8124):
        print("[*] Starting GBF Proxy in background for testing...")
        gbf_proxy.LISTEN_HOST = "127.0.0.1"
        gbf_proxy.LISTEN_PORT = 8124
        gbf_proxy.start_proxy_thread()
        await asyncio.sleep(0.5)
        started_here = True

    try:
        print("[*] Testing GBF Speed Proxy using AsyncClient on port 8124...")
        async with httpx.AsyncClient(
            proxy="http://127.0.0.1:8124",
            verify="d:/acgpower/gbf_speed_proxy/certs/ca.crt",
            timeout=10.0,
        ) as client:
            # Test 1: Local Cache Hit
            url_cache = "https://prd-game-a-granbluefantasy.akamaized.net/assets/1772717316/css/arousal/form.css"
            resp = await client.get(url_cache)
            print(f"Test 1 - Cache Hit: status={resp.status_code}, X-Proxy-Cache={resp.headers.get('x-proxy-cache')}, bytes={len(resp.content)}")
            assert resp.status_code == 200
            assert resp.headers.get("x-proxy-cache") == "HIT"

            # Test 2: Mock 200 Endpoint
            url_mock = "https://game.granbluefantasy.jp/rest/error/js"
            resp_mock = await client.get(url_mock)
            print(f"Test 2 - Mock Endpoint: status={resp_mock.status_code}, json={resp_mock.json()}")
            assert resp_mock.status_code == 200

            # Test 3: OPTIONS Preflight Mock
            resp_opt = await client.options("https://game.granbluefantasy.jp/rest/multiraid/start.json")
            print(f"Test 3 - OPTIONS Preflight: status={resp_opt.status_code}, cors={resp_opt.headers.get('access-control-allow-origin')}")
            assert resp_opt.status_code == 200

            # Test 4: Upstream Relay through Clash
            url_upstream = "https://granbluefantasy.jp/"
            resp_up = await client.get(url_upstream)
            print(f"Test 4 - Clash Upstream: status={resp_up.status_code}, location={resp_up.headers.get('location')}")
            assert resp_up.status_code in (200, 301, 302)

            # Test 5: Game portal upstream
            resp_game = await client.get("https://game.granbluefantasy.jp/", follow_redirects=True)
            print(f"Test 5 - Game Page Upstream: status={resp_game.status_code}, title_found={'グランブルーファンタジー' in resp_game.text}")
            assert resp_game.status_code == 200
            assert "グランブルーファンタジー" in resp_game.text

            # Test 6: CA Fingerprint SHA-256
            from cert_manager import get_ca_fingerprint_sha256
            fp = get_ca_fingerprint_sha256()
            print(f"Test 6 - CA SHA-256 Fingerprint: {fp}")
            assert len(fp.split(":")) == 32

            # Test 7: Chunked Transfer-Encoding POST
            async def chunked_stream():
                yield b"part1_"
                yield b"part2_"
                yield b"payload"

            resp_chunked = await client.post(
                "https://game.granbluefantasy.jp/rest/error/js",
                content=chunked_stream(),
                headers={"transfer-encoding": "chunked"},
            )
            print(f"Test 7 - Chunked POST handling: status={resp_chunked.status_code}, body={resp_chunked.json()}")
            assert resp_chunked.status_code == 200

            # Test 8: Dynamic API CORS preservation (do not inject '*' into dynamic pages)
            print(f"Test 8 - Dynamic API CORS preservation: CORS header={resp_game.headers.get('access-control-allow-origin')}")
            assert resp_game.headers.get("access-control-allow-origin") != "*"

            # Test 9: Cache Query String Normalization
            url_cache_q = "https://prd-game-a-granbluefantasy.akamaized.net/assets/1772717316/css/arousal/form.css?_t=999999999&debug=1"
            resp_q = await client.get(url_cache_q)
            print(f"Test 9 - Cache Query Normalization: status={resp_q.status_code}, cache={resp_q.headers.get('x-proxy-cache')}")
            assert resp_q.status_code == 200
            assert resp_q.headers.get("x-proxy-cache") == "HIT"

            # Test 10: Cache Integrity Verification (reject empty and HTML error pages for media)
            from cache_manager import cache_manager
            assert not cache_manager.save_cache("/assets/test/broken.png", {"content-type": "text/html"}, b"<html>Error</html>")
            assert not cache_manager.save_cache("/assets/test/empty.png", {"content-type": "image/png"}, b"")
            print("Test 10 - Cache Integrity (rejected HTML error page & empty payload): OK")

            # Test 11: Immutable Header Logic (only versioned assets get immutable when enabled)
            headers_test_ver = {}
            cache_manager._apply_browser_cache_headers(headers_test_ver, "/assets/1772717316/foo.js")
            # Default enable_browser_cache is False -> should NOT contain immutable
            assert "immutable" not in headers_test_ver.get("Cache-Control", "")

            # If temporarily enabled:
            from config_manager import config_manager
            config_manager.config["enable_browser_cache"] = True
            headers_ver_on = {}
            cache_manager._apply_browser_cache_headers(headers_ver_on, "/assets/1772717316/foo.js")
            assert "immutable" in headers_ver_on.get("Cache-Control", "")

            # Unversioned asset even when enabled should NEVER get immutable
            headers_unver = {}
            cache_manager._apply_browser_cache_headers(headers_unver, "/manifest.json")
            assert "immutable" not in headers_unver.get("Cache-Control", "")

            # Test 12: Path Traversal Rejection
            assert cache_manager._get_local_path("/C:/Windows/win.ini") is None
            assert cache_manager._get_local_path("/../../etc/passwd") is None
            assert cache_manager._get_local_path("/assets/../../../boot.ini") is None
            assert cache_manager.get_cache("/C:/Windows/win.ini") is None
            assert cache_manager.get_cache("/../../test.png") is None
            print("Test 12 - Path Traversal & Arbitrary File Read Rejection: OK")

            # Test 13: HEAD Request Zero-Body & Content-Length Preservation
            resp_head = await client.head(url_cache)
            print(f"Test 13 - HEAD Request: status={resp_head.status_code}, content_len={len(resp_head.content)}, header_len={resp_head.headers.get('content-length')}")
            assert resp_head.status_code == 200
            assert resp_head.headers.get("x-proxy-cache") == "HIT"
            assert len(resp_head.content) == 0
            assert int(resp_head.headers.get("content-length", 0)) > 0

            # Test 14: Local Unique CA & SAN Scope Check
            from cert_manager import SAN_DOMAINS
            assert "*.akamaized.net" not in SAN_DOMAINS
            assert "prd-game-a-granbluefantasy.akamaized.net" in SAN_DOMAINS
            print("Test 14 - Local Dynamic CA & Scoped SAN Domains: OK")

            # Test 15: Fingerprint-based CA Check (SHA-1 verification against certs/ca.crt)
            from config_manager import is_ca_installed
            ca_status = is_ca_installed()
            print(f"Test 15 - Fingerprint CA Installed Status: {ca_status}")
            assert isinstance(ca_status, bool)

            # Test 16: Legacy Leaked CA Detection
            from config_manager import check_legacy_leaked_ca_installed, LEGACY_LEAKED_CA_SHA1
            legacy_detected = check_legacy_leaked_ca_installed()
            print(f"Test 16 - Legacy Leaked CA ({LEGACY_LEAKED_CA_SHA1[:8]}...) Detected: {legacy_detected}")
            assert isinstance(legacy_detected, bool)

            # Test 17: Multi Set-Cookie Header Preservation
            class MockWriter:
                def __init__(self):
                    self.data = b""
                def write(self, d):
                    self.data += d
                async def drain(self):
                    pass

            mock_writer = MockWriter()
            mock_upstream = httpx.Response(
                200,
                headers=[
                    ("Set-Cookie", "session_id=abc1234; path=/; HttpOnly"),
                    ("Set-Cookie", "remember_me=true; expires=Wed, 21 Oct 2026 07:28:00 GMT; path=/"),
                ],
                content=b'{"ok": true}',
            )
            await gbf_proxy.forward_upstream_response(mock_writer, {}, mock_upstream)
            raw_resp = mock_writer.data.decode("iso-8859-1")
            set_cookie_lines = [line for line in raw_resp.split("\r\n") if line.startswith("Set-Cookie:")]
            assert len(set_cookie_lines) == 2
            assert "session_id=abc1234" in set_cookie_lines[0]
            assert "remember_me=true" in set_cookie_lines[1]
            print("Test 17 - Multi Set-Cookie Header Preservation: OK")

            print("\n[+] ALL 17 TESTS PASSED SUCCESSFULLY!")
    finally:
        if started_here:
            gbf_proxy.stop_proxy_thread()

if __name__ == "__main__":
    asyncio.run(run_test())
