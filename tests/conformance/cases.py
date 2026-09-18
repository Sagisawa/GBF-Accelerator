"""
Cross-Engine Wire Behavior Contract Test Cases (35 pure black-box contracts).
Interacts ONLY via standard HTTP/HTTPS/SSE client requests using httpx.
DO NOT import internal proxy modules (gbf_proxy, cache_manager, etc.) here.
"""

import asyncio
import dataclasses
import socket
import ssl
import time
from typing import Any, Callable, Dict, List, Optional
import httpx

from tests.conformance.mock_upstream import MockUpstreamServer


def _get_local_lan_ip() -> Optional[str]:
    """Retrieve host LAN IPv4 address without importing proxy internals."""
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as s:
            s.connect(("8.8.8.8", 80))
            return s.getsockname()[0]
    except Exception:
        return None


@dataclasses.dataclass
class ConformanceContext:
    proxy_port: int
    control_port: int
    client: httpx.AsyncClient
    control_client: httpx.AsyncClient
    direct_client: httpx.AsyncClient
    mock_upstream: MockUpstreamServer
    ca_ssl_context: ssl.SSLContext


@dataclasses.dataclass
class TestCase:
    name: str
    category: str
    description: str
    func: Callable[[ConformanceContext], Any]


# ==============================================================================
# P0 业务语义透明 & 响应头零污染 (P0_SECURITY_INTEGRITY)
# ==============================================================================

async def case_01_cache_hit_zero_headers(ctx: ConformanceContext):
    """Static asset cache hit must have zero X-Proxy-* and X-Cache-* headers on the wire."""
    path = f"/assets/test/conformance_asset_01_{time.time_ns()}.css"
    content = b"/* asset 01 */\n.btn { display: none; }"
    ctx.mock_upstream.set_scenario(path, "ok", content=content, content_type="text/css")
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"

    # Prime cache (miss -> fetch from mock upstream)
    r1 = await ctx.client.get(url)
    assert r1.status_code == 200, f"Expected 200 on cache prime, got {r1.status_code}"
    assert r1.content == content, "Prime cache content mismatch"

    # Second fetch: cache hit
    r2 = await ctx.client.get(url)
    assert r2.status_code == 200, f"Expected 200 on cache hit, got {r2.status_code}"
    assert r2.content == content, "Cache hit content mismatch"

    for header in r2.headers:
        h_lower = header.lower()
        assert not h_lower.startswith("x-proxy-"), f"Disallowed proxy header on wire: {header}"
        assert not h_lower.startswith("x-cache-"), f"Disallowed cache header on wire: {header}"


async def case_02_dynamic_api_passthrough(ctx: ConformanceContext):
    """Dynamic API requests pass through upstream with zero local mocks and clean headers."""
    path = "/rest/error/js"
    url = f"https://game.granbluefantasy.jp{path}"
    ctx.mock_upstream.reset_counts()

    resp = await ctx.client.get(url)
    assert resp.status_code == 200, f"Dynamic API relay failed: {resp.status_code}"
    assert resp.json() == {"result": "ok"}
    assert ctx.mock_upstream.get_request_count("GET", path) >= 1, "Dynamic API did not reach upstream"

    for header in resp.headers:
        assert not header.lower().startswith("x-proxy-"), f"Disallowed proxy header leak: {header}"
        assert not header.lower().startswith("x-cache-"), f"Disallowed cache header leak: {header}"


async def case_03_heartbeat_passthrough(ctx: ConformanceContext):
    """Anti-cheat & heartbeat endpoint /ob/r must penetrate upstream without local interference."""
    path = "/ob/r"
    url = f"https://game.granbluefantasy.jp{path}"
    ctx.mock_upstream.reset_counts()

    resp = await ctx.client.post(url, content=b'{"hb": 1}')
    assert resp.status_code == 200, f"Heartbeat did not pass through: {resp.status_code}"
    assert resp.json().get("ob") is True, f"Heartbeat response mismatch: {resp.text}"
    assert ctx.mock_upstream.get_request_count("POST", path) >= 1, "Heartbeat was intercepted locally"

    for header in resp.headers:
        assert not header.lower().startswith("x-proxy-"), f"Disallowed proxy header leak: {header}"
        assert not header.lower().startswith("x-cache-"), f"Disallowed cache header leak: {header}"


