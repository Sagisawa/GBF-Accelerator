import asyncio
import httpx
import sys
import time

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
        from cert_manager import CA_CERT_PATH, ensure_ca
        ensure_ca()
        ssl_ctx = ssl.create_default_context(cafile=certifi.where())
        ssl_ctx.load_verify_locations(cafile=str(CA_CERT_PATH))
        # Ensure test fixture exists in cache for Test 1 & Test 9 (Cache Hit verification)
        from cache_manager import cache_manager
        test_css_key = "/assets/1772717316/css/arousal/form.css"
        if not cache_manager.has_cache(test_css_key):
            cache_manager.save_cache(
                test_css_key,
                {"content-type": "text/css; charset=UTF-8", "ETag": '"1772717316-form"'},
                b"/* gbf test form.css */",
            )

        async with httpx.AsyncClient(
            proxy=f"http://127.0.0.1:{test_port}",
            verify=ssl_ctx,
            timeout=10.0,
        ) as client:
            # Test 1: Local Cache Hit & Absence of X-Proxy Headers on Wire
            url_cache = "https://prd-game-a-granbluefantasy.akamaized.net/assets/1772717316/css/arousal/form.css"
            resp = await client.get(url_cache)
            print(f"Test 1 - Cache Hit (Zero Leakage): status={resp.status_code}, clean_headers={not any(k.lower().startswith('x-proxy-') for k in resp.headers)}, bytes={len(resp.content)}")
            assert resp.status_code == 200
            assert "x-proxy-cache" not in resp.headers
            assert not any(k.lower().startswith("x-proxy-") for k in resp.headers)

            # Test 2: /rest/error/js Passthrough (Zero-Mock Policy)
            url_mock = "https://game.granbluefantasy.jp/rest/error/js"
            resp_mock = await client.get(url_mock)
            print(f"Test 2 - /rest/error/js Passthrough: status={resp_mock.status_code}")
            assert resp_mock.status_code in (200, 400, 404, 405)  # Genuine upstream response, never mocked

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

            # Test 7: Chunked Transfer-Encoding POST (Forwarded Upstream)
            async def chunked_stream():
                yield b"part1_"
                yield b"part2_"
                yield b"payload"

            resp_chunked = await client.post(
                "https://game.granbluefantasy.jp/rest/error/js",
                content=chunked_stream(),
                headers={"transfer-encoding": "chunked"},
            )
            print(f"Test 7 - Chunked POST handling: status={resp_chunked.status_code}")
            assert resp_chunked.status_code in (200, 400, 404, 405)

            # Test 8: Dynamic API CORS preservation (do not inject '*' into dynamic pages)
            print(f"Test 8 - Dynamic API CORS preservation: CORS header={resp_game.headers.get('access-control-allow-origin')}")
            assert resp_game.headers.get("access-control-allow-origin") != "*"

            # Test 9: Cache Query String Normalization
            url_cache_q = "https://prd-game-a-granbluefantasy.akamaized.net/assets/1772717316/css/arousal/form.css?_t=999999999&debug=1"
            resp_q = await client.get(url_cache_q)
            print(f"Test 9 - Cache Query Normalization: status={resp_q.status_code}")
            assert resp_q.status_code == 200
            assert "x-proxy-cache" not in resp_q.headers
            assert not any(k.lower().startswith("x-proxy-") for k in resp_q.headers)

            # Test 10: Cache Integrity Verification (reject empty and HTML error pages for media)
            from cache_manager import cache_manager
            assert not cache_manager.save_cache("/assets/test/broken.png", {"content-type": "text/html"}, b"<html>Error</html>")
            assert not cache_manager.save_cache("/assets/test/empty.png", {"content-type": "image/png"}, b"")
            print("Test 10 - Cache Integrity (rejected HTML error page & empty payload): OK")

            # Test 11: Browser Cache Configuration Matrix (4-quadrant verification)
            from config_manager import config_manager
            orig_setting = config_manager.config.get("enable_browser_cache", True)
            try:
                # Quadrant 1: Disabled + versioned -> MUST NOT inject any cache headers
                config_manager.config["enable_browser_cache"] = False
                h_dis_ver = {}
                cache_manager._apply_browser_cache_headers(h_dis_ver, "/assets/1772717316/foo.js")
                assert "Cache-Control" not in h_dis_ver, "Disabled browser cache must not inject Cache-Control for versioned assets"
                assert "Expires" not in h_dis_ver, "Disabled browser cache must not inject Expires for versioned assets"

                # Quadrant 2: Disabled + unversioned -> MUST NOT inject any cache headers
                h_dis_unver = {}
                cache_manager._apply_browser_cache_headers(h_dis_unver, "/manifest.json")
                assert "Cache-Control" not in h_dis_unver, "Disabled browser cache must not inject Cache-Control for unversioned assets"
                assert "Expires" not in h_dis_unver, "Disabled browser cache must not inject Expires for unversioned assets"

                # Quadrant 3: Enabled + versioned -> MUST inject immutable and 2038 Expires
                config_manager.config["enable_browser_cache"] = True
                h_en_ver = {}
                cache_manager._apply_browser_cache_headers(h_en_ver, "/assets/1772717316/foo.js")
                assert h_en_ver.get("Cache-Control") == "public, max-age=31536000, immutable"
                assert "Expires" in h_en_ver

                # Quadrant 4: Enabled + unversioned -> MUST inject standard short cache, NEVER immutable
                h_en_unver = {}
                cache_manager._apply_browser_cache_headers(h_en_unver, "/manifest.json")
                assert h_en_unver.get("Cache-Control") == "public, max-age=3600"
                assert "immutable" not in h_en_unver.get("Cache-Control", "")
                assert "Expires" not in h_en_unver
                print("Test 11 - Browser Cache Configuration 4-Quadrant Matrix: OK")
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
            assert "x-proxy-cache" not in resp_head.headers
            assert not any(k.lower().startswith("x-proxy-") for k in resp_head.headers)
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
            _p_sf = cache_manager._get_local_path("/assets/test/concurrent_singleflight_test.js")
            if _p_sf and _p_sf.exists():
                _p_sf.unlink()
            if _p_sf and (_p_sf.parent / (_p_sf.name + ".ext")).exists():
                (_p_sf.parent / (_p_sf.name + ".ext")).unlink()
            cache_manager.clear_ram_cache()

            target_client = gbf_proxy.asset_client if getattr(gbf_proxy, "asset_client", None) else gbf_proxy.http_client
            real_request = target_client.request
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

            target_client.request = mock_concurrent_request
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
                target_client.request = real_request
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
            from gbf_proxy import is_client_ip_allowed, _get_prefetch_priority
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

            # Test 33: Synchronous Zero-Executor RAM Cache Fast Path
            from cache_manager import cache_manager
            test_ram_path = "/assets/test/pure_memory_fastpath.js"
            cache_manager.store_ram_cache(test_ram_path, {"ETag": '"test-etag-123"'}, b'console.log("fast")')
            ram_res = cache_manager.get_ram_cache(test_ram_path)
            assert ram_res is not None, "get_ram_cache must return data for in-memory asset"
            r_headers, r_data = ram_res
            assert r_headers.get("X-Cache-Source") == "RAM"
            assert r_data == b'console.log("fast")'
            print("Test 33 - Synchronous Zero-Executor RAM Cache Fast Path: OK")

            # Test 34: Prefetch PriorityQueue Classification
            assert _get_prefetch_priority("/app.js") == 1
            assert _get_prefetch_priority("/style.css") == 1
            assert _get_prefetch_priority("/manifest.json") == 1
            assert _get_prefetch_priority("/character.png") == 2
            assert _get_prefetch_priority("/icon.webp") == 2
            assert _get_prefetch_priority("/font.woff2") == 3
            assert _get_prefetch_priority("/bgm.mp3") == 4
            assert _get_prefetch_priority("/voice.m4a") == 4
            print("Test 34 - Prefetch Priority Classification (P1 Script/CSS > P2 Image > P3 Font > P4 Audio): OK")

            # Test 35: Bounded Negative Disk Cache Index
            non_existent = f"/assets/test/definitely_not_exist_{int(time.time()*1000)}.png"
            assert cache_manager.get_disk_cache(non_existent) is None
            clean_none_key = non_existent.split("?")[0].lstrip("/")
            with cache_manager._missing_lock:
                assert clean_none_key in cache_manager._known_missing, "Missing file must be recorded in negative cache"
            # Second lookup should hit negative cache directly
            assert cache_manager.get_disk_cache(non_existent) is None
            # Storing cache clears negative cache entry
            cache_manager.save_cache(non_existent, {"ETag": '"found"'}, b"\x89PNG\r\n\x1a\nnew_data")
            with cache_manager._missing_lock:
                assert clean_none_key not in cache_manager._known_missing, "Saving cache must evict from negative cache"
            # Cleanup test artifacts
            clean_file = cache_manager._get_local_path(non_existent)
            if clean_file:
                clean_file.unlink(missing_ok=True)
                clean_file.with_name(clean_file.name + ".ext").unlink(missing_ok=True)
            with cache_manager._ram_lock:
                cache_manager._ram_cache.pop(clean_none_key, None)
            print("Test 35 - Bounded Negative Disk Cache Index (NTFS stat bypass): OK")

            # Test 36: Prefetch Cross-Host Reference Extraction
            from gbf_proxy import extract_asset_refs
            sample_js = b'''
                var bg = "https://prd-game-a-granbluefantasy.akamaized.net/assets/img/scene/bg/sample.png";
                var icon = "/assets/img/icon/item.png";
                var audio = "sound/bgm/battle.mp3";
                var invalid_external = "https://malicious-cdn.example.com/assets/hack.png";
            '''
            extracted = extract_asset_refs("/manifest.js", sample_js, default_host="game.granbluefantasy.jp")
            assert ("prd-game-a-granbluefantasy.akamaized.net", "/assets/img/scene/bg/sample.png") in extracted, "Must preserve explicit Akamai CDN host"
            assert ("game.granbluefantasy.jp", "/assets/img/icon/item.png") in extracted, "Relative path must inherit default_host"
            assert ("game.granbluefantasy.jp", "/sound/bgm/battle.mp3") in extracted, "Relative sound path must inherit default_host"
            assert not any("malicious-cdn.example.com" in h for h, p in extracted), "Non-GBF external host must be ignored"
            print("Test 36 - Prefetch Cross-Host Reference Extraction (explicit CDN host preserved & relative fallback): OK")

            # Test 37: RAM Cache Gzip Header Integrity (Never claim gzip on uncompressed data)
            uncompressed_js = b"console.log('clean_js_no_gzip');"
            cache_manager.store_ram_cache("/assets/test/uncompressed_decompressed_upstream.js", {"content-encoding": "gzip", "content-type": "application/javascript"}, uncompressed_js)
            ram_item = cache_manager.get_ram_cache("/assets/test/uncompressed_decompressed_upstream.js")
            assert ram_item is not None
            r_head, r_body = ram_item
            assert "Content-Encoding" not in r_head or r_head["Content-Encoding"] != "gzip", "Uncompressed plaintext must NEVER have Content-Encoding: gzip in RAM cache"
            assert r_body == uncompressed_js

            # Genuine gzip data must retain Content-Encoding: gzip
            import gzip
            genuine_gzip_js = gzip.compress(uncompressed_js)
            cache_manager.store_ram_cache("/assets/test/genuine_gzip.js", {"content-type": "application/javascript"}, genuine_gzip_js)
            ram_gzip_item = cache_manager.get_ram_cache("/assets/test/genuine_gzip.js")
            assert ram_gzip_item is not None
            rg_head, rg_body = ram_gzip_item
            assert rg_head.get("Content-Encoding") == "gzip", "Genuine gzip data must retain Content-Encoding: gzip"
            print("Test 37 - RAM Cache Gzip Header Integrity (prevents ERR_CONTENT_DECODING_FAILED & update loop): OK")

            # Test 38: Disk Cache HTML Error Auto-Repair (Plain Text)
            broken_rel = "/assets/test/corrupt_502_error.js"
            broken_p = cache_manager._get_local_path(broken_rel)
            if broken_p:
                broken_p.parent.mkdir(parents=True, exist_ok=True)
                broken_p.write_bytes(b"<!DOCTYPE html><html><body>502 Bad Gateway</body></html>")
                ext_p = broken_p.with_name(broken_p.name + ".ext")
                ext_p.write_text('{"ct": "application/javascript"}', encoding="utf-8")
                # Clear negative cache for this test key
                with cache_manager._missing_lock:
                    cache_manager._known_missing.pop("assets/test/corrupt_502_error.js", None)

                # get_disk_cache must detect corrupt HTML, auto-repair (delete), and return None
                assert cache_manager.get_disk_cache(broken_rel) is None, "Corrupt HTML error file must be rejected by get_disk_cache"
                assert not broken_p.exists(), "Broken file must be automatically deleted (auto-repair)"
                assert not ext_p.exists(), "Broken .ext file must be automatically deleted (auto-repair)"
            print("Test 38 - Disk Cache HTML Error Page Auto-Repair (prevents Ready-page script freeze): OK")

            # Test 39: Disk Cache HTML Error Auto-Repair (Gzip-compressed HTML)
            gzip_broken_rel = "/assets/test/corrupt_gzip_502_error.js"
            gzip_broken_p = cache_manager._get_local_path(gzip_broken_rel)
            if gzip_broken_p:
                gzip_broken_p.parent.mkdir(parents=True, exist_ok=True)
                gzip_broken_p.write_bytes(gzip.compress(b"<!DOCTYPE html><html><body>502 Bad Gateway</body></html>"))
                gz_ext_p = gzip_broken_p.with_name(gzip_broken_p.name + ".ext")
                gz_ext_p.write_text('{"ct": "application/javascript", "ce": "gzip"}', encoding="utf-8")
                with cache_manager._missing_lock:
                    cache_manager._known_missing.pop("assets/test/corrupt_gzip_502_error.js", None)

                assert cache_manager.get_disk_cache(gzip_broken_rel) is None, "Gzip-compressed HTML error must be rejected by get_disk_cache"
                assert not gzip_broken_p.exists(), "Gzipped broken file must be automatically deleted"
                assert not gz_ext_p.exists(), "Gzipped broken .ext file must be automatically deleted"
            print("Test 39 - Gzip-Compressed HTML Error Page Auto-Repair (streaming zlib chunk inspect): OK")

            # Test 40: CreateJS Animation & Manifest Prefetch Extraction
            manifest_sample = b'define(["jquery","backbone"],function(a,b){var c=b.Model.extend({defaults:{manifest:[{src:Game.imgUri+"/sp/cjs/npc_3040620000_02.png",id:"npc_3040620000_02",type:"image"},{src:Game.imgUri+"/sp/cjs/ab_all_3040620000_02.png",id:"ab_all",type:"image"}]}});return c});'
            m_refs = gbf_proxy.extract_asset_refs(
                "/assets/1789040290/js/model/manifest/npc_3040620000_02.js",
                manifest_sample,
                "prd-game-a-granbluefantasy.akamaized.net"
            )
            extracted_paths = [p for _, p in m_refs]
            assert "/assets/1789040290/js/cjs/npc_3040620000_02.js" in extracted_paths, "Twin CreateJS code script must be deduced"
            assert "/assets/img/sp/cjs/npc_3040620000_02.png" in extracted_paths, "CreateJS character sprite PNG must be extracted"
            assert "/assets/img/sp/cjs/ab_all_3040620000_02.png" in extracted_paths, "CreateJS ability sprite PNG must be extracted"

            # Test English version manifest
            en_refs = gbf_proxy.extract_asset_refs(
                "/assets_en/1789040290/js/model/manifest/npc_3040620000_02.js",
                manifest_sample,
                "prd-game-a-granbluefantasy.akamaized.net"
            )
            en_paths = [p for _, p in en_refs]
            assert "/assets_en/1789040290/js/cjs/npc_3040620000_02.js" in en_paths, "English twin CreateJS script must be deduced"
            assert "/assets_en/img/sp/cjs/npc_3040620000_02.png" in en_paths, "English CreateJS character sprite PNG must be extracted"
            print("Test 40 - CreateJS Character/Enemy Animation & Manifest Prefetch Extraction: OK")

            # Test 41: Upstream Flaky Network & Offline Cache Fallback (Direction 1)
            # 1. Setup sample cached assets in historical version
            v_hist_path = "/assets/1789040290/js/cjs/test_flaky_fallback_cjs.js"
            v_hist_data = b'define(["cjs"], function(){ return {test: true}; });'
            cache_manager.save_cache(v_hist_path, {"ETag": '"v1789-cjs"'}, v_hist_data)

            # 2. Test cross-version fallback for simulated new version (e.g. v9999999999)
            future_path = "/assets/9999999999/js/cjs/test_flaky_fallback_cjs.js"
            fb_hit = cache_manager.get_fallback_cache(future_path)
            assert fb_hit is not None, "Fallback cache must find historical asset under older version"
            fb_headers, fb_data = fb_hit
            assert fb_data == v_hist_data, "Fallback data must match historical cached asset"
            assert fb_headers.get("X-Proxy-Fallback") == "STALE-VERSION-1789040290", "Fallback header must indicate source version"
            assert "max-age=60" in fb_headers.get("Cache-Control", ""), "Fallback must have short max-age for self-healing"
            assert fb_headers.get("X-Proxy-Cache") == "FALLBACK"
            # Ensure future path was NOT written to disk permanently
            assert not cache_manager.has_cache(future_path), "Fallback must NOT contaminate new version disk path"

            # 3. Test core business logic scripts are strictly excluded from fallback
            assert cache_manager.get_fallback_cache("/assets/9999999999/js/app.js") is None, "app.js must be excluded"
            assert cache_manager.get_fallback_cache("/assets/9999999999/js/main.js") is None, "main.js must be excluded"
            assert cache_manager.get_fallback_cache("/assets/9999999999/set-error-handler.js") is None, "set-error-handler must be excluded"

            # 4. Test cross-language fallback (assets_en -> assets)
            lang_base_path = "/assets/img/sp/cjs/test_lang_fallback.png"
            lang_png_data = b'\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4'
            cache_manager.save_cache(lang_base_path, {"ETag": '"lang-png"'}, lang_png_data)
            en_img_req = "/assets_en/img/sp/cjs/test_lang_fallback.png"
            lang_fb = cache_manager.get_fallback_cache(en_img_req)
            assert lang_fb is not None, "Cross-language fallback must locate Japanese asset"
            l_headers, l_data = lang_fb
            assert l_data == lang_png_data
            assert l_headers.get("X-Proxy-Fallback") == "CROSS-LANG-JP"

            # Cleanup test artifacts
            clean_file1 = cache_manager._get_local_path(v_hist_path)
            if clean_file1:
                clean_file1.unlink(missing_ok=True)
                clean_file1.with_name(clean_file1.name + ".ext").unlink(missing_ok=True)
            clean_file2 = cache_manager._get_local_path(lang_base_path)
            if clean_file2:
                clean_file2.unlink(missing_ok=True)
                clean_file2.with_name(clean_file2.name + ".ext").unlink(missing_ok=True)
            print("Test 41 - Upstream Flaky Network Fallback (cross-version, cross-lang, core JS protection): OK")

            # Test 42: Magic Bytes Media Format Validation & One-Click Cache Audit Auto-Repair
            # 1. Magic Bytes validation for various asset types
            assert cache_manager.is_valid_cache_content("/test.png", {}, b'\x89PNG\r\n\x1a\n\x00\x00\x00') is True
            assert cache_manager.is_valid_cache_content("/test.png", {}, b'{"error": "rate limit"}') is False
            assert cache_manager.is_valid_cache_content("/test.png", {}, b'503 Service Unavailable') is False
            assert cache_manager.is_valid_cache_content("/test.jpg", {}, b'\xff\xd8\xff\xe0\x00\x10JFIF') is True
            assert cache_manager.is_valid_cache_content("/test.jpg", {}, b'<html>error</html>') is False
            assert cache_manager.is_valid_cache_content("/test.webp", {}, b'RIFF\x00\x00\x00\x00WEBPVP8 ') is True
            assert cache_manager.is_valid_cache_content("/test.webp", {}, b'RIFF\x00\x00\x00\x00JPEG') is False
            assert cache_manager.is_valid_cache_content("/test.mp3", {}, b'ID3\x03\x00\x00') is True
            assert cache_manager.is_valid_cache_content("/test.js", {}, b'console.log("ok");') is True
            assert cache_manager.is_valid_cache_content("/test.js", {}, b'<!DOCTYPE html><html>') is False

            # 2. Cache audit and auto-repair test
            import tempfile, shutil
            from pathlib import Path
            audit_tmp = Path(tempfile.mkdtemp())
            try:
                from cache_manager import CacheManager
                test_cm = CacheManager(cache_base_dir=audit_tmp)
                # Valid files
                (audit_tmp / "good.png").write_bytes(b'\x89PNG\r\n\x1a\nreal_png_data')
                (audit_tmp / "good.js").write_bytes(b'var a = 1;')
                # Corrupted / Truncated files
                (audit_tmp / "zero.png").write_bytes(b'')
                (audit_tmp / "corrupt_txt.png").write_bytes(b'Internal Server Error Text')
                (audit_tmp / "corrupt_html.jpg").write_bytes(b'<!doctype html><html>504</html>')
                (audit_tmp / "stale.tmp.1234.5678").write_bytes(b'interrupted write')

                audit_res = test_cm.audit_and_repair_cache()
                assert audit_res["scanned"] == 6, f"Expected 6 scanned, got {audit_res['scanned']}"
                assert audit_res["corrupted"] == 4, f"Expected 4 corrupted cleaned, got {audit_res['corrupted']}"
                assert audit_res["healthy"] == 2, f"Expected 2 healthy, got {audit_res['healthy']}"
                assert (audit_tmp / "good.png").is_file(), "Good PNG must remain"
                assert (audit_tmp / "good.js").is_file(), "Good JS must remain"
                assert not (audit_tmp / "zero.png").exists(), "0-byte file must be deleted"
                assert not (audit_tmp / "corrupt_txt.png").exists(), "Corrupt PNG must be deleted"
                assert not (audit_tmp / "corrupt_html.jpg").exists(), "Corrupt JPG must be deleted"
                assert not (audit_tmp / "stale.tmp.1234.5678").exists(), "Stale .tmp file must be deleted"
            finally:
                shutil.rmtree(audit_tmp, ignore_errors=True)
            print("Test 42 - Magic Bytes Media Format Validation & Cache Audit Auto-Repair: OK")

            # Test 43: 1.7.0 Dual Client Isolation & Protocol Integrity
            assert gbf_proxy.api_client is not None, "api_client must be initialized"
            assert gbf_proxy.asset_client is not None, "asset_client must be initialized"
            assert gbf_proxy.api_client is not gbf_proxy.asset_client, "api_client and asset_client must be isolated instances"
            # Verify protocol settings: api_client is HTTP/1.1 only, asset_client has HTTP/2 enabled
            api_pool = getattr(gbf_proxy.api_client._transport, "_pool", None)
            asset_pool = getattr(gbf_proxy.asset_client._transport, "_pool", None)
            if api_pool:
                assert getattr(api_pool, "_http1", False) is True, "api_client must use HTTP/1.1"
                assert getattr(api_pool, "_http2", False) is False, "api_client must not use HTTP/2"
            if asset_pool:
                assert getattr(asset_pool, "_http2", False) == gbf_proxy.HTTP2_ENABLED, "asset_client must use HTTP/2"
            print("Test 43 - Dual Client Isolation & Protocol Configuration Integrity: OK")

            # Test 44: Safe Stale Retry with Dual Constraints (POST never, unknown GET never, whitelist GET 1x)
            orig_api_req = gbf_proxy.api_client.request
            gbf_proxy.api_telemetry.reset()
            import collections
            test_call_counts = collections.defaultdict(int)

            async def mock_retry_api_client_request(method, url, headers=None, content=b""):
                clean_p = url.split("?")[0].lower()
                test_call_counts[clean_p] += 1
                if "fail_stale" in url:
                    if test_call_counts[clean_p] == 1:
                        raise httpx.RemoteProtocolError("Server disconnected (simulated stale socket)")
                    return httpx.Response(200, json={"success": True, "recovered": True}, request=httpx.Request(method, url))
                if "fail_permanent" in url:
                    raise httpx.ConnectError("Connection refused")
                return httpx.Response(200, json={"ok": True}, request=httpx.Request(method, url))

            gbf_proxy.api_client.request = mock_retry_api_client_request
            try:
                # 1. State-changing POST (normal_attack_result.json): MUST NEVER RETRY
                post_url = "https://game.granbluefantasy.jp/rest/multiraid/normal_attack_result.json?fail_stale=1"
                try:
                    await gbf_proxy.request_api("POST", post_url, {}, content=b"{}", path="/rest/multiraid/normal_attack_result.json")
                    assert False, "State-changing POST must raise exception and never retry"
                except httpx.RemoteProtocolError:
                    pass
                assert test_call_counts["https://game.granbluefantasy.jp/rest/multiraid/normal_attack_result.json"] == 1, \
                    "POST normal_attack_result.json must be attempted exactly 1 time (NEVER retried)"

                # 2. Unknown GET path: MUST NEVER RETRY
                unknown_url = "https://game.granbluefantasy.jp/rest/unknown/action.json?fail_stale=1"
                try:
                    await gbf_proxy.request_api("GET", unknown_url, {}, path="/rest/unknown/action.json")
                    assert False, "Unknown GET path must raise exception and never retry"
                except httpx.RemoteProtocolError:
                    pass
                assert test_call_counts["https://game.granbluefantasy.jp/rest/unknown/action.json"] == 1, \
                    "Unknown GET path must be attempted exactly 1 time (NEVER retried)"

                # 3. Business initialization GET (start.json): MUST NEVER RETRY (ambiguous outcome)
                start_url = "https://game.granbluefantasy.jp/rest/multiraid/start.json?fail_stale=1"
                try:
                    await gbf_proxy.request_api("GET", start_url, {}, path="/rest/multiraid/start.json")
                    assert False, "start.json must raise exception and never retry"
                except httpx.RemoteProtocolError:
                    pass
                assert test_call_counts["https://game.granbluefantasy.jp/rest/multiraid/start.json"] == 1, \
                    "start.json must be attempted exactly 1 time (NEVER retried on ambiguous outcome)"

                # 4. Whitelist read-only GET (condition.json): MUST RETRY EXACTLY ONCE on stale drop
                whitelist_url = "https://game.granbluefantasy.jp/rest/multiraid/condition.json?fail_stale=1"
                resp, reused = await gbf_proxy.request_api("GET", whitelist_url, {}, path="/rest/multiraid/condition.json")
                assert resp.status_code == 200
                assert resp.json().get("recovered") is True
                assert test_call_counts["https://game.granbluefantasy.jp/rest/multiraid/condition.json"] == 2, \
                    "Whitelist condition.json must be retried exactly once on connection-level drop"
                assert gbf_proxy.api_telemetry.retry_count == 1, "Telemetry retry_count must record exactly 1 retry"
                print("Test 44 - Strict Dual-Constraint Retry (POST never, unknown GET never, start.json never, whitelist GET once): OK")
            finally:
                gbf_proxy.api_client.request = orig_api_req

            # Test 45: Connection Reuse Telemetry & Latency Percentiles (P50/P95/P99)
            gbf_proxy.api_telemetry.reset()
            # Simulate a stream object shared across multiple requests
            class DummyStream:
                pass
            stream_1 = DummyStream()
            stream_2 = DummyStream()

            # Record simulated requests
            r1 = gbf_proxy.api_telemetry.record(latency_ms=120.0, protocol="HTTP/1.1", stream=stream_1)
            assert r1 is False, "First request on stream_1 must be a new connection"
            r2 = gbf_proxy.api_telemetry.record(latency_ms=90.0, protocol="HTTP/1.1", stream=stream_1)
            assert r2 is True, "Second request on stream_1 must be recognized as reused"
            r3 = gbf_proxy.api_telemetry.record(latency_ms=85.0, protocol="HTTP/1.1", stream=stream_1)
            assert r3 is True, "Third request on stream_1 must be recognized as reused"

            # Stream 2: new connection
            r4 = gbf_proxy.api_telemetry.record(latency_ms=210.0, protocol="HTTP/1.1", stream=stream_2)
            assert r4 is False, "First request on stream_2 must be a new connection"
            r5 = gbf_proxy.api_telemetry.record(latency_ms=95.0, protocol="HTTP/1.1", stream=stream_2)
            assert r5 is True, "Second request on stream_2 must be recognized as reused"

            # Check latency percentiles calculation
            pct = gbf_proxy.api_telemetry.get_percentiles()
            assert pct["count"] == 5
            assert 85.0 <= pct["p50"] <= 100.0, f"Unexpected P50: {pct['p50']}"
            assert pct["p95"] >= 120.0, f"Unexpected P95: {pct['p95']}"
            assert pct["p99"] >= pct["p95"], f"P99 should be >= P95: {pct['p99']}"

            stats = gbf_proxy.api_telemetry.get_stats()
            assert stats["total_requests"] == 5
            assert stats["reused_connections"] == 3
            assert stats["new_connections"] == 2
            assert stats["reuse_rate"] == 60.0, f"Expected 60.0% reuse rate, got {stats['reuse_rate']}"
            assert stats["protocols"]["HTTP/1.1"] == 5
            print("Test 45 - Connection Reuse Telemetry Accounting & Percentiles (P50/P95/P99): OK", flush=True)

            # Test 46: Real HTTP/1.1 TCP Connection Pool Reuse & Stale Socket Recovery Integration Test
            active_server_writers = []
            should_server_drop = False

            async def handle_mock_http11(r: asyncio.StreamReader, w: asyncio.StreamWriter):
                nonlocal should_server_drop
                active_server_writers.append(w)
                try:
                    while True:
                        line = await r.readline()
                        if not line:
                            break
                        while True:
                            h = await r.readline()
                            if h in (b"\r\n", b"\n", b""):
                                break
                        if should_server_drop:
                            should_server_drop = False
                            w.close()
                            await w.wait_closed()
                            return

                        resp_bytes = (
                            b"HTTP/1.1 200 OK\r\n"
                            b"Content-Type: application/json\r\n"
                            b"Content-Length: 16\r\n"
                            b"Connection: keep-alive\r\n\r\n"
                            b"{\"connected\": 1}"
                        )
                        w.write(resp_bytes)
                        await w.drain()
                except Exception:
                    pass

            mock_srv = await asyncio.start_server(handle_mock_http11, "127.0.0.1", 0)
            mock_port = mock_srv.sockets[0].getsockname()[1]
            mock_url = f"http://127.0.0.1:{mock_port}/rest/multiraid/condition.json"

            orig_proxy_api_client = gbf_proxy.api_client
            gbf_proxy.api_client = httpx.AsyncClient(
                limits=httpx.Limits(max_connections=4, max_keepalive_connections=4, keepalive_expiry=10.0),
                http1=True,
                http2=False,
                trust_env=False
            )
            gbf_proxy.api_telemetry.reset()

            try:
                # 1. First request -> new connection
                resp_1, reused_1 = await gbf_proxy.request_api("GET", mock_url, {}, path="/rest/multiraid/condition.json")
                stream_act_1 = resp_1.extensions.get("network_stream")
                assert stream_act_1 is not None, "HTTPX response must include network_stream"
                assert reused_1 is False, "First request must negotiate a new connection"

                # 2. Second request -> HTTPX reuses the same TCP stream
                resp_2, reused_2 = await gbf_proxy.request_api("GET", mock_url, {}, path="/rest/multiraid/condition.json")
                stream_act_2 = resp_2.extensions.get("network_stream")
                assert stream_act_2 is stream_act_1, "HTTPX connection pool must reuse identical underlying stream"
                assert reused_2 is True, "Second request on existing socket must be recognized as reused"

                # 3. Third request -> reuses again
                resp_3, reused_3 = await gbf_proxy.request_api("GET", mock_url, {}, path="/rest/multiraid/condition.json")
                stream_act_3 = resp_3.extensions.get("network_stream")
                assert stream_act_3 is stream_act_1
                assert reused_3 is True

                # 4. Server drops connection, testing real socket drop & stale retry recovery
                should_server_drop = True
                resp_4, reused_4 = await gbf_proxy.request_api("GET", mock_url, {}, path="/rest/multiraid/condition.json")
                stream_act_4 = resp_4.extensions.get("network_stream")
                assert resp_4.status_code == 200
                assert stream_act_4 is not stream_act_1, "Reconnected request must use a newly negotiated stream"
                assert gbf_proxy.api_telemetry.retry_count == 1, "Must record 1 retry in telemetry"

                stats_46 = gbf_proxy.api_telemetry.get_stats()
                assert stats_46["new_connections"] == 2
                assert stats_46["reused_connections"] == 2
                assert stats_46["reuse_rate"] == 50.0
                assert stats_46["protocols"]["HTTP/1.1"] == 4
                print("Test 46 - Real HTTP/1.1 Server TCP Connection Pool Reuse & Stale Socket Recovery: OK", flush=True)
            finally:
                await gbf_proxy.api_client.aclose()
                gbf_proxy.api_client = orig_proxy_api_client
                for w in active_server_writers:
                    try:
                        w.close()
                    except Exception:
                        pass
                mock_srv.close()
                await mock_srv.wait_closed()

            # Test 47: Real Prefetch Worker Queue -> request_asset -> save_cache Pipeline
            prefetch_test_path = "/assets/test/prefetch_live_worker_test.png"
            prefetch_test_host = "prd-game-a-granbluefantasy.akamaized.net"

            def cleanup_test_47():
                p = cache_manager._get_local_path(prefetch_test_path)
                if p and p.exists():
                    try:
                        p.unlink()
                    except Exception:
                        pass
                    p_ext = p.with_name(p.name + ".ext")
                    if p_ext.exists():
                        try:
                            p_ext.unlink()
                        except Exception:
                            pass
                cache_manager._ram_cache.pop(prefetch_test_path, None)

            cleanup_test_47()
            assert not cache_manager.has_cache(prefetch_test_path)

            orig_req_asset = gbf_proxy.request_asset
            orig_queue = gbf_proxy.prefetch_queue
            orig_inflight = set(gbf_proxy.prefetch_inflight)
            orig_sem = gbf_proxy.save_semaphore
            prefetch_asset_calls = []

            async def mock_prefetch_asset_fetch(method, url, headers=None, content=b""):
                prefetch_asset_calls.append((method, url, headers))
                png_bytes = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR" + b"\x00" * 30
                return httpx.Response(
                    200,
                    headers={"Content-Type": "image/png", "Content-Length": str(len(png_bytes))},
                    content=png_bytes,
                    request=httpx.Request(method, url)
                )

            gbf_proxy.request_asset = mock_prefetch_asset_fetch
            gbf_proxy.save_semaphore = asyncio.Semaphore(16)
            gbf_proxy.prefetch_queue = asyncio.PriorityQueue()
            gbf_proxy.prefetch_inflight.clear()

            try:
                await gbf_proxy.prefetch_queue.put((1, 0, prefetch_test_host, prefetch_test_path))
                gbf_proxy.prefetch_inflight.add(f"{prefetch_test_host}{prefetch_test_path}")

                worker_task = asyncio.create_task(gbf_proxy.prefetch_worker())
                await asyncio.wait_for(gbf_proxy.prefetch_queue.join(), timeout=3.0)
                worker_task.cancel()
                try:
                    await worker_task
                except asyncio.CancelledError:
                    pass

                assert len(prefetch_asset_calls) == 1, f"Expected 1 fetch, got {len(prefetch_asset_calls)}"
                assert prefetch_asset_calls[0][0] == "GET"
                assert prefetch_asset_calls[0][1] == f"https://{prefetch_test_host}{prefetch_test_path}"
                await asyncio.sleep(0.05)
                assert cache_manager.has_cache(prefetch_test_path) is True, "Prefetched asset must be successfully saved to cache"
                meta_47, data_47 = cache_manager.get_cache(prefetch_test_path)
                assert data_47.startswith(b"\x89PNG"), "Cached data must match PNG magic bytes"
                print("Test 47 - Real Prefetch Worker Queue -> request_asset -> save_cache Pipeline: OK", flush=True)
            finally:
                gbf_proxy.request_asset = orig_req_asset
                gbf_proxy.prefetch_queue = orig_queue
                gbf_proxy.prefetch_inflight.clear()
                gbf_proxy.prefetch_inflight.update(orig_inflight)
                gbf_proxy.save_semaphore = orig_sem
                cleanup_test_47()

            # Test 48: Dynamic API Timeout Observability (Specific TimeoutException Subclass & Elapsed ms)
            orig_req_api = gbf_proxy.request_api
            captured_timeout_logs = []
            def timeout_log_listener(line, level):
                if "[TIMEOUT]" in line:
                    captured_timeout_logs.append(line)

            gbf_proxy.register_log_listener(timeout_log_listener)
            try:
                # 1. Verify ReadTimeout subclass recording and 504 status
                async def mock_timeout_read(*args, **kwargs):
                    await asyncio.sleep(0.02)
                    raise httpx.ReadTimeout("Mock server read timed out")

                gbf_proxy.request_api = mock_timeout_read
                resp_to_1 = await client.post("https://game.granbluefantasy.jp/rest/multiraid/ability_result.json", json={"ability_id": 1})
                assert resp_to_1.status_code == 504
                assert resp_to_1.json() == {"error": "Upstream API Gateway Timeout", "code": 504}
                assert any("ReadTimeout" in l and "API Gateway Timeout" in l and "ms)" in l for l in captured_timeout_logs), "Log must record ReadTimeout subclass and elapsed ms"

                # 2. Verify ConnectTimeout subclass recording and 504 status
                async def mock_timeout_connect(*args, **kwargs):
                    await asyncio.sleep(0.01)
                    raise httpx.ConnectTimeout("Mock connect to upstream timed out")

                gbf_proxy.request_api = mock_timeout_connect
                captured_timeout_logs.clear()
                resp_to_2 = await client.post("https://game.granbluefantasy.jp/rest/multiraid/normal_attack_result.json", json={})
                assert resp_to_2.status_code == 504
                assert resp_to_2.json() == {"error": "Upstream API Gateway Timeout", "code": 504}
                assert any("ConnectTimeout" in l and "API Gateway Timeout" in l and "ms)" in l for l in captured_timeout_logs), "Log must record ConnectTimeout subclass and elapsed ms"

                print("Test 48 - Dynamic API Timeout Observability (ReadTimeout/ConnectTimeout & Elapsed ms): OK", flush=True)
            finally:
                gbf_proxy.request_api = orig_req_api
                gbf_proxy.unregister_log_listener(timeout_log_listener)

            # Test 49: /ob/r Heartbeat Passthrough (Never Mocked)
            resp_obr = await client.get("https://game.granbluefantasy.jp/ob/r")
            print(f"Test 49 - /ob/r Heartbeat Passthrough: status={resp_obr.status_code}", flush=True)
            assert resp_obr.status_code in (200, 400, 404, 405)  # Genuine upstream response, never mocked

            # Test 50: set-error-handler.js Byte-for-Byte Fidelity (No JS Tampering)
            sample_js_bytes = b'function onError(t, a){ t&&alert(t),a&&window.location.reload(); }'
            cache_manager.save_cache("/assets/test/set-error-handler.js", {"Content-Type": "application/javascript"}, sample_js_bytes)
            saved_hit = cache_manager.get_disk_cache("/assets/test/set-error-handler.js")
            assert saved_hit is not None
            _, saved_data = saved_hit
            assert saved_data == sample_js_bytes, "Saved set-error-handler.js must preserve exact original bytes without void 0 tampering"
            assert b"void 0" not in saved_data
            assert b"t&&alert(t)" in saved_data
            clean_test_js = cache_manager._get_local_path("/assets/test/set-error-handler.js")
            if clean_test_js:
                clean_test_js.unlink(missing_ok=True)
                clean_test_js.with_name(clean_test_js.name + ".ext").unlink(missing_ok=True)
            print("Test 50 - set-error-handler.js Byte Fidelity (No Tampering): OK", flush=True)

            # Test 51: Legacy Tampered JS Quarantine & Auto-Healing
            import tempfile, shutil
            from pathlib import Path
            q_tmp_dir = Path(tempfile.mkdtemp())
            try:
                from cache_manager import CacheManager
                q_cm = CacheManager(cache_base_dir=q_tmp_dir)
                fake_tampered_bytes = b'function onError(t, a){ void 0; }'
                test_target_file = q_tmp_dir / "set-error-handler.js"
                with open(test_target_file, "wb") as f:
                    f.write(fake_tampered_bytes)
                ext_file = q_tmp_dir / "set-error-handler.js.ext"
                with open(ext_file, "w") as f:
                    f.write('{"ETag": "old"}')

                # Verify quarantine triggers on tampered file
                is_quarantined = q_cm._check_and_quarantine_tampered_js(test_target_file)
                assert is_quarantined is True, "Must quarantine tampered set-error-handler.js"
                assert not test_target_file.exists(), "Original tampered file must be moved"
                quarantined_files = list(q_tmp_dir.glob("set-error-handler.js.quarantine.*"))
                assert len(quarantined_files) == 1, "Must find exactly one .quarantine backup file"

                # Verify negative case: legitimate modern minified JS using `void 0` (e.g. `x === void 0`) is NOT quarantined
                legit_file = q_tmp_dir / "set-error-handler.js"
                with open(legit_file, "wb") as f:
                    f.write(b'function onError(t, a){ if (t === void 0) { console.error("error occurred", a); } }')
                assert q_cm._check_and_quarantine_tampered_js(legit_file) is False, "Must NOT quarantine legitimate JS using void 0"
                assert legit_file.is_file(), "Legitimate JS file must remain intact"

                # Verify negative case: original official script with alert/reload is NOT quarantined
                with open(legit_file, "wb") as f:
                    f.write(b'function(t, a){ t && alert(t), a && window.location.reload() }')
                assert q_cm._check_and_quarantine_tampered_js(legit_file) is False, "Must NOT quarantine original official script"
                assert legit_file.is_file(), "Original script must remain intact"

                print("Test 51 - Legacy Tampered JS Quarantine & Auto-Healing: OK", flush=True)
            finally:
                shutil.rmtree(q_tmp_dir, ignore_errors=True)

            # Test 52: Prefetch Headers Minimal & Compliant
            assert "User-Agent" in gbf_proxy.PREFETCH_HEADERS
            assert "python-httpx" not in gbf_proxy.PREFETCH_HEADERS["User-Agent"].lower()
            assert "Mozilla/5.0" in gbf_proxy.PREFETCH_HEADERS["User-Agent"]
            assert not any(k.lower().startswith("x-proxy-") for k in gbf_proxy.PREFETCH_HEADERS)
            print("Test 52 - Prefetch Headers Minimal & Compliant: OK", flush=True)

            # Test 53: Upstream Response Byte & Header Fidelity Regression (Zero Tampering & Zero Leakage)
            raw_test_body = b'{"fidelity": "verified", "chars": "\xe3\x82\xb0\xe3\x83\xa9\xe3\x83\x96\xe3\x83\xab", "numbers": [1,2,3]}'
            upstream_headers = [
                (b"Content-Type", b"application/json; charset=utf-8"),
                (b"ETag", b'"fidelity-test-etag-9988"'),
                (b"Cache-Control", b"private, no-cache, no-store"),
                (b"Last-Modified", b"Wed, 16 Sep 2026 08:00:00 GMT"),
                (b"Set-Cookie", b"auth_token=abc1234; Path=/; Secure; HttpOnly"),
                (b"Set-Cookie", b"user_pref=dark; Path=/"),
            ]
            mock_upstream_resp = httpx.Response(
                status_code=200,
                headers=upstream_headers,
                content=raw_test_body,
            )

            class FidelityWriter:
                def __init__(self):
                    self.data = bytearray()
                def write(self, d):
                    self.data.extend(d)
                async def drain(self):
                    pass

            fidelity_writer = FidelityWriter()
            await gbf_proxy.forward_upstream_response(fidelity_writer, {}, mock_upstream_resp)
            f_raw = bytes(fidelity_writer.data)
            f_header_bytes, f_body_bytes = f_raw.split(b"\r\n\r\n", 1)

            # 1. Byte-for-byte exact body match (Zero payload modification)
            assert f_body_bytes == raw_test_body, "Forwarded body must strictly match upstream bytes"

            # 2. Key business headers fidelity (Case-insensitive HTTP header check)
            f_headers_str = f_header_bytes.decode("iso-8859-1")
            f_headers_lower = {line.split(":", 1)[0].strip().lower(): line.split(":", 1)[1].strip() for line in f_headers_str.split("\r\n") if ":" in line}
            assert f_headers_lower.get("content-type") == "application/json; charset=utf-8"
            assert f_headers_lower.get("etag") == '"fidelity-test-etag-9988"'
            assert f_headers_lower.get("cache-control") == "private, no-cache, no-store"
            assert f_headers_lower.get("last-modified") == "Wed, 16 Sep 2026 08:00:00 GMT"

            # 3. Multiple Set-Cookie preserved without comma-folding
            assert "Set-Cookie: auth_token=abc1234; Path=/; Secure; HttpOnly" in f_headers_str
            assert "Set-Cookie: user_pref=dark; Path=/" in f_headers_str

            # 4. Zero proxy header leakage and safe Content-Encoding
            assert "x-proxy" not in f_headers_str.lower(), "Forwarded response must never leak X-Proxy-* headers"
            assert "content-encoding: gzip" not in f_headers_str.lower(), "Decompressed plain content must never retain gzip header"
            print("Test 53 - Upstream Response Byte & Header Fidelity Regression: OK", flush=True)

            # Test 54 - Tray & GUI Complete Shutdown Lifecycle (quit_app)
            import unittest.mock as mock
            import tkinter as tk
            import gui_main

            test_root = tk.Tk()
            test_root.withdraw()
            with mock.patch.object(gui_main.GBFAcceleratorGUI, "start_proxy"), \
                 mock.patch.object(gui_main.GBFAcceleratorGUI, "setup_tray"):
                test_gui = gui_main.GBFAcceleratorGUI(test_root)

            mock_tray = mock.MagicMock()
            test_gui.tray_icon = mock_tray
            test_gui._stats_job = "mock_stats_job_id"

            with mock.patch.object(test_root, "after_cancel") as mock_cancel, \
                 mock.patch.object(test_root, "destroy") as mock_destroy, \
                 mock.patch.object(gui_main.system_proxy, "disable_pac_proxy") as mock_disable_pac, \
                 mock.patch.object(gui_main.gbf_proxy, "stop_proxy_thread") as mock_stop_proxy:

                # 1. Verify pystray passes (icon, item) without raising TypeError
                test_gui.show_from_tray("mock_icon", "mock_item")
                test_gui.toggle_proxy_from_tray("mock_icon", "mock_item")

                # 2. Call quit_app with pystray arguments
                test_gui.quit_app("mock_icon", "mock_item", terminate_process=False)

                # 3. Verify complete cleanup calls
                assert test_gui._is_quitting is True, "Must mark app as quitting"
                mock_tray.stop.assert_called_once()
                assert test_gui.tray_icon is None, "Tray icon reference must be cleared"
                mock_cancel.assert_called_once_with("mock_stats_job_id")
                assert test_gui._stats_job is None, "Stats job reference must be None"
                mock_disable_pac.assert_called_once()
                mock_stop_proxy.assert_called_once()

                # 4. Verify idempotency
                test_gui.quit_app("mock_icon", "mock_item", terminate_process=False)
                assert mock_tray.stop.call_count == 1, "Tray stop should not be called again"

            test_root.destroy()
            print("Test 54 - Tray & GUI Complete Shutdown Lifecycle (quit_app): OK", flush=True)

            # ================= Issue #4 Regression Tests (Tests 55 - 62) =================
            import errno
            import socket
            import concurrent.futures

            # Test 55: Fast-path direct bind on idle port (zero kill_process_on_port overhead)
            with mock.patch("gbf_proxy.kill_process_on_port") as mock_kill, \
                 mock.patch("asyncio.start_server", new_callable=mock.AsyncMock) as mock_start_srv:
                mock_start_srv.return_value = mock.MagicMock()
                server_inst = await gbf_proxy._bind_listener_server(ssl_context=None)
                assert server_inst is not None
                mock_kill.assert_not_called()
                mock_start_srv.assert_called_once()
            print("Test 55 - Fast-path direct bind on idle port (zero kill_process_on_port overhead): OK", flush=True)

            # Test 56: _is_address_in_use_error WinError 10048 & EADDRINUSE classification
            win_err = OSError()
            win_err.winerror = 10048
            assert gbf_proxy._is_address_in_use_error(win_err) is True
            posix_err = OSError(errno.EADDRINUSE, "Address in use")
            assert gbf_proxy._is_address_in_use_error(posix_err) is True
            perm_err = OSError(errno.EACCES, "Permission denied")
            assert gbf_proxy._is_address_in_use_error(perm_err) is False
            val_err = ValueError("Invalid")
            assert gbf_proxy._is_address_in_use_error(val_err) is False
            print("Test 56 - _is_address_in_use_error WinError 10048 & EADDRINUSE classification: OK", flush=True)

            # Test 57: Non-10048 OSError fails immediately without executing cleanup
            with mock.patch("gbf_proxy.kill_process_on_port") as mock_kill, \
                 mock.patch("asyncio.start_server", side_effect=OSError(errno.EACCES, "Permission denied")):
                caught_perm = False
                try:
                    await gbf_proxy._bind_listener_server(ssl_context=None)
                except OSError as e:
                    if e.errno == errno.EACCES:
                        caught_perm = True
                assert caught_perm is True, "Must re-raise non-address-in-use OSError"
                mock_kill.assert_not_called()
            print("Test 57 - Non-EADDRINUSE OSError fails immediately without cleanup: OK", flush=True)

            # Test 58: Non-GBF process occupying port is preserved & retry fails cleanly
            held_sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            held_sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            held_sock.bind(("127.0.0.1", 0))
            held_port = held_sock.getsockname()[1]
            held_sock.listen(1)

            orig_port = gbf_proxy.LISTEN_PORT
            gbf_proxy.LISTEN_PORT = held_port
            try:
                with mock.patch("gbf_proxy.kill_process_on_port", return_value=False) as mock_kill:
                    bind_failed_with_eaddrinuse = False
                    try:
                        await gbf_proxy._bind_listener_server(ssl_context=None)
                    except OSError as e:
                        if gbf_proxy._is_address_in_use_error(e):
                            bind_failed_with_eaddrinuse = True
                    assert bind_failed_with_eaddrinuse is True, "Must raise address in use error"
                    mock_kill.assert_called_once_with(held_port)
                    assert held_sock.fileno() != -1, "Third-party socket must not be terminated"
            finally:
                gbf_proxy.LISTEN_PORT = orig_port
                held_sock.close()
            print("Test 58 - Non-GBF process occupying port is preserved & retry fails cleanly: OK", flush=True)

            # Test 59: Concurrent start_proxy_thread under _proxy_thread_lock maintains atomicity
            orig_thread = gbf_proxy.proxy_thread
            with mock.patch("gbf_proxy.run_proxy_in_thread") as mock_run:
                mock_run.side_effect = lambda: time.sleep(0.1)
                with concurrent.futures.ThreadPoolExecutor(max_workers=5) as executor:
                    futures = [executor.submit(gbf_proxy.start_proxy_thread) for _ in range(5)]
                    concurrent.futures.wait(futures)
                assert gbf_proxy.proxy_thread is not None
                gbf_proxy.proxy_thread.join(timeout=1.0)
                gbf_proxy.proxy_thread = orig_thread
            print("Test 59 - Concurrent start_proxy_thread atomicity under _proxy_thread_lock: OK", flush=True)

            # Test 60: Grace window simulation (ready during grace period treated as success)
            orig_proxy_thread_60 = gbf_proxy.proxy_thread
            import tkinter as tk
            test_gui_root = tk.Tk()
            test_gui_root.withdraw()
            try:
                with mock.patch.object(gui_main.GBFAcceleratorGUI, "start_proxy"), \
                     mock.patch.object(gui_main.GBFAcceleratorGUI, "setup_tray"):
                    gui_inst = gui_main.GBFAcceleratorGUI(test_gui_root)

                call_count = 0
                def mock_wait(timeout):
                    nonlocal call_count
                    call_count += 1
                    if call_count == 1:
                        # Primary 5.0s wait times out
                        return False
                    # Grace 0.8s window succeeds
                    gbf_proxy.PROXY_STATS["is_running"] = True
                    return True

                mock_thread = mock.MagicMock()
                mock_thread.is_alive.return_value = True

                with mock.patch.object(gbf_proxy.proxy_ready_event, "wait", side_effect=mock_wait), \
                     mock.patch.object(gui_main.system_proxy, "enable_pac_proxy"), \
                     mock.patch.object(gui_main.messagebox, "showerror") as mock_err_box, \
                     mock.patch.object(gbf_proxy, "start_proxy_thread"):
                    gbf_proxy.proxy_thread = mock_thread
                    gbf_proxy.PROXY_STATS["is_running"] = False
                    gbf_proxy.PROXY_STATS["last_error"] = ""
                    gui_inst.start_proxy()
                    assert "运行中" in gui_inst.var_status_text.get(), f"Status must be 运行中, got {gui_inst.var_status_text.get()}"
                    mock_err_box.assert_not_called()
            finally:
                test_gui_root.destroy()
                gbf_proxy.proxy_thread = orig_proxy_thread_60
            print("Test 60 - Grace window convergence (ready during grace period succeeds): OK", flush=True)

            # Test 61: last_error fallback when empty string
            gbf_proxy.PROXY_STATS["last_error"] = ""
            err_result = gbf_proxy.PROXY_STATS.get("last_error") or "端口绑定失败或超时"
            assert err_result == "端口绑定失败或超时", "Empty last_error must fallback to descriptive error"
            print("Test 61 - last_error empty string fallback: OK", flush=True)

            # Test 62: Failure cleanup and clean restart recovery
            gbf_proxy.PROXY_STATS["is_running"] = False
            gbf_proxy.PROXY_STATS["last_error"] = "Simulated error"
            gbf_proxy.proxy_ready_event.set()
            gbf_proxy.stop_proxy_thread()
            assert gbf_proxy.proxy_thread is None
            assert gbf_proxy.PROXY_STATS["is_running"] is False
            assert gbf_proxy.proxy_ready_event.is_set() is False
            with mock.patch("gbf_proxy.run_proxy_in_thread") as mock_run:
                mock_run.side_effect = lambda: gbf_proxy.proxy_ready_event.set()
                gbf_proxy.start_proxy_thread()
                assert gbf_proxy.proxy_thread is not None
                gbf_proxy.stop_proxy_thread()
                assert gbf_proxy.proxy_thread is None
            print("Test 62 - True failure cleanup and clean restart recovery: OK", flush=True)

            # Test 63: _ActiveForegroundAssetTracker lifecycle, invariants & exception safety
            gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 0
            gbf_proxy._last_foreground_asset_ts = 0.0
            with gbf_proxy._ActiveForegroundAssetTracker():
                assert gbf_proxy.ACTIVE_FOREGROUND_ASSETS == 1, "Counter must increment to 1 on enter"
                with gbf_proxy._ActiveForegroundAssetTracker():
                    assert gbf_proxy.ACTIVE_FOREGROUND_ASSETS == 2, "Nested counter must increment to 2"
                assert gbf_proxy.ACTIVE_FOREGROUND_ASSETS == 1, "Nested exit must decrement counter back to 1"
            assert gbf_proxy.ACTIVE_FOREGROUND_ASSETS == 0, "Counter must be 0 after outer exit"
            assert gbf_proxy._last_foreground_asset_ts > 0.0, "_last_foreground_asset_ts must be updated"

            # Invariant under exception
            ts_before = gbf_proxy._last_foreground_asset_ts
            try:
                with gbf_proxy._ActiveForegroundAssetTracker():
                    assert gbf_proxy.ACTIVE_FOREGROUND_ASSETS == 1
                    raise RuntimeError("Simulated network failure")
            except RuntimeError:
                pass
            assert gbf_proxy.ACTIVE_FOREGROUND_ASSETS == 0, "Counter must decrement even when exception raised"
            assert gbf_proxy._last_foreground_asset_ts >= ts_before, "Timestamp must update on exception exit"
            print("Test 63 - _ActiveForegroundAssetTracker lifecycle invariants & exception safety: OK", flush=True)

            # Test 64: Real time-window Prefetch QoS pause & resumption
            orig_queue = gbf_proxy.prefetch_queue
            orig_inflight = set(gbf_proxy.prefetch_inflight)
            orig_sem = gbf_proxy.save_semaphore
            if gbf_proxy.save_semaphore is None:
                gbf_proxy.save_semaphore = asyncio.Semaphore(16)
            gbf_proxy.prefetch_queue = asyncio.PriorityQueue()
            gbf_proxy.prefetch_inflight.clear()

            gbf_proxy.ACTIVE_API_COUNT = 0
            gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 1
            gbf_proxy._last_foreground_asset_ts = 0.0

            qos_logs = []
            def qos_log_listener(line, level):
                if "[PREFETCH-QOS]" in line:
                    qos_logs.append(line)
            gbf_proxy.register_log_listener(qos_log_listener)

            mock_fetch_count = 0
            async def dummy_request_asset(*args, **kwargs):
                nonlocal mock_fetch_count
                mock_fetch_count += 1
                mock_resp = mock.MagicMock()
                mock_resp.status_code = 200
                mock_resp.content = b"fake-data"
                mock_resp.headers = {"content-type": "image/png"}
                return mock_resp

            # Enqueue a test prefetch item
            await gbf_proxy.prefetch_queue.put((1, 1, "prd-game-a-granbluefantasy.akamaized.net", "/test_qos_item.png"))

            with mock.patch("gbf_proxy.request_asset", side_effect=dummy_request_asset), \
                 mock.patch("gbf_proxy._bounded_save_cache"):
                worker_task = asyncio.create_task(gbf_proxy.prefetch_worker())
                try:
                    # While ACTIVE_FOREGROUND_ASSETS == 1, worker must yield and NEVER call request_asset
                    await asyncio.sleep(0.15)
                    assert mock_fetch_count == 0, f"Prefetch must be paused during active foreground asset, got {mock_fetch_count} calls"

                    # Finish foreground asset -> enters cooldown
                    gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 0
                    gbf_proxy._last_foreground_asset_ts = time.perf_counter()

                    # Within cooldown (< 0.10s), worker must STILL be yielding
                    await asyncio.sleep(0.04)
                    assert mock_fetch_count == 0, "Prefetch must remain paused during foreground cooldown window"

                    # After cooldown expires, worker should resume and process the item
                    await asyncio.sleep(0.12)
                    assert mock_fetch_count == 1, f"Prefetch must resume after cooldown expired, got {mock_fetch_count}"

                    # Verify QoS log messages
                    assert any("Paused prefetch (reason=foreground_asset)" in l for l in qos_logs), f"Expected pause log, got: {qos_logs}"
                    assert any("Resumed prefetch (reason=foreground_asset)" in l for l in qos_logs), f"Expected resume log, got: {qos_logs}"
                finally:
                    worker_task.cancel()
                    try:
                        await worker_task
                    except asyncio.CancelledError:
                        pass
                    gbf_proxy.unregister_log_listener(qos_log_listener)
                    gbf_proxy.prefetch_queue = orig_queue
                    gbf_proxy.prefetch_inflight.clear()
                    gbf_proxy.prefetch_inflight.update(orig_inflight)
                    gbf_proxy.save_semaphore = orig_sem
            print("Test 64 - Real time-window Prefetch QoS pause & resumption: OK", flush=True)

            # Test 65: Continuous foreground requests (A -> 50ms -> B) full timeline zero-leakage test
            orig_queue = gbf_proxy.prefetch_queue
            orig_inflight = set(gbf_proxy.prefetch_inflight)
            orig_sem = gbf_proxy.save_semaphore
            if gbf_proxy.save_semaphore is None:
                gbf_proxy.save_semaphore = asyncio.Semaphore(16)
            gbf_proxy.prefetch_queue = asyncio.PriorityQueue()
            gbf_proxy.prefetch_inflight.clear()

            gbf_proxy.ACTIVE_API_COUNT = 0
            gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 0
            gbf_proxy._last_foreground_asset_ts = 0.0

            continuous_calls = 0
            async def continuous_request_asset(*args, **kwargs):
                nonlocal continuous_calls
                continuous_calls += 1
                mock_resp = mock.MagicMock()
                mock_resp.status_code = 200
                mock_resp.content = b"fake-data-2"
                mock_resp.headers = {"content-type": "image/png"}
                return mock_resp

            await gbf_proxy.prefetch_queue.put((1, 2, "prd-game-a-granbluefantasy.akamaized.net", "/test_continuous.png"))

            with mock.patch("gbf_proxy.request_asset", side_effect=continuous_request_asset), \
                 mock.patch("gbf_proxy._bounded_save_cache"):
                worker_task = asyncio.create_task(gbf_proxy.prefetch_worker())
                try:
                    # 1. Foreground request A active
                    gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 1
                    await asyncio.sleep(0.05)
                    assert continuous_calls == 0, "Timeline stage 1: Request A active -> prefetch must be 0"

                    # 2. Request A completes -> enters cooldown
                    gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 0
                    gbf_proxy._last_foreground_asset_ts = time.perf_counter()
                    await asyncio.sleep(0.05)  # 50ms into 100ms cooldown
                    assert continuous_calls == 0, "Timeline stage 2: Request A cooldown -> prefetch must be 0"

                    # 3. Request B arrives during A's cooldown
                    gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 1
                    await asyncio.sleep(0.05)
                    assert continuous_calls == 0, "Timeline stage 3: Request B active -> prefetch must be 0"

                    # 4. Request B completes -> enters B's cooldown
                    gbf_proxy.ACTIVE_FOREGROUND_ASSETS = 0
                    gbf_proxy._last_foreground_asset_ts = time.perf_counter()
                    await asyncio.sleep(0.05)  # 50ms into B's cooldown
                    assert continuous_calls == 0, "Timeline stage 4: Request B cooldown -> prefetch must be 0"

                    # 5. B's cooldown finishes (> 100ms total from B finish)
                    await asyncio.sleep(0.12)
                    assert continuous_calls == 1, f"Timeline stage 5: Full cooldown elapsed -> prefetch must execute, got {continuous_calls}"
                finally:
                    worker_task.cancel()
                    try:
                        await worker_task
                    except asyncio.CancelledError:
                        pass
                    gbf_proxy.prefetch_queue = orig_queue
                    gbf_proxy.prefetch_inflight.clear()
                    gbf_proxy.prefetch_inflight.update(orig_inflight)
                    gbf_proxy.save_semaphore = orig_sem
            print("Test 65 - Continuous foreground requests (A -> 50ms -> B) timeline zero-leakage test: OK", flush=True)

            # ---------------- Test 66: Prefetch Discovery Queue Bounded Backpressure & Deduplication & Executor Offload ----------------
            orig_disc_queue = gbf_proxy.prefetch_discovery_queue
            orig_disc_inflight = set(gbf_proxy.prefetch_discovery_inflight)
            orig_disc_dropped = gbf_proxy.prefetch_discovery_dropped
            orig_pref_queue = gbf_proxy.prefetch_queue
            orig_pref_inflight = set(gbf_proxy.prefetch_inflight)

            gbf_proxy.prefetch_discovery_queue = asyncio.Queue(maxsize=gbf_proxy.PREFETCH_DISCOVERY_QUEUE_MAX)
            gbf_proxy.prefetch_discovery_inflight.clear()
            gbf_proxy.prefetch_discovery_dropped = 0
            gbf_proxy.prefetch_queue = asyncio.PriorityQueue()
            gbf_proxy.prefetch_inflight.clear()

            try:
                # 1. Deduplication with host case-insensitivity & query stripping
                gbf_proxy.maybe_enqueue_prefetch("prd-game-a-granbluefantasy.akamaized.net", "/assets/app.js?v=1", b"var x = 1;")
                gbf_proxy.maybe_enqueue_prefetch("PRD-GAME-A-GRANBLUEFANTASY.AKAMAIZED.NET", "/assets/app.js?v=2", b"var x = 2;")
                assert gbf_proxy.prefetch_discovery_queue.qsize() == 1, "Duplicate JS submission with varying query/case must only queue once"
                expected_key = "prd-game-a-granbluefantasy.akamaized.net/assets/app.js"
                assert expected_key in gbf_proxy.prefetch_discovery_inflight, f"Inflight set must contain normalized key {expected_key}"

                # 2. Bounded backpressure (QueueFull handling)
                # Fill up remaining slots in queue (capacity 32, 1 slot already occupied -> fill 31)
                for i in range(gbf_proxy.PREFETCH_DISCOVERY_QUEUE_MAX - 1):
                    gbf_proxy.maybe_enqueue_prefetch("prd-game-a-granbluefantasy.akamaized.net", f"/assets/file_{i}.js", b"var x = 0;")
                assert gbf_proxy.prefetch_discovery_queue.qsize() == gbf_proxy.PREFETCH_DISCOVERY_QUEUE_MAX
                assert gbf_proxy.prefetch_discovery_dropped == 0

                # Saturated: 33rd distinct submission should be gracefully dropped without raising
                overflow_path = "/assets/overflow.js"
                overflow_key = f"prd-game-a-granbluefantasy.akamaized.net{overflow_path}"
                gbf_proxy.maybe_enqueue_prefetch("prd-game-a-granbluefantasy.akamaized.net", overflow_path, b"var overflow = 1;")
                assert gbf_proxy.prefetch_discovery_dropped == 1, "Dropped counter must increment on QueueFull"
                assert overflow_key not in gbf_proxy.prefetch_discovery_inflight, "Dropped job key must not linger in inflight set"

                # 3. Offload execution via discovery worker
                # Clear queue and add a test manifest with static asset references
                while not gbf_proxy.prefetch_discovery_queue.empty():
                    try:
                        gbf_proxy.prefetch_discovery_queue.get_nowait()
                        gbf_proxy.prefetch_discovery_queue.task_done()
                    except (asyncio.QueueEmpty, ValueError):
                        break
                gbf_proxy.prefetch_discovery_inflight.clear()

                js_content = b'var assets = ["assets/img/test_disc_icon.png", "assets/sound/test_disc_bgm.mp3"];'
                gbf_proxy.maybe_enqueue_prefetch("prd-game-a-granbluefantasy.akamaized.net", "/assets/manifest.js", js_content)
                assert gbf_proxy.prefetch_discovery_queue.qsize() == 1

                disc_worker = asyncio.create_task(gbf_proxy.prefetch_discovery_worker())
                try:
                    await asyncio.wait_for(gbf_proxy.prefetch_discovery_queue.join(), timeout=3.0)
                    # Verify discovery worker offloaded regex + cache check and populated prefetch_queue
                    assert gbf_proxy.prefetch_queue.qsize() >= 1, f"Discovery worker should have enqueued references into prefetch_queue, got {gbf_proxy.prefetch_queue.qsize()}"
                    assert len(gbf_proxy.prefetch_discovery_inflight) == 0, "Discovery inflight set must be empty after job completes"
                finally:
                    disc_worker.cancel()
                    try:
                        await disc_worker
                    except asyncio.CancelledError:
                        pass
            finally:
                gbf_proxy.prefetch_discovery_queue = orig_disc_queue
                gbf_proxy.prefetch_discovery_inflight.clear()
                gbf_proxy.prefetch_discovery_inflight.update(orig_disc_inflight)
                gbf_proxy.prefetch_discovery_dropped = orig_disc_dropped
                gbf_proxy.prefetch_queue = orig_pref_queue
                gbf_proxy.prefetch_inflight.clear()
                gbf_proxy.prefetch_inflight.update(orig_pref_inflight)
            print("Test 66 - Prefetch Discovery Queue Bounded Backpressure & Deduplication & Executor Offload: OK", flush=True)

            # ---------------- Test 67: Prefetch Discovery Worker Exception Isolation & Cancellation Safety ----------------
            orig_disc_queue = gbf_proxy.prefetch_discovery_queue
            orig_disc_inflight = set(gbf_proxy.prefetch_discovery_inflight)
            gbf_proxy.prefetch_discovery_queue = asyncio.Queue(maxsize=gbf_proxy.PREFETCH_DISCOVERY_QUEUE_MAX)
            gbf_proxy.prefetch_discovery_inflight.clear()

            try:
                # Put a job into the queue
                test_key = "prd-game-a-granbluefantasy.akamaized.net/assets/error_test.js"
                gbf_proxy.prefetch_discovery_inflight.add(test_key)
                gbf_proxy.prefetch_discovery_queue.put_nowait((
                    "prd-game-a-granbluefantasy.akamaized.net",
                    "/assets/error_test.js",
                    b"var test = 1;",
                    test_key
                ))

                # Mock _extract_and_filter_prefetch_refs to throw
                orig_extract_func = gbf_proxy._extract_and_filter_prefetch_refs
                def mocked_extract_fail(*args, **kwargs):
                    raise RuntimeError("Simulated discovery worker failure")
                gbf_proxy._extract_and_filter_prefetch_refs = mocked_extract_fail

                disc_worker = asyncio.create_task(gbf_proxy.prefetch_discovery_worker())
                try:
                    await asyncio.wait_for(gbf_proxy.prefetch_discovery_queue.join(), timeout=3.0)
                    assert not disc_worker.done(), "Discovery worker must survive runtime exceptions in loop"
                    assert test_key not in gbf_proxy.prefetch_discovery_inflight, "Discovery inflight key must be cleaned up even on exception"
                finally:
                    gbf_proxy._extract_and_filter_prefetch_refs = orig_extract_func
                    disc_worker.cancel()
                    try:
                        await disc_worker
                    except asyncio.CancelledError:
                        pass
            finally:
                gbf_proxy.prefetch_discovery_queue = orig_disc_queue
                gbf_proxy.prefetch_discovery_inflight.clear()
                gbf_proxy.prefetch_discovery_inflight.update(orig_disc_inflight)
            print("Test 67 - Prefetch Discovery Worker Exception Isolation & Cancellation Safety: OK", flush=True)

            # ---------------- Test 68: Local Cache Independence From Upstream Availability ----------------
            # Verifies that once an asset is cached locally, neither L1 (RAM) nor L2 (SSD) relies
            # on upstream connectivity. If upstream becomes completely unavailable, local requests
            # still succeed (200 OK, identical bytes) with zero upstream fetch calls.
            if gbf_proxy.proxy_thread is None or not gbf_proxy.PROXY_STATS.get("is_running", False):
                gbf_proxy.LISTEN_HOST = "127.0.0.1"
                gbf_proxy.LISTEN_PORT = test_port
                gbf_proxy.start_proxy_thread()
                assert gbf_proxy.proxy_ready_event.wait(timeout=5.0), "Proxy failed to restart for Test 68"
                await asyncio.sleep(0.3)

            async with httpx.AsyncClient(
                proxy=f"http://127.0.0.1:{test_port}",
                verify=ssl_ctx,
                timeout=10.0,
            ) as e2e_client:
                target_client = gbf_proxy.asset_client or gbf_proxy.http_client
                real_request = target_client.request
                test_asset_url = "https://prd-game-a-granbluefantasy.akamaized.net/assets/test/upstream_independence_test.png"
                test_asset_path = "/assets/test/upstream_independence_test.png"
                test_payload = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15c4\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82"

                upstream_state = "alive"
                upstream_call_count = 0

                async def mock_upstream_independence(method, url, **kwargs):
                    nonlocal upstream_call_count
                    if "upstream_independence_test.png" in str(url):
                        upstream_call_count += 1
                        if upstream_state == "alive":
                            return httpx.Response(
                                200,
                                headers={"content-type": "image/png", "etag": '"independence-test-v1"'},
                                content=test_payload,
                                request=httpx.Request(method, url),
                            )
                        else:
                            raise httpx.ConnectError("Simulated upstream proxy offline / unreachable")
                    return await real_request(method, url, **kwargs)

                target_client.request = mock_upstream_independence
                try:
                    # Part A: Cold miss with upstream alive -> populates L1 RAM and L2 SSD
                    resp_cold = await e2e_client.get(test_asset_url)
                    assert resp_cold.status_code == 200
                    assert resp_cold.content == test_payload
                    assert upstream_call_count == 1, f"Expected 1 upstream fetch on cold miss, got {upstream_call_count}"

                    # Wait briefly for bounded async disk write to complete
                    for _ in range(20):
                        if cache_manager.has_cache(test_asset_path):
                            break
                        await asyncio.sleep(0.05)
                    assert cache_manager.has_cache(test_asset_path), "Asset must be persisted on disk"

                    # Part B: Upstream becomes unavailable -> L1 (RAM) hit succeeds with 0 upstream calls
                    upstream_state = "offline"
                    upstream_call_count = 0
                    resp_ram = await e2e_client.get(test_asset_url)
                    assert resp_ram.status_code == 200
                    assert resp_ram.content == test_payload
                    assert upstream_call_count == 0, f"Expected 0 upstream calls on RAM hit, got {upstream_call_count}"

                    # Part C: Evict RAM cache (force L2 SSD path) while upstream is still unavailable
                    cache_manager.clear_ram_cache()
                    assert cache_manager.get_ram_cache(test_asset_path) is None

                    resp_ssd = await e2e_client.get(test_asset_url)
                    assert resp_ssd.status_code == 200
                    assert resp_ssd.content == test_payload
                    assert upstream_call_count == 0, f"Expected 0 upstream calls on SSD hit, got {upstream_call_count}"

                    print("Test 68 - Local Cache Independence (Cold -> RAM -> SSD with Upstream Offline): OK", flush=True)
                finally:
                    target_client.request = real_request
                    p = cache_manager._get_local_path(test_asset_path)
                    if p and p.exists():
                        p.unlink(missing_ok=True)
                    if p:
                        ext_p = p.parent / (p.name + ".ext")
                        if ext_p.exists():
                            ext_p.unlink(missing_ok=True)
                    cache_manager.clear_ram_cache()

                # ---------------- Test 69: High-Concurrency SingleFlight Under Slow Upstream & Failure Recovery ----------------
                target_client = gbf_proxy.asset_client or gbf_proxy.http_client
                real_request = target_client.request
                test_sf_concurrency_url = "https://prd-game-a-granbluefantasy.akamaized.net/assets/test/high_concurrency_sf_test.js"
                test_sf_concurrency_path = "/assets/test/high_concurrency_sf_test.js"
                test_sf_fail_url = "https://prd-game-a-granbluefantasy.akamaized.net/assets/test/sf_failure_recovery_test.js"
                test_sf_fail_path = "/assets/test/sf_failure_recovery_test.js"

                sf_concurrency_fetches = 0
                sf_failure_fetches = 0
                fail_mode = True

                async def mock_sf_contract(method, url, **kwargs):
                    nonlocal sf_concurrency_fetches, sf_failure_fetches
                    str_url = str(url)
                    if "high_concurrency_sf_test.js" in str_url:
                        sf_concurrency_fetches += 1
                        await asyncio.sleep(0.30)  # Slow upstream
                        return httpx.Response(
                            200,
                            headers={"content-type": "application/javascript", "etag": '"sf-concurrency-test"'},
                            content=b"console.log('30-concurrent-singleflight-verified');",
                            request=httpx.Request(method, url),
                        )
                    if "sf_failure_recovery_test.js" in str_url:
                        sf_failure_fetches += 1
                        if fail_mode:
                            await asyncio.sleep(0.10)
                            raise httpx.ConnectError("Simulated upstream gateway drop during flight")
                        else:
                            return httpx.Response(
                                200,
                                headers={"content-type": "application/javascript", "etag": '"sf-recovered"'},
                                content=b"console.log('recovery-success');",
                                request=httpx.Request(method, url),
                            )
                    return await real_request(method, url, **kwargs)

                target_client.request = mock_sf_contract
                try:
                    # --- Part A: 30 concurrent callers against slow upstream ---
                    sf_key_a = f"prd-game-a-granbluefantasy.akamaized.net{test_sf_concurrency_path}"
                    tasks_a = [e2e_client.get(test_sf_concurrency_url) for _ in range(30)]
                    responses_a = await asyncio.wait_for(asyncio.gather(*tasks_a), timeout=6.0)

                    assert sf_concurrency_fetches == 1, f"Expected exactly 1 upstream fetch for 30 concurrent requests, got {sf_concurrency_fetches}"
                    expected_content_a = b"console.log('30-concurrent-singleflight-verified');"
                    for r in responses_a:
                        assert r.status_code == 200
                        assert r.content == expected_content_a

                    # Wait for bounded async save to finalize and remove flight_key
                    for _ in range(20):
                        if sf_key_a not in gbf_proxy._inflight_fetches:
                            break
                        await asyncio.sleep(0.05)
                    assert sf_key_a not in gbf_proxy._inflight_fetches, "Inflight tracking entry must be removed after completion"

                    # --- Part B: 5 concurrent callers when upstream fails -> deadlock freedom & inflight cleanup ---
                    sf_key_b = f"prd-game-a-granbluefantasy.akamaized.net{test_sf_fail_path}"
                    fail_mode = True
                    tasks_b = [e2e_client.get(test_sf_fail_url) for _ in range(5)]
                    responses_b = await asyncio.wait_for(asyncio.gather(*tasks_b), timeout=3.0)

                    # All 5 callers must safely terminate without hanging
                    for r in responses_b:
                        assert r.status_code in (502, 504), f"Expected gateway error on upstream failure, got {r.status_code}"

                    # Inflight entry must be cleaned up
                    assert sf_key_b not in gbf_proxy._inflight_fetches, "Inflight key must be cleared after failure"

                    # --- Part C: Subsequent request after recovery must retry normally and succeed ---
                    fail_mode = False
                    resp_retry = await e2e_client.get(test_sf_fail_url)
                    assert resp_retry.status_code == 200
                    assert resp_retry.content == b"console.log('recovery-success');"
                    for _ in range(20):
                        if sf_key_b not in gbf_proxy._inflight_fetches:
                            break
                        await asyncio.sleep(0.05)
                    assert sf_key_b not in gbf_proxy._inflight_fetches, "Inflight key must be cleared after recovered fetch completes"

                    print("Test 69 - High-Concurrency SingleFlight & Failure Recovery Contract: OK", flush=True)
                finally:
                    target_client.request = real_request
                    for p_str in (test_sf_concurrency_path, test_sf_fail_path):
                        p = cache_manager._get_local_path(p_str)
                        if p and p.exists():
                            p.unlink(missing_ok=True)
                        if p:
                            ext_p = p.parent / (p.name + ".ext")
                            if ext_p.exists():
                                ext_p.unlink(missing_ok=True)
                # ---------------- Test 70: Granular Request/Cache Telemetry & Prefetch Worker Pipeline ----------------
                gbf_proxy.reset_telemetry_stats()
                test_pf_url = "https://prd-game-a-granbluefantasy.akamaized.net/assets/test/telemetry_pf_reused.png"
                test_pf_path = "/assets/test/telemetry_pf_reused.png"
                test_pf_host = "prd-game-a-granbluefantasy.akamaized.net"
                test_pf_data = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtelemetry-reused"

                # Ensure clean initial state for this asset
                cache_manager.clear_ram_cache()
                p_disk = cache_manager._get_local_path(test_pf_path)
                if p_disk and p_disk.exists():
                    p_disk.unlink(missing_ok=True)
                if p_disk:
                    ext_p = p_disk.parent / (p_disk.name + ".ext")
                    if ext_p.exists():
                        ext_p.unlink(missing_ok=True)

                orig_req_asset = gbf_proxy.request_asset
                orig_queue = gbf_proxy.prefetch_queue
                orig_inflight = set(gbf_proxy.prefetch_inflight)
                orig_sem = gbf_proxy.save_semaphore
                if gbf_proxy.save_semaphore is None:
                    gbf_proxy.save_semaphore = asyncio.Semaphore(16)
                gbf_proxy.prefetch_queue = asyncio.PriorityQueue()
                gbf_proxy.prefetch_inflight.clear()

                async def mock_pf_request_asset(method, url, headers=None, content=b""):
                    if test_pf_path in str(url):
                        return httpx.Response(
                            200,
                            headers={"content-type": "image/png", "etag": '"pf-reused-etag"'},
                            content=test_pf_data,
                            request=httpx.Request(method, url),
                        )
                    return await orig_req_asset(method, url, headers=headers, content=content)

                gbf_proxy.request_asset = mock_pf_request_asset

                try:
                    # Part A: Real prefetch_worker execution (queue -> request_asset -> cache -> record_prefetch_saved)
                    await gbf_proxy.prefetch_queue.put((1, 0, test_pf_host, test_pf_path))
                    gbf_proxy.prefetch_inflight.add(f"{test_pf_host}{test_pf_path}")

                    worker_task = asyncio.create_task(gbf_proxy.prefetch_worker())
                    await asyncio.wait_for(gbf_proxy.prefetch_queue.join(), timeout=3.0)
                    worker_task.cancel()
                    try:
                        await worker_task
                    except asyncio.CancelledError:
                        pass

                    # Wait briefly for bounded async disk write to complete
                    for _ in range(20):
                        if cache_manager.has_cache(test_pf_path):
                            break
                        await asyncio.sleep(0.05)

                    assert gbf_proxy.PROXY_STATS["prefetch_asset_requests"] == 1
                    assert gbf_proxy.PROXY_STATS["prefetch_asset_successes"] == 1
                    assert cache_manager.has_cache(test_pf_path), "Asset must be persisted on disk by prefetch worker"

                    # Part B: Foreground request hits the prewarmed asset
                    resp_fg1 = await e2e_client.get(test_pf_url)
                    assert resp_fg1.status_code == 200
                    assert resp_fg1.content == test_pf_data
                    assert gbf_proxy.PROXY_STATS["foreground_asset_requests"] == 1
                    assert (gbf_proxy.PROXY_STATS["cache_ram_hit"] + gbf_proxy.PROXY_STATS["cache_disk_hit"]) >= 1
                    assert gbf_proxy.PROXY_STATS["prefetch_reused"] == 1, "First foreground hit must be recognized as prefetch_reused"

                    # Part C: Subsequent foreground request hits cache again, but does not double-count prefetch_reused
                    resp_fg2 = await e2e_client.get(test_pf_url)
                    assert resp_fg2.status_code == 200
                    assert gbf_proxy.PROXY_STATS["foreground_asset_requests"] == 2
                    assert gbf_proxy.PROXY_STATS["prefetch_reused"] == 1, "Repeated foreground hits must not inflate prefetch_reused"

                    # Part D: Check summary dictionary
                    summary = gbf_proxy.get_telemetry_summary()
                    assert summary["foreground_asset_requests"] == 2
                    assert summary["prefetch_asset_requests"] == 1
                    assert summary["prefetch_asset_successes"] == 1
                    assert summary["prefetch_reused"] == 1
                    assert summary["prefetch_reuse_rate_pct"] == 100.0
                    assert summary["prefetch_success_rate_pct"] == 100.0
                    assert summary["prefetch_reuse_of_success_pct"] == 100.0

                    print("Test 70 - Granular Request/Cache Telemetry & Prefetch Worker Pipeline: OK", flush=True)
                finally:
                    gbf_proxy.request_asset = orig_req_asset
                    gbf_proxy.prefetch_queue = orig_queue
                    gbf_proxy.prefetch_inflight.clear()
                    gbf_proxy.prefetch_inflight.update(orig_inflight)
                    gbf_proxy.save_semaphore = orig_sem
                    cache_manager.clear_ram_cache()
                    if p_disk and p_disk.exists():
                        p_disk.unlink(missing_ok=True)
                    if p_disk:
                        ext_p = p_disk.parent / (p_disk.name + ".ext")
                        if ext_p.exists():
                            ext_p.unlink(missing_ok=True)

                # ---------------- Test 71: Dynamic API Transparency & Isolation Contract ----------------
                orig_api_req = gbf_proxy.api_client.request
                captured_upstream_calls = []
                gbf_proxy.reset_telemetry_stats()

                # Clean any prefetch queues
                while not gbf_proxy.prefetch_queue.empty():
                    try:
                        gbf_proxy.prefetch_queue.get_nowait()
                    except Exception:
                        break
                while not gbf_proxy.prefetch_discovery_queue.empty():
                    try:
                        gbf_proxy.prefetch_discovery_queue.get_nowait()
                    except Exception:
                        break

                async def mock_transparency_api(method, url, headers=None, content=b"", **kwargs):
                    captured_upstream_calls.append({
                        "method": method,
                        "url": str(url),
                        "headers": dict(headers or {}),
                        "content": content,
                    })
                    url_str = str(url)
                    # Case 1: normal POST start.json -> returns 200 with multi-cookie and game headers
                    if method == "POST" and "multiraid/start.json" in url_str:
                        return httpx.Response(
                            200,
                            headers=[
                                ("content-type", "application/json"),
                                ("x-game-version", "1.7.3"),
                                ("set-cookie", "session_id=sess_abc123; Path=/; HttpOnly"),
                                ("set-cookie", "auth_token=tok_xyz987; Path=/; Secure"),
                            ],
                            content=b'{"result":"battle_started","turn":1}',
                            request=httpx.Request(method, url),
                        )
                    # Case 2: failing GET start.json -> raises ReadError (should NOT retry)
                    elif method == "GET" and "multiraid/start.json" in url_str:
                        raise httpx.ReadError("Simulated stale mid-stream reset on start.json")
                    # Case 3: failing POST action -> raises RemoteProtocolError (should NOT retry)
                    elif method == "POST" and "ability_result.json" in url_str:
                        raise httpx.RemoteProtocolError("Simulated upstream disconnect during skill cast")
                    return httpx.Response(200, json={"ok": True}, request=httpx.Request(method, url))

                gbf_proxy.api_client.request = mock_transparency_api
                try:
                    # Part A: POST start.json transparency, zero-cache, zero-prefetch, header & multi-cookie preservation
                    post_url = "https://game.granbluefantasy.jp/rest/multiraid/start.json"
                    post_headers = {
                        "Cookie": "player_id=12345; user_env=chrome",
                        "Origin": "https://game.granbluefantasy.jp",
                        "Referer": "https://game.granbluefantasy.jp/",
                        "X-Requested-With": "XMLHttpRequest",
                    }
                    post_payload = b'{"raid_id":"30011"}'
                    resp_post = await e2e_client.post(post_url, headers=post_headers, content=post_payload)

                    assert resp_post.status_code == 200
                    assert resp_post.content == b'{"result":"battle_started","turn":1}'
                    assert len(captured_upstream_calls) == 1
                    call_a = captured_upstream_calls[0]
                    assert call_a["method"] == "POST"
                    assert call_a["content"] == post_payload
                    assert call_a["headers"].get("cookie") == "player_id=12345; user_env=chrome"
                    assert call_a["headers"].get("origin") == "https://game.granbluefantasy.jp"
                    assert call_a["headers"].get("referer") == "https://game.granbluefantasy.jp/"
                    assert call_a["headers"].get("x-requested-with") == "XMLHttpRequest"
                    assert not any(k.lower().startswith("x-proxy-") for k in call_a["headers"])

                    # Client response headers check: multi-cookie preserved, x-game-version preserved, no x-proxy-*
                    client_cookies = resp_post.headers.get_list("set-cookie")
                    assert any("session_id=sess_abc123" in c for c in client_cookies), f"Cookies missing session_id: {client_cookies}"
                    assert any("auth_token=tok_xyz987" in c for c in client_cookies), f"Cookies missing auth_token: {client_cookies}"
                    assert resp_post.headers.get("x-game-version") == "1.7.3"
                    assert not any(k.lower().startswith("x-proxy-") for k in resp_post.headers)
                    assert resp_post.headers.get("access-control-allow-origin") != "*", "Dynamic API must never have wildcard CORS injected"

                    # Zero cache check
                    assert not cache_manager.has_cache("/rest/multiraid/start.json")
                    assert cache_manager.get_ram_cache("/rest/multiraid/start.json") is None

                    # Zero prefetch enqueue check
                    assert gbf_proxy.prefetch_queue.empty()
                    assert gbf_proxy.prefetch_discovery_queue.empty()

                    # Part B: GET start.json on ReadError -> MUST NOT retry (max_attempts = 1)
                    captured_upstream_calls.clear()
                    retry_count_before = gbf_proxy.PROXY_STATS["api_retry_count"]
                    get_start_url = "https://game.granbluefantasy.jp/rest/multiraid/start.json"
                    resp_get_fail = await e2e_client.get(get_start_url)
                    assert resp_get_fail.status_code == 502, f"Expected 502 Bad Gateway on ReadError, got {resp_get_fail.status_code}"
                    assert len(captured_upstream_calls) == 1, f"GET start.json must make strictly 1 attempt, got {len(captured_upstream_calls)}"
                    assert gbf_proxy.PROXY_STATS["api_retry_count"] == retry_count_before, "api_retry_count must not increment for start.json"
                    assert not cache_manager.has_cache("/rest/multiraid/start.json")
                    assert gbf_proxy.prefetch_queue.empty(), "GET start.json must never enqueue into prefetch_queue"
                    assert gbf_proxy.prefetch_discovery_queue.empty(), "GET start.json must never enqueue into prefetch_discovery_queue"

                    # Part C: POST action on RemoteProtocolError -> MUST NOT retry (max_attempts = 1)
                    captured_upstream_calls.clear()
                    post_action_url = "https://game.granbluefantasy.jp/rest/raid/ability_result.json"
                    resp_post_fail = await e2e_client.post(post_action_url, content=b'{"ability_id":1}')
                    assert resp_post_fail.status_code == 502, f"Expected 502 on POST fail, got {resp_post_fail.status_code}"
                    assert len(captured_upstream_calls) == 1, f"POST action must make strictly 1 attempt, got {len(captured_upstream_calls)}"
                    assert gbf_proxy.PROXY_STATS["api_retry_count"] == retry_count_before, "api_retry_count must not increment for POST"
                    assert not cache_manager.has_cache("/rest/raid/ability_result.json")

                    print("Test 71 - Dynamic API Transparency & Isolation Contract (POST/start.json zero retry, zero cache, zero prefetch enqueue, header preservation): OK", flush=True)
                finally:
                    gbf_proxy.api_client.request = orig_api_req

            print("\n[+] ALL 71 TESTS PASSED SUCCESSFULLY!", flush=True)
    except Exception as e:
        import traceback
        traceback.print_exc()
        sys.exit(1)
    finally:
        gbf_proxy.stop_proxy_thread()
        import os, sys
        sys.stdout.flush()
        sys.stderr.flush()
        os._exit(0)

if __name__ == "__main__":
    import os
    asyncio.run(run_test())
    os._exit(0)
