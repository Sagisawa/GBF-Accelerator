import asyncio
import httpx
import sys

import gbf_proxy
from config_manager import is_port_open

async def run_test():
    test_port = 8126
    print(f"[*] Starting GBF Proxy in background for testing on port {test_port}...")
    gbf_proxy.LISTEN_HOST = "127.0.0.1"
    gbf_proxy.LISTEN_PORT = test_port
    gbf_proxy.start_proxy_thread()
    await asyncio.sleep(0.8)

    try:
        print(f"[*] Testing GBF Speed Proxy using AsyncClient on port {test_port}...")
        import certifi, ssl
        ssl_ctx = ssl.create_default_context(cafile=certifi.where())
        ssl_ctx.load_verify_locations(cafile="d:/acgpower/gbf_speed_proxy/certs/ca.crt")
        async with httpx.AsyncClient(
            proxy=f"http://127.0.0.1:{test_port}",
            verify=ssl_ctx,
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
            from config_manager import config_manager
            orig_setting = config_manager.config.get("enable_browser_cache", True)
            try:
                config_manager.config["enable_browser_cache"] = False
                headers_test_ver = {}
                cache_manager._apply_browser_cache_headers(headers_test_ver, "/assets/1772717316/foo.js")
                assert "immutable" not in headers_test_ver.get("Cache-Control", "")

                config_manager.config["enable_browser_cache"] = True
                headers_ver_on = {}
                cache_manager._apply_browser_cache_headers(headers_ver_on, "/assets/1772717316/foo.js")
                assert "immutable" in headers_ver_on.get("Cache-Control", "")

                # Unversioned asset even when enabled should NEVER get immutable
                headers_unver = {}
                cache_manager._apply_browser_cache_headers(headers_unver, "/manifest.json")
                assert "immutable" not in headers_unver.get("Cache-Control", "")
                print("Test 11 - Immutable Header Logic: OK")
            finally:
                config_manager.config["enable_browser_cache"] = orig_setting

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
            assert "prd-game-a-granbluefantasy-steam.akamaized.net" in SAN_DOMAINS
            assert gbf_proxy._is_gbf_akamai_host("prd-game-a-granbluefantasy-steam.akamaized.net")
            assert not gbf_proxy._is_gbf_akamai_host("unrelated.example.akamaized.net")
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

            # Test 18: _ActiveApiTracker exception safety
            initial_api_count = gbf_proxy.ACTIVE_API_COUNT
            try:
                with gbf_proxy._ActiveApiTracker():
                    assert gbf_proxy.ACTIVE_API_COUNT == initial_api_count + 1
                    raise ValueError("Simulated API failure")
            except ValueError:
                pass
            assert gbf_proxy.ACTIVE_API_COUNT == initial_api_count
            print("Test 18 - _ActiveApiTracker Exception Safety: OK")

            # Test 19: LimitOverrunError handling in read_http_request
            class MockOverrunReader:
                async def readuntil(self, separator=b"\r\n\r\n"):
                    raise asyncio.LimitOverrunError("Header exceeds buffer limit", 65536)

            overrun_res = await gbf_proxy.read_http_request(MockOverrunReader())
            assert overrun_res is None
            print("Test 19 - read_http_request LimitOverrunError Handling: OK")

            # Test 20: Bounded background cache save
            save_url = "/assets/test/bounded_save.png"
            await gbf_proxy._bounded_save_cache(save_url, {"content-type": "image/png"}, b"\x89PNG\r\n\x1a\n")
            assert cache_manager.has_cache(save_url)
            p = cache_manager._get_local_path(save_url)
            if p and p.exists():
                p.unlink()
            if p:
                ext_p = p.parent / (p.name + ".ext")
                if ext_p.exists():
                    ext_p.unlink()
            print("Test 20 - Bounded Cache Save Concurrency: OK")

            # Test 21: SingleFlight inflight map tracking
            flight_path = "/assets/test/flight_demo.js"
            flight_key = f"game.granbluefantasy.jp{flight_path}"
            loop = asyncio.get_running_loop()
            fut = loop.create_future()
            gbf_proxy._inflight_fetches[flight_key] = fut
            assert flight_key in gbf_proxy._inflight_fetches
            fut.set_result(({"x-test": "flight"}, b"console.log('flight');"))
            res = await gbf_proxy._inflight_fetches[flight_key]
            assert res[1] == b"console.log('flight');"
            gbf_proxy._inflight_fetches.pop(flight_key, None)
            print("Test 21 - SingleFlight Future Coalescing: OK")

            # Test 22: Instant RAM cache sync store
            test_ram_path = "/assets/test/instant_ram.js"
            cache_manager.store_ram_cache(test_ram_path, {"content-type": "application/javascript"}, b"console.log('ram_instant');")
            assert cache_manager.has_cache(test_ram_path)
            ram_cached = cache_manager.get_cache(test_ram_path)
            assert ram_cached is not None
            assert ram_cached[0]["X-Cache-Source"] == "RAM"
            assert ram_cached[1] == b"console.log('ram_instant');"
            print("Test 22 - Instant RAM Cache Hot-Path Store: OK")

            # Test 23: End-to-End Concurrent SingleFlight Coalescing
            real_request = gbf_proxy.http_client.request
            sf_call_count = 0

            async def mock_concurrent_request(method, url, **kwargs):
                nonlocal sf_call_count
                if "concurrent_singleflight_test.js" in str(url):
                    sf_call_count += 1
                    # Tiny delay to guarantee both concurrent client requests overlap in flight
                    await asyncio.sleep(0.08)
                    return httpx.Response(
                        200,
                        headers={"content-type": "application/javascript", "etag": '"sf-test-123"'},
                        content=b"/* singleflight verified */",
                        request=httpx.Request(method, url),
                    )
                return await real_request(method, url, **kwargs)

            gbf_proxy.http_client.request = mock_concurrent_request
            try:
                test_sf_url = "https://prd-game-a-granbluefantasy.akamaized.net/assets/test/concurrent_singleflight_test.js"
                res1, res2 = await asyncio.gather(
                    client.get(test_sf_url),
                    client.get(test_sf_url),
                )
                assert res1.status_code == 200
                assert res2.status_code == 200
                assert res1.content == b"/* singleflight verified */"
                assert res2.content == b"/* singleflight verified */"
                assert sf_call_count == 1, f"Expected exactly 1 upstream fetch, got {sf_call_count}"
                print(f"Test 23 - End-to-End Concurrent SingleFlight Coalescing (2 requests -> {sf_call_count} upstream fetch): OK")
            finally:
                gbf_proxy.http_client.request = real_request
                p = cache_manager._get_local_path("/assets/test/concurrent_singleflight_test.js")
                if p and p.exists():
                    p.unlink()
                if p:
                    ext_p = p.parent / (p.name + ".ext")
                    if ext_p.exists():
                        ext_p.unlink()

            # Test 24: LAN IP detection
            from config_manager import get_lan_ip
            lan_ip = get_lan_ip()
            assert isinstance(lan_ip, str) and len(lan_ip.split(".")) == 4
            assert all(p.isdigit() and 0 <= int(p) <= 255 for p in lan_ip.split("."))
            print(f"Test 24 - LAN IP Detection: {lan_ip} -> OK")

            # Direct client for testing local HTTP control & download endpoints
            async with httpx.AsyncClient(trust_env=False, timeout=5.0) as direct_http:
                # Test 25: Root CA HTTP download endpoint (/ca.crt)
                resp_ca = await direct_http.get(f"http://127.0.0.1:{test_port}/ca.crt")
                assert resp_ca.status_code == 200
                assert resp_ca.headers.get("content-type") == "application/x-x509-ca-cert"
                assert "attachment" in resp_ca.headers.get("content-disposition", "")
                assert b"BEGIN CERTIFICATE" in resp_ca.content
                print(f"Test 25 - Root CA /ca.crt HTTP Endpoint: status={resp_ca.status_code}, length={len(resp_ca.content)} -> OK")

                # Test 26: Dynamic PAC generation with custom Host header
                resp_pac = await direct_http.get(
                    f"http://127.0.0.1:{test_port}/proxy.pac",
                    headers={"Host": "192.168.1.188:8126"},
                )
                assert resp_pac.status_code == 200
                assert resp_pac.headers.get("content-type") == "application/x-ns-proxy-autoconfig"
                assert "PROXY 192.168.1.188:8126; DIRECT" in resp_pac.text
                print(f"Test 26 - Dynamic PAC with Host Header (192.168.1.188:8126): status={resp_pac.status_code} -> OK")

                # Test 27: Mobile setup HTML landing page (GET /)
                resp_index = await direct_http.get(f"http://127.0.0.1:{test_port}/")
                assert resp_index.status_code == 200
                assert "text/html" in resp_index.headers.get("content-type", "")
                assert "GBF 加速器" in resp_index.text
                assert "/ca.crt" in resp_index.text
                assert "/proxy.pac" in resp_index.text
                print(f"Test 27 - Mobile LAN Setup Landing Page (GET /): status={resp_index.status_code} -> OK")

            # Test 28: Apple/Safari Modern TLS Certificate Policy Compliance (validity <= 398 days, SERVER_AUTH EKU)
            from cert_manager import ensure_server_cert, SERVER_CERT_PATH
            from cryptography import x509
            ensure_server_cert()
            with open(SERVER_CERT_PATH, "rb") as cf:
                server_cert = x509.load_pem_x509_certificate(cf.read())

            validity_days = (server_cert.not_valid_after_utc - server_cert.not_valid_before_utc).days
            assert validity_days <= 398, f"Apple/Safari TLS policy suggests server cert validity <= 398 days, got {validity_days}"

            eku_ext = server_cert.extensions.get_extension_for_oid(x509.ExtensionOID.EXTENDED_KEY_USAGE).value
            assert x509.ExtendedKeyUsageOID.SERVER_AUTH in eku_ext, "Server cert must contain SERVER_AUTH EKU"
            print(f"Test 28 - Apple/Safari Modern TLS Policy Compliance (validity={validity_days}d <= 398d, EKU=SERVER_AUTH): OK")

            # Test 29: SkyLeap & Mobage multi-level subdomain SAN compliance
            sans = set(server_cert.extensions.get_extension_for_oid(x509.ExtensionOID.SUBJECT_ALTERNATIVE_NAME).value.get_values_for_type(x509.DNSName))
            required_mobage_domains = {
                "gbf.game.mbga.jp",
                "*.game.mbga.jp",
                "*.sp.pf.mbga.jp",
                "g12016007.sp.pf.mbga.jp",
                "*.pf.mbga.jp",
                "connect.mobage.jp",
                "*.connect.mobage.jp",
                "sp.mbga.jp",
                "*.sp.mbga.jp",
                "mobage.jp",
                "*.mobage.jp",
            }
            assert required_mobage_domains.issubset(sans), f"Missing Mobage domains in SAN: {required_mobage_domains - sans}"
            print("Test 29 - SkyLeap & Mobage Multi-level SANs (gbf.game.mbga.jp, g12016007.sp.pf.mbga.jp, etc.): OK")

            # Test 30: SkyLeap portal upstream proxy request (TLS handshake + query string forwarding)
            skyleap_url = "https://gbf.game.mbga.jp/?opensocial_viewer_id=129649231&token=b7c52c36fab15341ef70"
            resp_skyleap = await client.get(skyleap_url)
            print(f"Test 30 - SkyLeap GBF Gateway Upstream: status={resp_skyleap.status_code}")
            assert resp_skyleap.status_code in (200, 301, 302, 403, 502, 504)  # Valid HTTP response received across TLS handshake

            # Test 31: Mobage OpenSocial container upstream proxy request (g12016007.sp.pf.mbga.jp)
            pf_url = "https://g12016007.sp.pf.mbga.jp/"
            resp_pf = await client.get(pf_url)
            print(f"Test 31 - Mobage OpenSocial Container Upstream: status={resp_pf.status_code}")
            assert resp_pf.status_code in (200, 301, 302, 403, 502, 504)

            # Test 32: Network ACL & Private Subnet Access Control
            from gbf_proxy import is_client_ip_allowed
            import config_manager
            # Loopback always allowed
            assert is_client_ip_allowed("127.0.0.1") is True
            assert is_client_ip_allowed("::1") is True

            # When allow_lan is False, non-loopback IPs must be blocked
            config_manager.config_manager.config["allow_lan"] = False
            assert is_client_ip_allowed("192.168.1.74") is False
            assert is_client_ip_allowed("8.8.8.8") is False

            # When allow_lan is True, RFC 1918 private IPs are allowed, public IPs are blocked
            config_manager.config_manager.config["allow_lan"] = True
            assert is_client_ip_allowed("192.168.1.74") is True
            assert is_client_ip_allowed("10.0.0.1") is True
            assert is_client_ip_allowed("172.16.0.5") is True
            assert is_client_ip_allowed("8.8.8.8") is False
            assert is_client_ip_allowed("1.1.1.1") is False
            assert is_client_ip_allowed("203.0.113.1") is False
            # Restore
            config_manager.config_manager.config["allow_lan"] = False
            print("Test 32 - Network ACL & Private Subnet Protection (public/unauthorized IP rejection): OK")

            print("\n[+] ALL 32 TESTS PASSED SUCCESSFULLY!")
    finally:
        gbf_proxy.stop_proxy_thread()

if __name__ == "__main__":
    import os
    asyncio.run(run_test())
    os._exit(0)