async def case_04_options_preflight(ctx: ConformanceContext):
    """OPTIONS preflight must return 200 with wildcard CORS headers and standard allowed methods."""
    url = "https://game.granbluefantasy.jp/rest/multiraid/start.json"
    resp = await ctx.client.options(url)
    assert resp.status_code == 200, f"Expected 200 OPTIONS, got {resp.status_code}"
    assert resp.headers.get("access-control-allow-origin") == "*", "OPTIONS must return wildcard origin"
    methods = resp.headers.get("access-control-allow-methods", "")
    assert "GET" in methods and "POST" in methods and "OPTIONS" in methods


async def case_05_upstream_relay_query(ctx: ConformanceContext):
    """GBF official portal must be relayed to upstream returning 200 OK with upstream content."""
    url = "https://granbluefantasy.jp/"
    ctx.mock_upstream.reset_counts()

    resp = await ctx.client.get(url)
    assert resp.status_code == 200, f"Relay failed: {resp.status_code}"
    assert "GBF Mock" in resp.text or "グランブルーファンタジー" in resp.text
    assert ctx.mock_upstream.get_request_count("GET", "/") >= 1, "Portal request was not relayed upstream"

    for header in resp.headers:
        assert not header.lower().startswith("x-proxy-"), f"Disallowed proxy header leak: {header}"
        assert not header.lower().startswith("x-cache-"), f"Disallowed cache header leak: {header}"


async def case_06_chunked_post(ctx: ConformanceContext):
    """Chunked Transfer-Encoding POST must be forwarded upstream intact."""
    path = "/rest/error/js"
    url = f"https://game.granbluefantasy.jp{path}"
    ctx.mock_upstream.reset_counts()

    async def chunk_gen():
        yield b"chunk_one_"
        yield b"chunk_two"

    resp = await ctx.client.post(
        url,
        content=chunk_gen(),
        headers={"transfer-encoding": "chunked"}
    )
    assert resp.status_code == 200, f"Chunked post failed: {resp.status_code}"
    assert resp.json() == {"result": "ok"}

    last_req = ctx.mock_upstream.get_last_request(path)
    assert last_req is not None, "Upstream did not record the chunked POST request"
    assert last_req.get("body") == b"chunk_one_chunk_two", (
        f"Chunked payload corruption: expected b'chunk_one_chunk_two', got {last_req.get('body')!r}"
    )


async def case_07_cors_preservation(ctx: ConformanceContext):
    """Dynamic API responses must strictly preserve original upstream CORS without injecting '*'."""
    url = "https://game.granbluefantasy.jp/rest/error/js"
    resp = await ctx.client.get(url)
    assert resp.status_code == 200, f"Dynamic API failed: {resp.status_code}"
    assert resp.headers.get("access-control-allow-origin") != "*", "Proxy must not inject wildcard CORS into dynamic responses"


async def case_08_cache_query_normalization(ctx: ConformanceContext):
    """Cache lookup strips query parameters, hitting cached asset under cache-busting queries."""
    path = f"/assets/test/conformance_asset_08_{time.time_ns()}.css"
    ctx.mock_upstream.set_scenario(path, "ok", content=b"/* 08 */\nbody { color: blue; }", content_type="text/css")
    url_base = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"

    r1 = await ctx.client.get(url_base)
    assert r1.status_code == 200

    url_q = f"{url_base}?_t=123456789&v=2.0.0"
    r2 = await ctx.client.get(url_q)
    assert r2.status_code == 200
    assert r2.content == r1.content
    assert not any(k.lower().startswith("x-proxy-") for k in r2.headers)


async def case_09_path_traversal_rejection(ctx: ConformanceContext):
    """Path traversal sequences in asset URLs must be safely rejected and never leak local files."""
    traversal_urls = [
        "https://prd-game-a-granbluefantasy.akamaized.net/assets/../../../../windows/win.ini",
        "https://prd-game-a-granbluefantasy.akamaized.net/C:/Windows/win.ini",
        "https://prd-game-a-granbluefantasy.akamaized.net/assets/../../../etc/passwd",
    ]
    for url in traversal_urls:
        try:
            resp = await ctx.client.get(url)
            assert b"[fonts]" not in resp.content.lower(), f"Leaked fonts in {url}"
            assert b"[extensions]" not in resp.content.lower(), f"Leaked extensions in {url}"
            assert b"root:x:" not in resp.content.lower(), f"Leaked passwd in {url}"
            assert resp.status_code in (400, 403, 404, 502), f"Expected error status for {url}, got {resp.status_code}: {resp.text[:60]}"
        except (httpx.RequestError, httpx.HTTPStatusError):
            pass


async def case_10_head_request_zero_body(ctx: ConformanceContext):
    """HEAD requests to static assets return 200 with zero content length body and preserved Content-Length header."""
    path = f"/assets/test/conformance_asset_10_{time.time_ns()}.css"
    ctx.mock_upstream.set_scenario(path, "ok", content=b"/* 10 */\n.box { margin: 10px; }", content_type="text/css")
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"

    await ctx.client.get(url)
    resp = await ctx.client.head(url)
    assert resp.status_code == 200
    assert len(resp.content) == 0, "HEAD request must have zero body"
    assert int(resp.headers.get("content-length", 0)) > 0, "Content-Length header must be preserved"
    assert not any(k.lower().startswith("x-proxy-") for k in resp.headers)


async def case_11_multi_set_cookie_preservation(ctx: ConformanceContext):
    """Multiple Set-Cookie headers from upstream must be preserved as distinct headers without folding."""
    path = "/rest/mock/multi_cookie"
    ctx.mock_upstream.set_scenario(path, "cookies")
    url = f"https://game.granbluefantasy.jp{path}"

    resp = await ctx.client.get(url)
    assert resp.status_code == 200
    cookies = resp.headers.get_list("set-cookie")
    assert len(cookies) >= 2, f"Expected at least 2 distinct Set-Cookie headers, got {len(cookies)}"
    assert any("session_id=sess_m2_123" in c for c in cookies), f"Missing session_id: {cookies}"
    assert any("auth_token=tok_m2_456" in c for c in cookies), f"Missing auth_token: {cookies}"


# ==============================================================================
# P1 资源调度与并发收敛契约 (P1_SCHEDULING_RESOURCES)
# ==============================================================================

async def case_12_singleflight_coalescing(ctx: ConformanceContext):
    """Concurrent requests for identical un-cached asset coalesce into 1 upstream fetch via SingleFlight."""
    path = f"/assets/test/sf_coalesce_{time.time_ns()}.png"
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"
    content = b"\x89PNG\r\n\x1a\nsf_coalesce_image_data"
    ctx.mock_upstream.set_scenario(path, "delay", delay_sec=0.15, content=content)
    ctx.mock_upstream.reset_counts()

    tasks = [ctx.client.get(url) for _ in range(20)]
    responses = await asyncio.gather(*tasks)

    for r in responses:
        assert r.status_code == 200
        assert len(r.content) > 0
        assert r.content == responses[0].content

    fetch_count = ctx.mock_upstream.get_request_count("GET", path)
    assert fetch_count == 1, f"SingleFlight failed: expected 1 upstream fetch, got {fetch_count}"


async def case_13_singleflight_failure_recovery(ctx: ConformanceContext):
    """When a SingleFlight fetch fails, in-flight state is cleared and subsequent requests re-fetch cleanly."""
    path = f"/assets/test/sf_fail_rec_{time.time_ns()}.png"
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"
    ctx.mock_upstream.set_scenario(path, "fail_once_502")
    ctx.mock_upstream.reset_counts()

    r1 = await ctx.client.get(url)
    assert r1.status_code == 502, f"Expected 502 on flight failure, got {r1.status_code}"

    r2 = await ctx.client.get(url)
    assert r2.status_code == 200, f"Expected 200 after recovery, got {r2.status_code}"
    assert len(r2.content) > 0


async def case_14_root_ca_endpoint(ctx: ConformanceContext):
    """GET /ca.crt serves valid Root CA certificate PEM for client setup."""
    resp = await ctx.direct_client.get("/ca.crt")
    assert resp.status_code == 200
    assert resp.headers.get("content-type") == "application/x-x509-ca-cert"
    assert b"BEGIN CERTIFICATE" in resp.content


async def case_15_dynamic_pac_endpoint(ctx: ConformanceContext):
    """GET /proxy.pac serves valid PAC script referencing the proxy port."""
    resp = await ctx.direct_client.get("/proxy.pac")
    assert resp.status_code == 200
    assert resp.headers.get("content-type") == "application/x-ns-proxy-autoconfig"
    assert "FindProxyForURL" in resp.text
    assert str(ctx.proxy_port) in resp.text


async def case_16_mobile_landing_endpoint(ctx: ConformanceContext):
    """GET / on proxy port serves mobile guide HTML landing page."""
    resp = await ctx.direct_client.get("/")
    assert resp.status_code == 200
    assert "text/html" in resp.headers.get("content-type", "")
    assert "GBF" in resp.text


async def case_17_skyleap_mobage_upstream(ctx: ConformanceContext):
    """SkyLeap Mobage portal request is proxied through without error."""
    ctx.mock_upstream.reset_counts()
    resp = await ctx.client.get("http://gbf.game.mbga.jp/")
    assert resp.status_code == 200, f"Mobage upstream relay failed: {resp.status_code}"
    assert "GBF Mock" in resp.text or "グランブルーファンタジー" in resp.text
    assert ctx.mock_upstream.get_request_count("GET", "/") >= 1


async def case_18_lan_acl_rejection(ctx: ConformanceContext):
    """LAN ACL permits loopback client connections and rejects non-loopback connections when allow_lan=False."""
    # 1. Loopback client must be permitted
    r_local = await ctx.direct_client.get("/ca.crt")
    assert r_local.status_code == 200
    assert b"BEGIN CERTIFICATE" in r_local.content

    # 2. Control plane reports default allow_lan=False
    r_cfg = await ctx.control_client.get("/api/config")
    assert r_cfg.status_code == 200
    cfg_data = r_cfg.json()
    assert cfg_data.get("ok") is True
    assert cfg_data.get("config", {}).get("allow_lan") is False

    r_status = await ctx.control_client.get("/api/status")
    assert r_status.status_code == 200
    status_data = r_status.json()
    assert status_data.get("allow_lan") is False
    assert status_data.get("lan_ip") is None

    # 3. Connection to non-loopback LAN IP must fail when allow_lan is False
    lan_ip = _get_local_lan_ip()
    if lan_ip and lan_ip != "127.0.0.1":
        conn_rejected = False
        try:
            async with httpx.AsyncClient(timeout=0.3, trust_env=False) as lan_client:
                await lan_client.get(f"http://{lan_ip}:{ctx.proxy_port}/ca.crt")
        except (httpx.ConnectError, httpx.ConnectTimeout):
            conn_rejected = True
        assert conn_rejected, f"LAN ACL violation: non-loopback connection to {lan_ip}:{ctx.proxy_port} was not rejected"


async def case_19_offline_cache_fallback(ctx: ConformanceContext):
    """When upstream returns error, proxy serves cached asset copy as offline fallback."""
    path = f"/assets/test/fallback_test_{time.time_ns()}.png"
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"
    content = b"\x89PNG\r\n\x1a\nfallback_valid_data"
    ctx.mock_upstream.set_scenario(path, "ok", content=content, content_type="image/png")

    r1 = await ctx.client.get(url)
    assert r1.status_code == 200

    ctx.mock_upstream.set_scenario(path, "error_html")
    r2 = await ctx.client.get(url)
    assert r2.status_code == 200, f"Expected 200 fallback cache, got {r2.status_code}"
    assert r2.content == content


async def case_20_post_zero_retry(ctx: ConformanceContext):
    """POST requests on upstream disconnect MUST NOT retry (strictly max_attempts=1)."""
    path = "/rest/raid/ability_result.json"
    url = f"https://game.granbluefantasy.jp{path}"
    ctx.mock_upstream.set_scenario(path, "drop")
    ctx.mock_upstream.reset_counts()

    resp = await ctx.client.post(url, content=b'{"ability_id":1}')
    assert resp.status_code == 502, f"Expected 502 on dropped POST, got {resp.status_code}"

    attempts = ctx.mock_upstream.get_request_count("POST", path)
    assert attempts == 1, f"Zero-retry violation: POST was retried ({attempts} attempts)"


async def case_21_get_safe_retry_whitelist(ctx: ConformanceContext):
    """Non-whitelisted GET drops without retry; whitelisted GET allows 1 silent fast retry."""
    # Part 1: Non-whitelisted GET -> Zero retry
    path_non = "/rest/multiraid/start.json"
    url_non = f"https://game.granbluefantasy.jp{path_non}"
    ctx.mock_upstream.set_scenario(path_non, "drop")
    ctx.mock_upstream.reset_counts()
    r_non = await ctx.client.get(url_non)
    assert r_non.status_code == 502
    assert ctx.mock_upstream.get_request_count("GET", path_non) == 1

    # Part 2: Whitelisted GET -> Safe 1 retry
    path_wl = "/rest/multiraid/condition.json"
    url_wl = f"https://game.granbluefantasy.jp{path_wl}"
    ctx.mock_upstream.set_scenario(path_wl, "drop_once_then_ok")
    ctx.mock_upstream.reset_counts()
    r_wl = await ctx.client.get(url_wl)
    assert r_wl.status_code == 200, f"Expected 200 on retry recovery, got {r_wl.status_code}"
    assert ctx.mock_upstream.get_request_count("GET", path_wl) == 2, "Whitelisted GET must attempt exactly 1 retry"


async def case_22_connection_reuse_keepalive(ctx: ConformanceContext):
    """Client reuse of persistent HTTP keep-alive connection across multiple requests."""
    url1 = "https://game.granbluefantasy.jp/rest/mock/req1"
    url2 = "https://game.granbluefantasy.jp/rest/mock/req2"
    ctx.mock_upstream.reset_counts()
    r1 = await ctx.client.get(url1)
    assert r1.status_code == 200
    r2 = await ctx.client.get(url2)
    assert r2.status_code == 200

    req1 = ctx.mock_upstream.get_last_request("/rest/mock/req1")
    req2 = ctx.mock_upstream.get_last_request("/rest/mock/req2")
    assert req1 is not None, "Missing req1 in mock upstream history"
    assert req2 is not None, "Missing req2 in mock upstream history"
    assert req1.get("conn_id") == req2.get("conn_id"), (
        f"Expected persistent HTTP keep-alive connection reuse, got different upstream connections: "
        f"{req1.get('conn_id')} != {req2.get('conn_id')}"
    )


async def case_23_asset_byte_fidelity(ctx: ConformanceContext):
    """Static assets served from proxy must be 100% byte-for-byte identical to upstream bytes."""
    path = f"/assets/test/byte_fidelity_{time.time_ns()}.js"
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"
    expected_bytes = b"var conformance = { version: '2.0.0', intact: true };\n"
    ctx.mock_upstream.set_scenario(path, "ok", content=expected_bytes, content_type="application/javascript")

    # 1. First fetch: streamed through proxy from upstream
    resp1 = await ctx.client.get(url)
    assert resp1.status_code == 200
    assert resp1.content == expected_bytes, "Byte fidelity violation on stream: served bytes differed from upstream"

    # 2. Second fetch: served from local cache
    resp2 = await ctx.client.get(url)
    assert resp2.status_code == 200
    assert resp2.content == expected_bytes, "Byte fidelity violation on cache hit: cached bytes differed from upstream"


async def case_24_offline_cache_independence(ctx: ConformanceContext):
    """Once cached, static assets can be retrieved offline when upstream is unreachable."""
    path = f"/assets/test/offline_indep_{time.time_ns()}.png"
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"
    content = b"\x89PNG\r\n\x1a\noffline_indep_bytes"
    ctx.mock_upstream.set_scenario(path, "ok", content=content, content_type="image/png")

    r1 = await ctx.client.get(url)
    assert r1.status_code == 200

    # Simulate upstream drop
    ctx.mock_upstream.set_scenario(path, "drop")
    r2 = await ctx.client.get(url)
    assert r2.status_code == 200
    assert r2.content == content


async def case_25_dynamic_api_isolation(ctx: ConformanceContext):
    """Dynamic game API requests are never persisted into static asset cache."""
    path = f"/rest/user/status_{time.time_ns()}.json"
    url = f"https://game.granbluefantasy.jp{path}"

    ctx.mock_upstream.set_scenario(path, "ok", content=b'{"v": 1}', content_type="application/json")
    resp1 = await ctx.client.get(url)
    assert resp1.status_code == 200
    assert resp1.content == b'{"v": 1}'

    # Dynamic API must never serve stale cache when upstream returns updated data
    ctx.mock_upstream.set_scenario(path, "ok", content=b'{"v": 2}', content_type="application/json")
    resp2 = await ctx.client.get(url)
    assert resp2.status_code == 200
    assert resp2.content == b'{"v": 2}', "Dynamic API was cached instead of fetched fresh from upstream!"

    # Dynamic API must never serve offline cache fallback when upstream fails
    ctx.mock_upstream.set_scenario(path, "drop")
    resp3 = await ctx.client.get(url)
    assert resp3.status_code == 502, f"Dynamic API served cached response instead of failing on offline upstream: {resp3.status_code}"


# ==============================================================================
# 控制面管理与协议短路契约 (CONTROL_PLANE_NETWORKING)
# ==============================================================================

async def case_26_control_status(ctx: ConformanceContext):
    """GET /api/status returns valid system status JSON with required keys."""
    resp = await ctx.control_client.get("/api/status")
    assert resp.status_code == 200
    data = resp.json()
    assert "version" in data
    assert "proxy_running" in data
    assert "listen_port" in data
    assert "control_port" in data
    assert "requests" in data
    assert "cache" in data


async def case_27_control_config_mutation(ctx: ConformanceContext):
    """GET /api/config and POST /api/config/apply mutate and restore configuration."""
    r_get = await ctx.control_client.get("/api/config")
    assert r_get.status_code == 200
    data = r_get.json()
    assert data.get("ok") is True
    orig_mb = data["config"].get("ram_cache_max_mb", 256)

    r_post = await ctx.control_client.post("/api/config/apply", json={"ram_cache_max_mb": 315})
    assert r_post.status_code == 200
    assert r_post.json().get("ok") is True

    r_rev = await ctx.control_client.post("/api/config/apply", json={"ram_cache_max_mb": orig_mb})
    assert r_rev.status_code == 200
    assert r_rev.json().get("ok") is True


async def case_28_control_cache_stats_clear(ctx: ConformanceContext):
    """GET /api/cache/stats and POST /api/cache/clear operate cleanly."""
    r_stats = await ctx.control_client.get("/api/cache/stats")
    assert r_stats.status_code == 200
    data = r_stats.json()
    assert data.get("ok") is True
    assert "ram_items" in data

    r_clear = await ctx.control_client.post("/api/cache/clear?ram_only=true")
    assert r_clear.status_code == 200
    clear_data = r_clear.json()
    assert clear_data.get("ok") is True
    assert clear_data.get("ram_cleared") is True


async def case_29_control_cache_audit_slim(ctx: ConformanceContext):
    """POST /api/cache/audit and POST /api/cache/slim trigger background maintenance jobs."""
    r_audit = await ctx.control_client.post("/api/cache/audit")
    assert r_audit.status_code == 200
    assert r_audit.json().get("ok") is True

    r_slim = await ctx.control_client.post("/api/cache/slim?keep=10")
    assert r_slim.status_code == 200
    assert r_slim.json().get("ok") is True


async def case_30_control_prefetch_status(ctx: ConformanceContext):
    """GET /api/prefetch/status returns queue and yielding metrics."""
    resp = await ctx.control_client.get("/api/prefetch/status")
    assert resp.status_code == 200
    data = resp.json()
    assert data.get("ok") is True
    assert "queue_size" in data
    assert "is_yielding" in data


async def case_31_control_telemetry_logs(ctx: ConformanceContext):
    """GET /api/telemetry and GET /api/logs return valid monitoring structures."""
    r_telem = await ctx.control_client.get("/api/telemetry")
    assert r_telem.status_code == 200
    assert r_telem.json().get("ok") is True

    r_logs = await ctx.control_client.get("/api/logs")
    assert r_logs.status_code == 200
    assert r_logs.json().get("ok") is True
    assert isinstance(r_logs.json().get("logs"), list)


async def case_32_control_sse_events(ctx: ConformanceContext):
    """GET /api/events establishes SSE streaming connection and receives initial event."""
    received = []
    async with httpx.AsyncClient(timeout=3.0, trust_env=False) as sse_client:
        async with sse_client.stream("GET", f"http://127.0.0.1:{ctx.control_port}/api/events") as stream:
            assert stream.status_code == 200
            assert "text/event-stream" in stream.headers.get("content-type", "")
            async for line in stream.aiter_lines():
                if line.startswith("event:"):
                    received.append(line.split(":", 1)[1].strip())
                if len(received) >= 1:
                    break
    assert len(received) >= 1


async def case_33_control_dashboard_static(ctx: ConformanceContext):
    """GET / serves dashboard HTML and OPTIONS /api/status returns 204 No Content CORS."""
    r_dash = await ctx.control_client.get("/")
    assert r_dash.status_code == 200
    assert "text/html" in r_dash.headers.get("content-type", "")

    r_cors = await ctx.control_client.options("/api/status")
    assert r_cors.status_code == 204
    assert r_cors.headers.get("access-control-allow-origin") == "*"


async def case_34_conditional_get_304(ctx: ConformanceContext):
    """Conditional GET with matching If-None-Match returns 304 Not Modified with empty body."""
    path = f"/assets/test/conditional_asset_{time.time_ns()}.css"
    etag = '"conformance-etag-css"'
    ctx.mock_upstream.set_scenario(path, "304_etag", etag=etag, content=b"/* cond */\n.x { margin: 0; }")
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"

    r1 = await ctx.client.get(url)
    assert r1.status_code == 200
    resp_etag = r1.headers.get("etag")
    assert resp_etag is not None

    r2 = await ctx.client.get(url, headers={"if-none-match": resp_etag})
    assert r2.status_code == 304, f"Expected 304 Not Modified, got {r2.status_code}"
    assert len(r2.content) == 0, "304 response must have empty body"
    assert r2.headers.get("etag") == resp_etag


async def case_35_anti_cache_error_pages(ctx: ConformanceContext):
    """502/503 HTML error pages returned for static asset URLs are never cached."""
    path = f"/assets/test/anti_cache_{time.time_ns()}.png"
    url = f"https://prd-game-a-granbluefantasy.akamaized.net{path}"

    # Stage 1: upstream returns 502 HTML error page
    ctx.mock_upstream.set_scenario(path, "error_html")
    r1 = await ctx.client.get(url)
    assert r1.status_code == 502

    # Stage 2: upstream recovers and returns valid PNG
    valid_png = b"\x89PNG\r\n\x1a\nvalid_png_content"
    ctx.mock_upstream.set_scenario(path, "ok", content=valid_png, content_type="image/png")
    r2 = await ctx.client.get(url)
    assert r2.status_code == 200, f"502 HTML error page was erroneously cached! Got {r2.status_code}"
    assert r2.content == valid_png


# ==============================================================================
# Complete 35-Case Contract Registry
# ==============================================================================

ALL_CASES: List[TestCase] = [
    TestCase(
        name="case_01_cache_hit_zero_headers",
        category="P0_SECURITY_INTEGRITY",
        description="Static asset cache hit must have zero X-Proxy-* and X-Cache-* headers on wire",
        func=case_01_cache_hit_zero_headers,
    ),
    TestCase(
        name="case_02_dynamic_api_passthrough",
        category="P0_SECURITY_INTEGRITY",
        description="Dynamic API requests pass through upstream with zero local mocks and clean headers",
        func=case_02_dynamic_api_passthrough,
    ),
    TestCase(
        name="case_03_heartbeat_passthrough",
        category="P0_SECURITY_INTEGRITY",
        description="Anti-cheat & heartbeat endpoint /ob/r must penetrate upstream without local interference",
        func=case_03_heartbeat_passthrough,
    ),
    TestCase(
        name="case_04_options_preflight",
        category="P0_SECURITY_INTEGRITY",
        description="OPTIONS preflight must return 200 with wildcard CORS headers and standard allowed methods",
        func=case_04_options_preflight,
    ),
    TestCase(
        name="case_05_upstream_relay_query",
        category="P0_SECURITY_INTEGRITY",
        description="GBF official portal must be relayed to upstream returning 200, 301, or 302",
        func=case_05_upstream_relay_query,
    ),
    TestCase(
        name="case_06_chunked_post",
        category="P0_SECURITY_INTEGRITY",
        description="Chunked Transfer-Encoding POST must be forwarded upstream intact",
        func=case_06_chunked_post,
    ),
    TestCase(
        name="case_07_cors_preservation",
        category="P0_SECURITY_INTEGRITY",
        description="Dynamic API responses must strictly preserve original upstream CORS without injecting '*'",
        func=case_07_cors_preservation,
    ),
    TestCase(
        name="case_08_cache_query_normalization",
        category="P0_SECURITY_INTEGRITY",
        description="Cache lookup strips query parameters, hitting cached asset under cache-busting queries",
        func=case_08_cache_query_normalization,
    ),
    TestCase(
        name="case_09_path_traversal_rejection",
        category="P0_SECURITY_INTEGRITY",
        description="Path traversal sequences in asset URLs must be safely rejected and never leak local files",
        func=case_09_path_traversal_rejection,
    ),
    TestCase(
        name="case_10_head_request_zero_body",
        category="P0_SECURITY_INTEGRITY",
        description="HEAD requests to static assets return 200 with zero content length body and preserved Content-Length header",
        func=case_10_head_request_zero_body,
    ),
    TestCase(
        name="case_11_multi_set_cookie_preservation",
        category="P0_SECURITY_INTEGRITY",
        description="Multiple Set-Cookie headers from upstream must be preserved as distinct headers without folding",
        func=case_11_multi_set_cookie_preservation,
    ),
    TestCase(
        name="case_12_singleflight_coalescing",
        category="P1_SCHEDULING_RESOURCES",
        description="Concurrent requests for identical un-cached asset coalesce into 1 upstream fetch via SingleFlight",
        func=case_12_singleflight_coalescing,
    ),
    TestCase(
        name="case_13_singleflight_failure_recovery",
        category="P1_SCHEDULING_RESOURCES",
        description="When a SingleFlight fetch fails, in-flight state is cleared and subsequent requests re-fetch cleanly",
        func=case_13_singleflight_failure_recovery,
    ),
    TestCase(
        name="case_14_root_ca_endpoint",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /ca.crt serves valid Root CA certificate PEM for client setup",
        func=case_14_root_ca_endpoint,
    ),
    TestCase(
        name="case_15_dynamic_pac_endpoint",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /proxy.pac serves valid PAC script referencing the proxy port",
        func=case_15_dynamic_pac_endpoint,
    ),
    TestCase(
        name="case_16_mobile_landing_endpoint",
        category="CONTROL_PLANE_NETWORKING",
        description="GET / on proxy port serves mobile guide HTML landing page",
        func=case_16_mobile_landing_endpoint,
    ),
    TestCase(
        name="case_17_skyleap_mobage_upstream",
        category="CONTROL_PLANE_NETWORKING",
        description="SkyLeap Mobage portal request is proxied through without error",
        func=case_17_skyleap_mobage_upstream,
    ),
    TestCase(
        name="case_18_lan_acl_rejection",
        category="CONTROL_PLANE_NETWORKING",
        description="LAN ACL permits loopback client connections and rejects non-loopback connections when allow_lan=False",
        func=case_18_lan_acl_rejection,
    ),
    TestCase(
        name="case_19_offline_cache_fallback",
        category="P1_SCHEDULING_RESOURCES",
        description="When upstream returns error, proxy serves cached asset copy as offline fallback",
        func=case_19_offline_cache_fallback,
    ),
    TestCase(
        name="case_20_post_zero_retry",
        category="P0_SECURITY_INTEGRITY",
        description="POST requests on upstream disconnect MUST NOT retry (strictly max_attempts=1)",
        func=case_20_post_zero_retry,
    ),
    TestCase(
        name="case_21_get_safe_retry_whitelist",
        category="P0_SECURITY_INTEGRITY",
        description="Non-whitelisted GET drops without retry; whitelisted GET allows 1 silent fast retry",
        func=case_21_get_safe_retry_whitelist,
    ),
    TestCase(
        name="case_22_connection_reuse_keepalive",
        category="P1_SCHEDULING_RESOURCES",
        description="Client reuse of persistent HTTP keep-alive connection across multiple requests",
        func=case_22_connection_reuse_keepalive,
    ),
    TestCase(
        name="case_23_asset_byte_fidelity",
        category="P0_SECURITY_INTEGRITY",
        description="Static assets served from proxy must be 100% byte-for-byte identical to upstream bytes",
        func=case_23_asset_byte_fidelity,
    ),
    TestCase(
        name="case_24_offline_cache_independence",
        category="P1_SCHEDULING_RESOURCES",
        description="Once cached, static assets can be retrieved offline when upstream is unreachable",
        func=case_24_offline_cache_independence,
    ),
    TestCase(
        name="case_25_dynamic_api_isolation",
        category="P0_SECURITY_INTEGRITY",
        description="Dynamic game API requests are never persisted into static asset cache",
        func=case_25_dynamic_api_isolation,
    ),
    TestCase(
        name="case_26_control_status",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /api/status returns valid system status JSON with required keys",
        func=case_26_control_status,
    ),
    TestCase(
        name="case_27_control_config_mutation",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /api/config and POST /api/config/apply mutate and restore configuration",
        func=case_27_control_config_mutation,
    ),
    TestCase(
        name="case_28_control_cache_stats_clear",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /api/cache/stats and POST /api/cache/clear operate cleanly",
        func=case_28_control_cache_stats_clear,
    ),
    TestCase(
        name="case_29_control_cache_audit_slim",
        category="CONTROL_PLANE_NETWORKING",
        description="POST /api/cache/audit and POST /api/cache/slim trigger background maintenance jobs",
        func=case_29_control_cache_audit_slim,
    ),
    TestCase(
        name="case_30_control_prefetch_status",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /api/prefetch/status returns queue and yielding metrics",
        func=case_30_control_prefetch_status,
    ),
    TestCase(
        name="case_31_control_telemetry_logs",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /api/telemetry and GET /api/logs return valid monitoring structures",
        func=case_31_control_telemetry_logs,
    ),
    TestCase(
        name="case_32_control_sse_events",
        category="CONTROL_PLANE_NETWORKING",
        description="GET /api/events establishes SSE streaming connection and receives initial event",
        func=case_32_control_sse_events,
    ),
    TestCase(
        name="case_33_control_dashboard_static",
        category="CONTROL_PLANE_NETWORKING",
        description="GET / serves dashboard HTML and OPTIONS /api/status returns 204 No Content CORS",
        func=case_33_control_dashboard_static,
    ),
    TestCase(
        name="case_34_conditional_get_304",
        category="P1_SCHEDULING_RESOURCES",
        description="Conditional GET with matching If-None-Match returns 304 Not Modified with empty body",
        func=case_34_conditional_get_304,
    ),
    TestCase(
        name="case_35_anti_cache_error_pages",
        category="P1_SCHEDULING_RESOURCES",
        description="502/503 HTML error pages returned for static asset URLs are never cached",
        func=case_35_anti_cache_error_pages,
    ),
]
