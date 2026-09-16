import asyncio
import datetime
import functools
import ipaddress
import re
import ssl
import time
import sys
import threading
import urllib.parse
import collections
from pathlib import Path
from typing import Dict, Tuple, Optional, Any

import httpx
from cert_manager import get_server_ssl_context
from cache_manager import cache_manager
from config_manager import config_manager, kill_process_on_port

# Ensure safe UTF-8 output on Windows consoles
if sys.platform == "win32":
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

# ================= Configuration =================
LISTEN_HOST = config_manager.get_effective_listen_host()
LISTEN_PORT = config_manager.get_listen_port()
UPSTREAM_PROXY = config_manager.get_effective_upstream_proxy()
DIRECT_MODE = bool(config_manager.config.get("direct_mode", False))
SHIMAKAZE_MODE = bool(config_manager.config.get("shimakaze_mode", False))

# Host patterns to perform SSL MITM inspection & caching (strictly scoped to GBF domains)
MITM_SUFFIXES = (
    "granbluefantasy.jp",
    "granbluefantasy.com",
    "granbluefantasy.akamaized.net",
    "gbf.akamaized.net",
)

# Steam edition's dedicated Akamai CDN hosts. Keep this list explicit so
# unrelated Akamai tenants are never brought into the local MITM scope.
STEAM_AKAMAI_HOSTS = frozenset(
    {
        "prd-game-a-granbluefantasy-steam.akamaized.net",
        *(f"prd-game-a{i}-granbluefantasy-steam.akamaized.net" for i in range(1, 6)),
    }
)

# Raw passthrough domains (no SSL MITM, direct low-latency TCP stream)
PASSTHROUGH_HOSTS = {
    "ws.game.granbluefantasy.jp",
}

DEFAULT_BROWSER_UA = (
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
    "AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
)

# Prefetch HTTP headers: avoid default Python client identity, send minimal and semantically correct headers
PREFETCH_HEADERS = {
    "User-Agent": DEFAULT_BROWSER_UA,
    "Accept": "*/*",
}

# ================= 架构硬约束 (v1.7.1 行为安全与透明化准则) =================
# 1. 业务透明：不修改业务状态、响应正文和业务语义；仅执行代理协议所必需的 HTTP 头部规范化。
# 2. 零 Mock 约束：任何新增的游戏接口规则必须默认原样透传；严禁引入未经专项架构评审的本地 Mock 或响应篡改逻辑。
MOCK_PATHS: tuple = ()

# Third-party telemetry, ad, and tracking domains to block/mock locally
TELEMETRY_PATTERNS = (
    "smbeat.jp",
    "smrtbeat.com",
    "rcv.a-i-ad.com",
    "datadoghq-browser-agent",
    "datadoghq.com",
    "spdmg-backend.i-mobile.co.jp",
    "creativecdn.com",
)

# Static asset extensions and path prefixes for cache coverage
STATIC_EXTENSIONS = (
    ".png", ".jpg", ".jpeg", ".gif", ".webp",
    ".mp3", ".wav", ".ogg", ".m4a", ".mp4", ".webm",
    ".js", ".css", ".woff", ".woff2", ".ttf", ".otf", ".svg", ".ico",
)

STATIC_PATH_PREFIXES = (
    "/assets/", "/assets_en/", "/sound/", "/img/", "/css/", "/js/", "/font/",
)

# Dynamic API prefixes that must NEVER be cached as static assets
DYNAMIC_API_PREFIXES = (
    "/rest/", "/quest/", "/party/", "/user/", "/deck/",
    "/gacha/", "/casino/", "/present/", "/mypage/",
    "/guild/", "/coopraid/", "/weapon/", "/socket/",
)

# HTTP/2 upstream multiplexing: lets concurrent asset fetches share one connection
# instead of paying a separate TCP+TLS handshake each (requires the h2 package).
try:
    import h2  # noqa: F401
    HTTP2_ENABLED = True
except ImportError:
    HTTP2_ENABLED = False

# Prefetch: referenced-asset extraction from cached JS/JSON bodies
ASSET_REF_RE = re.compile(
    r'[\'"(](?:https?://([^\'"()/\s]+))?/?((?:assets(?:_(?:en|jp))?|img|sound|css|js|font)/'
    r'[A-Za-z0-9_\-./%]+\.(?:png|jpe?g|gif|webp|mp3|wav|ogg|m4a|mp4|webm|js|json|css|woff2?|ttf|otf|svg))'
)

# CreateJS & Game.imgUri dynamic sprite references (e.g. /sp/cjs/npc_xxx.png, /sp/assets/...)
CJS_IMG_REF_RE = re.compile(
    r'[\'"(]/?((?:sp|assets(?:_(?:en|jp))?/img/sp)/[A-Za-z0-9_\-./%]+\.(?:png|jpe?g|gif|webp))[\'")\s]'
)

# ================= 1.7.0 Telemetry & Dual Client Isolation Architecture =================
class ApiTelemetry:
    """Low-overhead in-memory telemetry for tracking GBF API connection reuse,
    protocols, latency distributions (P50/P95/P99), and stale connection retries.
    Default-enabled, lightweight, thread-safe.
    """
    def __init__(self, max_samples: int = 1000, enabled: bool = True):
        self.enabled = enabled
        self.max_samples = max_samples
        self._samples = collections.deque(maxlen=max_samples)
        self._lock = threading.RLock()
        self.seen_streams: set = set()
        self.total_requests = 0
        self.reused_connections = 0
        self.new_connections = 0
        self.retry_count = 0
        self.exception_counts: Dict[str, int] = collections.defaultdict(int)
        self.protocols: Dict[str, int] = collections.defaultdict(int)

    def record(
        self,
        latency_ms: float,
        protocol: str = "HTTP/1.1",
        stream: Any = None,
        exception: Optional[str] = None,
        retried: bool = False,
    ) -> bool:
        if not self.enabled:
            return False
        with self._lock:
            self.total_requests += 1
            if exception:
                self.exception_counts[exception] += 1
                return False

            self._samples.append(latency_ms)
            if protocol:
                self.protocols[protocol] += 1
            if retried:
                self.retry_count += 1

            reused = False
            if stream is not None:
                if stream in self.seen_streams:
                    reused = True
                    self.reused_connections += 1
                else:
                    self.seen_streams.add(stream)
                    self.new_connections += 1
                    if len(self.seen_streams) > 500:
                        self.seen_streams.clear()
                        self.seen_streams.add(stream)
            return reused

    def get_percentiles(self) -> Dict[str, Any]:
        with self._lock:
            if not self._samples:
                return {"p50": 0.0, "p95": 0.0, "p99": 0.0, "count": 0}
            sorted_lat = sorted(self._samples)
            n = len(sorted_lat)

            def p(q: float) -> float:
                idx = min(int(n * q), n - 1)
                return round(sorted_lat[idx], 1)

            return {
                "p50": p(0.50),
                "p95": p(0.95),
                "p99": p(0.99),
                "count": n,
            }

    def get_stats(self) -> Dict[str, Any]:
        with self._lock:
            pct = self.get_percentiles()
            total = self.total_requests
            denom = self.reused_connections + self.new_connections
            reuse_rate = round(self.reused_connections / max(1, denom) * 100, 1)
            return {
                "total_requests": total,
                "reused_connections": self.reused_connections,
                "new_connections": self.new_connections,
                "reuse_rate": reuse_rate,
                "retry_count": self.retry_count,
                "percentiles": pct,
                "protocols": dict(self.protocols),
                "exceptions": dict(self.exception_counts),
            }

    def reset(self):
        with self._lock:
            self._samples.clear()
            self.seen_streams.clear()
            self.total_requests = 0
            self.reused_connections = 0
            self.new_connections = 0
            self.retry_count = 0
            self.exception_counts.clear()
            self.protocols.clear()

api_telemetry = ApiTelemetry(enabled=bool(config_manager.config.get("enable_api_telemetry", True)))

# Strict read-only idempotent whitelist: only these paths may be retried on connection-level drop
RETRYABLE_API_PATHS = frozenset({
    "/rest/multiraid/start.json",
    "/rest/multiraid/condition.json",
    "/rest/raid/start.json",
    "/rest/quest/start.json",
    "/rest/quest/stage_list",
    "/rest/party/deck_info",
})

# Dedicated Dual Clients:
# - api_client: Dedicated HTTP/1.1 Keep-Alive pool for game.granbluefantasy.jp (never blocked by prefetch)
# - asset_client: Dedicated HTTP/2 multiplexed pool for Akamai CDN & static assets
api_client: Optional[httpx.AsyncClient] = None
asset_client: Optional[httpx.AsyncClient] = None
http_client: Optional[httpx.AsyncClient] = None  # Backward-compatible alias

# Real-time statistics dictionary for GUI
PROXY_STATS = {
    "hits": 0,
    "ram_hits": 0,
    "downloads": 0,
    "apis": 0,
    "is_running": False,
    "last_error": "",
}

proxy_server_instance = None
proxy_loop = None
proxy_thread = None
import threading
proxy_ready_event = threading.Event()

ACTIVE_API_COUNT = 0
_inflight_fetches: Dict[str, asyncio.Future] = {}
SAVE_CONCURRENCY_LIMIT = 16
save_semaphore: Optional[asyncio.Semaphore] = None

class _ActiveApiTracker:
    """Context manager to ensure ACTIVE_API_COUNT is strictly decremented on exit."""
    __slots__ = ()
    def __enter__(self):
        global ACTIVE_API_COUNT
        ACTIVE_API_COUNT += 1
        return self
    def __exit__(self, exc_type, exc_val, exc_tb):
        global ACTIVE_API_COUNT
        ACTIVE_API_COUNT = max(0, ACTIVE_API_COUNT - 1)
        return False

async def _bounded_save_cache(url_path: str, headers: Dict[str, str], data: bytes, flight_key: Optional[str] = None):
    """Save cache asynchronously with bounded concurrency (backpressure)."""
    global save_semaphore
    if save_semaphore is None:
        save_semaphore = asyncio.Semaphore(SAVE_CONCURRENCY_LIMIT)
    try:
        async with save_semaphore:
            loop = asyncio.get_running_loop()
            await loop.run_in_executor(None, cache_manager.save_cache, url_path, headers, data)
    except (asyncio.CancelledError, Exception):
        pass
    finally:
        if flight_key:
            _inflight_fetches.pop(flight_key, None)

@functools.lru_cache(maxsize=256)
def _is_domain_or_subdomain(host: str, domain: str) -> bool:
    host = (host or "").rstrip(".").lower()
    domain = domain.rstrip(".").lower()
    return host == domain or host.endswith("." + domain)

@functools.lru_cache(maxsize=256)
def _is_gbf_akamai_host(host: str) -> bool:
    normalized = (host or "").rstrip(".").lower()
    # These are the GBF CDN hostnames currently used by the game. The
    # explicit prd-game-a* entries avoid treating unrelated Akamai tenants as GBF.
    explicit = {
        "prd-game-a-granbluefantasy.akamaized.net",
        *(f"prd-game-a{i}-granbluefantasy.akamaized.net" for i in range(1, 6)),
    }
    return normalized in explicit or normalized in STEAM_AKAMAI_HOSTS or any(
        _is_domain_or_subdomain(normalized, d) for d in ("granbluefantasy.akamaized.net", "gbf.akamaized.net")
    )

@functools.lru_cache(maxsize=256)
def _is_gbf_host(host: str) -> bool:
    return _is_gbf_akamai_host(host) or any(
        _is_domain_or_subdomain(host, d) for d in ("granbluefantasy.jp", "granbluefantasy.com")
    )

# Permitted LAN subnets: RFC 1918 private ranges, RFC 3927 IPv4 link-local, RFC 4193 ULA, and RFC 4291 IPv6 link-local
_LAN_SUBNETS = (
    ipaddress.ip_network("10.0.0.0/8"),
    ipaddress.ip_network("172.16.0.0/12"),
    ipaddress.ip_network("192.168.0.0/16"),
    ipaddress.ip_network("169.254.0.0/16"),
    ipaddress.ip_network("fc00::/7"),
    ipaddress.ip_network("fe80::/10"),
)

def is_client_ip_allowed(client_ip: str) -> bool:
    """Network ACL: strictly restrict clients to loopback, or RFC 1918 / link-local LAN subnets when allow_lan is True."""
    if not client_ip:
        return False
    # Strip IPv6-mapped IPv4 prefix if present (e.g. ::ffff:192.168.1.50)
    if client_ip.startswith("::ffff:"):
        client_ip = client_ip[7:]
    try:
        ip_obj = ipaddress.ip_address(client_ip)
        if ip_obj.is_loopback:
            return True
        if not config_manager.config.get("allow_lan", False):
            return False
        # When allow_lan is active, only permit genuine LAN subnets
        return any(ip_obj in net for net in _LAN_SUBNETS)
    except ValueError:
        return False

# ================= Prefetch (background asset warmup) =================
PREFETCH_WORKERS = 3
PREFETCH_QUEUE_MAX = 600
prefetch_queue: Optional[asyncio.PriorityQueue] = None
prefetch_inflight: set = set()
_prefetch_seq: int = 0

def _get_prefetch_priority(url_path: str) -> int:
    """Classify assets into priority bands for background prefetching:
    P1 (Highest): Render-blocking code & styles (.js, .css, .json manifest)
    P2 (Medium):  Visual UI textures & character assets (.png, .jpg, .webp, .svg, .ico)
    P3 (Normal):  Web fonts (.woff, .woff2, .ttf, .otf)
    P4 (Lowest):  Heavy media/audio (.mp3, .wav, .ogg, .m4a, .mp4, .webm)
    """
    clean = url_path.split("?")[0].lower()
    if clean.endswith((".js", ".css", ".json")):
        return 1
    if clean.endswith((".png", ".jpg", ".jpeg", ".webp", ".svg", ".ico")):
        return 2
    if clean.endswith((".woff", ".woff2", ".ttf", ".otf")):
        return 3
    if clean.endswith((".mp3", ".wav", ".ogg", ".m4a", ".mp4", ".webm")):
        return 4
    return 2

def extract_asset_refs(url_path: str, data: bytes, default_host: str = "") -> list:
    """Extract referenced static asset (host, path) tuples from a cached JS/JSON body (executor thread).
    Preserves explicitly declared GBF hosts in absolute URLs; falls back to default_host for relative paths.
    Supports standard assets, CreateJS twin scripts, and dynamic Game.imgUri CreateJS spritesheets.
    """
    clean = url_path.split("?")[0].lower()
    if not (clean.endswith(".js") or clean.endswith(".json")):
        return []
    if len(data) >= 2 and data[0] == 0x1F and data[1] == 0x8B:
        try:
            import gzip
            data = gzip.decompress(data)
        except Exception:
            return []
    try:
        text = data.decode("utf-8", errors="ignore")
    except Exception:
        return []

    refs: list = []
    seen: set = set()

    # 1. Deduced twin CreateJS script for model manifest files
    # E.g. /assets/{ver}/js/model/manifest/npc_xxx.js -> /assets/{ver}/js/cjs/npc_xxx.js
    clean_url = url_path.split("?")[0]
    m_twin = re.match(r"^/(assets(?:_(?:en|jp))?/\d+/js/)model/manifest/([^/]+\.js)$", clean_url)
    if m_twin:
        twin_host = default_host
        twin_cjs = f"/{m_twin.group(1)}cjs/{m_twin.group(2)}"
        item = (twin_host, twin_cjs)
        if item not in seen:
            seen.add(item)
            refs.append(item)

    # 2. Standard asset references (assets/..., img/..., sound/..., etc.)
    for m in ASSET_REF_RE.finditer(text):
        ref_host = (m.group(1) or "").lower()
        if ref_host and not (
            _is_gbf_akamai_host(ref_host)
            or _is_domain_or_subdomain(ref_host, "granbluefantasy.jp")
            or _is_domain_or_subdomain(ref_host, "granbluefantasy.com")
        ):
            continue
        host = ref_host if ref_host else default_host
        path = "/" + m.group(2)
        if len(path) > 200:
            continue
        item = (host, path)
        if item in seen:
            continue
        seen.add(item)
        refs.append(item)
        if len(refs) >= 120:
            break

    # 3. CreateJS & Game.imgUri spritesheets and textures (e.g. /sp/cjs/npc_xxx.png)
    if len(refs) < 120:
        img_prefix = "/assets_en/img" if clean_url.startswith("/assets_en/") else "/assets/img"
        for m in CJS_IMG_REF_RE.finditer(text):
            raw_path = m.group(1)
            if raw_path.startswith("sp/"):
                img_path = f"{img_prefix}/{raw_path}"
            else:
                img_path = "/" + raw_path.lstrip("/")
            if len(img_path) > 200:
                continue
            item = (default_host, img_path)
            if item in seen:
                continue
            seen.add(item)
            refs.append(item)
            if len(refs) >= 120:
                break

    return refs

async def maybe_enqueue_prefetch(target_host: str, url_path: str, data: bytes):
    """Queue missing assets referenced by a freshly served JS/JSON for background warmup."""
    global _prefetch_seq
    if not config_manager.config.get("enable_prefetch", True) or prefetch_queue is None:
        return
    clean = url_path.split("?")[0].lower()
    if not (clean.endswith(".js") or clean.endswith(".json")):
        return
    if not (_is_gbf_akamai_host(target_host) or _is_domain_or_subdomain(target_host, "granbluefantasy.jp")):
        return
    if prefetch_queue.qsize() >= PREFETCH_QUEUE_MAX:
        return
    try:
        refs = await asyncio.get_running_loop().run_in_executor(None, extract_asset_refs, url_path, data, target_host)
    except Exception:
        return

    enqueued = 0
    for host, ref in refs:
        effective_host = host or target_host
        if prefetch_queue.qsize() >= PREFETCH_QUEUE_MAX:
            break
        key = f"{effective_host}{ref}"
        if key in prefetch_inflight or cache_manager.has_cache(ref):
            continue
        prefetch_inflight.add(key)
        _prefetch_seq += 1
        prio = _get_prefetch_priority(ref)
        prefetch_queue.put_nowait((prio, _prefetch_seq, effective_host, ref))
        enqueued += 1
    if enqueued:
        format_log("PREFETCH-Q", "35", f"Queued {enqueued} referenced assets (prioritized) from {url_path}")

async def prefetch_worker():
    """Background worker: fetch queued assets via the upstream pool and save them to cache."""
    while True:
        prio, seq, target_host, url_path = await prefetch_queue.get()
        try:
            # Yield to active in-flight game API requests to avoid contending for bandwidth (max 1.5s cap)
            yield_start = time.perf_counter()
            while ACTIVE_API_COUNT > 0 and (time.perf_counter() - yield_start) < 1.5:
                await asyncio.sleep(0.05)

            flight_key = f"{target_host}{url_path}"
            if cache_manager.has_cache(url_path) or flight_key in _inflight_fetches:
                continue
            url = f"https://{target_host}{url_path}"
            resp = await request_asset("GET", url, headers=PREFETCH_HEADERS)
            if resp.status_code == 200 and resp.content:
                await _bounded_save_cache(url_path, dict(resp.headers), resp.content)
                format_log("PREFETCH", "35", f"Warmed (P{prio}) -> {target_host}{url_path} ({len(resp.content):,} B)")
        except asyncio.CancelledError:
            raise
        except Exception:
            pass
        finally:
            prefetch_inflight.discard(f"{target_host}{url_path}")
            prefetch_queue.task_done()

def run_proxy_in_thread():
    global proxy_loop, proxy_server_instance, ACTIVE_API_COUNT, save_semaphore
    ACTIVE_API_COUNT = 0
    _inflight_fetches.clear()
    save_semaphore = None
    proxy_loop = asyncio.new_event_loop()
    asyncio.set_event_loop(proxy_loop)
    try:
        proxy_loop.run_until_complete(main())
    except (asyncio.CancelledError, KeyboardInterrupt):
        pass
    except Exception as e:
        PROXY_STATS["last_error"] = str(e)
        PROXY_STATS["is_running"] = False
        proxy_ready_event.set()
    finally:
        ACTIVE_API_COUNT = 0
        _inflight_fetches.clear()
        save_semaphore = None
        PROXY_STATS["is_running"] = False
        proxy_ready_event.set()

def start_proxy_thread():
    global proxy_thread, ACTIVE_API_COUNT, save_semaphore
    ACTIVE_API_COUNT = 0
    _inflight_fetches.clear()
    save_semaphore = None
    proxy_ready_event.clear()
    PROXY_STATS["last_error"] = ""
    if proxy_thread and proxy_thread.is_alive():
        return
    proxy_thread = threading.Thread(target=run_proxy_in_thread, daemon=True)
    proxy_thread.start()

def stop_proxy_thread():
    global proxy_loop, proxy_server_instance, proxy_thread, ACTIVE_API_COUNT, save_semaphore
    ACTIVE_API_COUNT = 0
    _inflight_fetches.clear()
    save_semaphore = None
    PROXY_STATS["is_running"] = False
    proxy_ready_event.clear()
    if proxy_loop and proxy_loop.is_running():
        try:
            def request_shutdown():
                # This callback runs on the proxy loop's own thread. Cancelling
                # tasks from the GUI thread is not asyncio-thread-safe and can
                # leave the old HTTP client alive during a routing switch.
                if proxy_server_instance:
                    proxy_server_instance.close()
                current = asyncio.current_task()
                for task in asyncio.all_tasks():
                    if task is not current:
                        task.cancel()
            proxy_loop.call_soon_threadsafe(request_shutdown)
        except Exception:
            pass
    if proxy_thread and proxy_thread.is_alive():
        try:
            proxy_thread.join(timeout=1.5)
        except Exception:
            pass
    proxy_thread = None

_log_listeners: list = []
_log_history: collections.deque = collections.deque(maxlen=2000)
_log_lock: threading.Lock = threading.Lock()

def register_log_listener(callback):
    """Register a listener callback(formatted_line: str, level: str) for live logs."""
    with _log_lock:
        if callback not in _log_listeners:
            _log_listeners.append(callback)

def register_log_listener_with_history(callback) -> list:
    """Atomically register a listener callback and return current log history snapshot.
    Prevents any race condition where a log could be emitted between reading history
    and registering the listener.
    """
    with _log_lock:
        if callback not in _log_listeners:
            _log_listeners.append(callback)
        return list(_log_history)

def unregister_log_listener(callback):
    """Unregister a live log listener."""
    with _log_lock:
        if callback in _log_listeners:
            _log_listeners.remove(callback)

def get_recent_logs() -> list:
    """Return a snapshot list of (formatted_line, level) tuples."""
    with _log_lock:
        return list(_log_history)

def clear_recent_logs():
    """Clear memory log history."""
    with _log_lock:
        _log_history.clear()

def format_log(level: str, color_code: str, msg: str):
    ts = datetime.datetime.now().strftime("%H:%M:%S")
    line = f"[{ts}] [{level}] {msg}"
    with _log_lock:
        _log_history.append((line, level))
        listeners = list(_log_listeners)
    for cb in listeners:
        try:
            cb(line, level)
        except Exception:
            pass
    # ANSI colored console log (safe for windowed GUI mode)
    try:
        if sys.stdout is not None:
            print(line, flush=True)
    except Exception:
        pass

async def init_http_client():
    global api_client, asset_client, http_client
    if SHIMAKAZE_MODE:
        verify_tls = False
        api_timeout = httpx.Timeout(20.0, connect=10.0)
        asset_timeout = httpx.Timeout(25.0, connect=12.0)
    else:
        verify_tls = config_manager.config.get("verify_upstream_tls", True)
        api_timeout = httpx.Timeout(12.0, connect=6.0)
        asset_timeout = httpx.Timeout(15.0, connect=8.0)

    # 1. Dedicated Dynamic Game API Client (Strict HTTP/1.1, isolated Keep-Alive pool)
    api_limits = httpx.Limits(
        max_connections=int(config_manager.config.get("api_max_connections", 16)),
        max_keepalive_connections=int(config_manager.config.get("api_max_keepalive", 4)),
        keepalive_expiry=float(config_manager.config.get("api_keepalive_expiry", 20.0)),
    )
    api_client = httpx.AsyncClient(
        proxy=None if DIRECT_MODE else UPSTREAM_PROXY,
        verify=verify_tls,
        timeout=api_timeout,
        limits=api_limits,
        headers={"User-Agent": DEFAULT_BROWSER_UA},
        follow_redirects=False,
        trust_env=False,
        http1=True,
        http2=False,  # Explicit HTTP/1.1 for game.granbluefantasy.jp
    )
    http_client = api_client

    # 2. Dedicated Static Asset & Prefetch Client (HTTP/2 multiplexing for Akamai CDN)
    asset_limits = httpx.Limits(
        max_connections=int(config_manager.config.get("asset_max_connections", 100)),
        max_keepalive_connections=int(config_manager.config.get("asset_max_keepalive", 40)),
        keepalive_expiry=float(config_manager.config.get("asset_keepalive_expiry", 60.0)),
    )
    asset_client = httpx.AsyncClient(
        proxy=None if DIRECT_MODE else UPSTREAM_PROXY,
        verify=verify_tls,
        timeout=asset_timeout,
        limits=asset_limits,
        headers={"User-Agent": DEFAULT_BROWSER_UA},
        follow_redirects=False,
        trust_env=False,
        http1=True,
        http2=HTTP2_ENABLED,
    )

async def close_http_client():
    global api_client, asset_client, http_client
    if api_client:
        await api_client.aclose()
        api_client = None
    if asset_client:
        await asset_client.aclose()
        asset_client = None
    http_client = None

async def request_api(
    method: str,
    url: str,
    headers: Dict[str, str],
    content: bytes = b"",
    path: str = "",
) -> Tuple[httpx.Response, bool]:
    """Execute dynamic game API request via dedicated api_client (HTTP/1.1 Keep-Alive pool).
    Applies strict dual-constraint safe retry:
    - Path must be in RETRYABLE_API_PATHS (read-only idempotent whitelist)
    - Method must be GET
    - Failure must be a connection-level exception (ConnectError, RemoteProtocolError, ReadError)
      occurring before receiving a complete HTTP response
    All POST requests (attacks, skills, summons) and unknown paths are NEVER retried.
    Returns (response, reused_bool).
    """
    clean_path = path.split("?")[0].lower() if path else url.split("?")[0].lower()
    is_retryable = (method.upper() == "GET" and clean_path in RETRYABLE_API_PATHS)
    max_attempts = 2 if is_retryable else 1

    start_t = time.perf_counter()
    for attempt in range(max_attempts):
        try:
            resp = await api_client.request(method, url, headers=headers, content=content)
            elapsed_ms = (time.perf_counter() - start_t) * 1000
            stream = resp.extensions.get("network_stream") if hasattr(resp, "extensions") else None
            reused = api_telemetry.record(
                latency_ms=elapsed_ms,
                protocol=resp.http_version,
                stream=stream,
                retried=(attempt > 0),
            )
            return resp, reused
        except (httpx.ConnectError, httpx.RemoteProtocolError, httpx.ReadError) as e:
            if attempt + 1 < max_attempts:
                format_log("STALE-RETRY", "33", f"Stale connection on read-only {clean_path} ({e.__class__.__name__}), fast-reconnecting (1/1)...")
                continue
            api_telemetry.record(
                latency_ms=(time.perf_counter() - start_t) * 1000,
                exception=e.__class__.__name__,
            )
            raise
        except Exception as e:
            api_telemetry.record(
                latency_ms=(time.perf_counter() - start_t) * 1000,
                exception=e.__class__.__name__,
            )
            raise

async def request_asset(
    method: str,
    url: str,
    headers: Optional[Dict[str, str]] = None,
    content: bytes = b"",
) -> httpx.Response:
    """Execute static asset or prefetch request via dedicated asset_client (HTTP/2 multiplexing)."""
    return await asset_client.request(method, url, headers=headers or {}, content=content)

async def pipe_stream(reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
    try:
        while not reader.at_eof():
            data = await reader.read(65536)
            if not data:
                break
            writer.write(data)
            await writer.drain()
    except (asyncio.CancelledError, ConnectionResetError, BrokenPipeError):
        pass
    except Exception:
        pass
    finally:
        try:
            writer.close()
            await writer.wait_closed()
        except Exception:
            pass

async def handle_passthrough(client_reader: asyncio.StreamReader, client_writer: asyncio.StreamWriter, target_host: str, target_port: int):
    """Tunnel raw TCP via Clash upstream for WebSockets and non-MITM hosts."""
    try:
        if DIRECT_MODE:
            upstream_reader, upstream_writer = await asyncio.wait_for(
                asyncio.open_connection(target_host, target_port), timeout=8.0
            )
        else:
            parsed = urllib.parse.urlparse(UPSTREAM_PROXY)
            proxy_h = parsed.hostname or "127.0.0.1"
            proxy_p = parsed.port or 7897
            upstream_reader, upstream_writer = await asyncio.wait_for(
                asyncio.open_connection(proxy_h, proxy_p), timeout=8.0
            )
            scheme = (parsed.scheme or "http").lower()
            if scheme.startswith("socks"):
                username = urllib.parse.unquote(parsed.username or "").encode("utf-8")
                password = urllib.parse.unquote(parsed.password or "").encode("utf-8")
                methods = b"\x00" if not username else b"\x00\x02"
                upstream_writer.write(b"\x05" + bytes([len(methods)]) + methods)
                await upstream_writer.drain()
                greeting = await asyncio.wait_for(upstream_reader.readexactly(2), timeout=8.0)
                if greeting[0] != 5 or greeting[1] == 255:
                    raise OSError("SOCKS5 authentication negotiation failed")
                if greeting[1] == 2:
                    if len(username) > 255 or len(password) > 255:
                        raise OSError("SOCKS5 credentials are too long")
                    upstream_writer.write(b"\x01" + bytes([len(username)]) + username + bytes([len(password)]) + password)
                    await upstream_writer.drain()
                    if await asyncio.wait_for(upstream_reader.readexactly(2), timeout=8.0) != b"\x01\x00":
                        raise OSError("SOCKS5 username/password authentication failed")
                host_bytes = target_host.encode("idna")
                if len(host_bytes) > 255:
                    raise OSError("target hostname too long")
                upstream_writer.write(b"\x05\x01\x00\x03" + bytes([len(host_bytes)]) + host_bytes + target_port.to_bytes(2, "big"))
                await upstream_writer.drain()
                reply = await asyncio.wait_for(upstream_reader.readexactly(4), timeout=8.0)
                if reply[1] != 0:
                    raise OSError(f"SOCKS5 CONNECT failed: {reply[1]}")
                atyp = reply[3]
                addr_len = 4 if atyp == 1 else (16 if atyp == 4 else (await upstream_reader.readexactly(1))[0])
                await upstream_reader.readexactly(addr_len + 2)
            else:
                connect_req = f"CONNECT {target_host}:{target_port} HTTP/1.1\r\nHost: {target_host}:{target_port}\r\n\r\n"
                upstream_writer.write(connect_req.encode("ascii"))
                await upstream_writer.drain()
                resp_line = await asyncio.wait_for(upstream_reader.readline(), timeout=10.0)
                parts = resp_line.decode("iso-8859-1", errors="replace").strip().split()
                if len(parts) < 2 or parts[1] != "200":
                    client_writer.write(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                    await client_writer.drain()
                    client_writer.close()
                    return
                while True:
                    line = await upstream_reader.readline()
                    if line in (b"\r\n", b"\n", b""):
                        break

        # Acknowledge to client
        client_writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        await client_writer.drain()

        format_log("BYPASS-TCP", "36", f"Tunneling {target_host}:{target_port} via upstream")

        # Bidirectional raw TCP piping
        await asyncio.gather(
            pipe_stream(client_reader, upstream_writer),
            pipe_stream(upstream_reader, client_writer),
            return_exceptions=True,
        )
    except Exception as e:
        try:
            client_writer.close()
        except Exception:
            pass

async def read_http_request(reader: asyncio.StreamReader) -> Optional[Tuple[str, str, str, Dict[str, str], bytes]]:
    """Read and parse a full HTTP request with single-pass header buffering and chunked support."""
    try:
        header_data = await asyncio.wait_for(reader.readuntil(b"\r\n\r\n"), timeout=30.0)
    except (asyncio.TimeoutError, asyncio.IncompleteReadError, asyncio.LimitOverrunError, ConnectionResetError, OSError):
        return None

    if not header_data or len(header_data) > 64 * 1024:
        return None

    try:
        raw_header_str = header_data[:-4].decode("iso-8859-1")
        lines = raw_header_str.split("\r\n")
        if not lines:
            return None
        parts = lines[0].strip().split()
        if len(parts) < 3:
            return None
        method, path, version = parts[0], parts[1], parts[2]

        headers: Dict[str, str] = {}
        for line in lines[1:]:
            if ":" in line:
                k, v = line.split(":", 1)
                headers[k.strip().lower()] = v.strip()
    except Exception:
        return None

    body = b""
    MAX_BODY_BYTES = 32 * 1024 * 1024  # 32MB max body

    # 1. Handle Chunked Transfer-Encoding
    if "chunked" in headers.get("transfer-encoding", "").lower():
        chunks = []
        total_chunk_bytes = 0
        while True:
            try:
                chunk_line = await asyncio.wait_for(reader.readline(), timeout=15.0)
            except (asyncio.TimeoutError, ConnectionResetError, OSError):
                return None
            if not chunk_line:
                break
            chunk_line_str = chunk_line.decode("iso-8859-1").strip().split(";")[0]
            if not chunk_line_str:
                continue
            try:
                chunk_size = int(chunk_line_str, 16)
            except ValueError:
                return None

            if chunk_size == 0:
                # Consume trailing trailer/empty line
                try:
                    await asyncio.wait_for(reader.readline(), timeout=5.0)
                except Exception:
                    pass
                break

            if total_chunk_bytes + chunk_size > MAX_BODY_BYTES:
                return None

            try:
                chunk_data = await asyncio.wait_for(reader.readexactly(chunk_size), timeout=15.0)
                # Consume trailing \r\n after chunk data
                await asyncio.wait_for(reader.readline(), timeout=5.0)
            except (asyncio.TimeoutError, ConnectionResetError, OSError):
                return None

            chunks.append(chunk_data)
            total_chunk_bytes += chunk_size

        body = b"".join(chunks)

    # 2. Handle standard Content-Length
    else:
        try:
            content_length = int(headers.get("content-length", 0))
        except ValueError:
            return None

        if content_length > MAX_BODY_BYTES:
            return None

        if content_length > 0:
            try:
                body = await asyncio.wait_for(reader.readexactly(content_length), timeout=30.0)
            except (asyncio.TimeoutError, ConnectionResetError, OSError):
                return None

    return method, path, version, headers, body

async def send_cached_response(
    writer: asyncio.StreamWriter,
    status_code: int,
    status_text: str,
    headers: Dict[str, str],
    body: bytes,
    keep_content_encoding: bool = False,
    is_head: bool = False,
):
    """Send HTTP response for local cache hits, mock endpoints, preflights, or synthetic errors."""
    filtered_headers: Dict[str, str] = {}
    for k, v in headers.items():
        k_lower = k.lower()
        if k_lower in ("content-length", "transfer-encoding", "connection"):
            continue
        if k_lower.startswith(("x-proxy-", "x-cache-")):
            continue
        if not keep_content_encoding and k_lower == "content-encoding":
            continue
        filtered_headers[k_lower] = v

    filtered_headers["content-length"] = str(len(body))
    filtered_headers["connection"] = "keep-alive"
    # Ensure CORS is allowed for locally mocked or cached static assets
    filtered_headers["access-control-allow-origin"] = "*"

    res_lines = [f"HTTP/1.1 {status_code} {status_text}"]
    for k, v in filtered_headers.items():
        res_lines.append(f"{k}: {v}")

    raw_header = ("\r\n".join(res_lines) + "\r\n\r\n").encode("iso-8859-1")
    if is_head:
        writer.write(raw_header)
    else:
        writer.write(raw_header + body)
    await writer.drain()

async def _safe_close_writer(writer: asyncio.StreamWriter, timeout: float = 0.5):
    """Safely close writer with timeout to avoid hanging on wait_closed."""
    try:
        writer.close()
        await asyncio.wait_for(writer.wait_closed(), timeout=timeout)
    except Exception:
        pass

async def forward_upstream_response(
    writer: asyncio.StreamWriter,
    client_headers: Dict[str, str],
    upstream_resp: httpx.Response,
    is_head: bool = False,
) -> bool:
    """Forward dynamic API response strictly preserving original upstream headers without CORS tampering."""
    # Since httpx automatically decompresses content into upstream_resp.content,
    # content-encoding must be stripped so clients don't attempt double decompression.
    hop_by_hop = {"transfer-encoding", "trailer", "te", "upgrade", "content-encoding"}
    out_headers: Dict[str, str] = {}

    for k, v in upstream_resp.headers.items():
        k_lower = k.lower()
        if k_lower in hop_by_hop or k_lower in ("content-length", "set-cookie"):
            continue
        out_headers[k_lower] = v

    # Extract all individual Set-Cookie headers without comma-folding
    cookies = upstream_resp.headers.get_list("set-cookie")

    # Set accurate Content-Length for buffered body
    out_headers["content-length"] = str(len(upstream_resp.content))

    # Connection negotiation: honor close if requested by client or upstream
    client_conn = client_headers.get("connection", "").lower()
    upstream_conn = upstream_resp.headers.get("connection", "").lower()
    should_close = ("close" in client_conn or "close" in upstream_conn or upstream_resp.http_version == "HTTP/1.0")

    if should_close:
        out_headers["connection"] = "close"
    else:
        out_headers["connection"] = "keep-alive"

    res_lines = [f"HTTP/1.1 {upstream_resp.status_code} {upstream_resp.reason_phrase}"]
    for k, v in out_headers.items():
        res_lines.append(f"{k}: {v}")

    # Emit each Set-Cookie as an individual header line
    for cookie in cookies:
        res_lines.append(f"Set-Cookie: {cookie}")

    raw_header = ("\r\n".join(res_lines) + "\r\n\r\n").encode("iso-8859-1")
    if is_head:
        writer.write(raw_header)
    else:
        writer.write(raw_header + upstream_resp.content)
    await writer.drain()

    return not should_close

async def handle_mitm_session(reader: asyncio.StreamReader, writer: asyncio.StreamWriter, target_host: str, ssl_context: ssl.SSLContext):
    """Handle decrypted HTTPS requests for GBF domains."""
    try:
        # Tell client CONNECT succeeded
        writer.write(b"HTTP/1.1 200 Connection Established\r\n\r\n")
        await writer.drain()

        # Upgrade connection to TLS
        await writer.start_tls(ssl_context)
    except Exception as e:
        format_log("TLS-ERR", "31", f"Handshake failed with client for {target_host}: {e}")
        try:
            writer.close()
        except Exception:
            pass
        return

    # Loop to handle HTTP Keep-Alive requests on this TLS connection
    while True:
        try:
            req = await read_http_request(reader)
            if not req:
                break
            method, path, version, headers, body = req
            is_head = (method.upper() == "HEAD")

            # ---------------- Rule 1: CORS OPTIONS ----------------
            if method.upper() == "OPTIONS":
                cors_headers = {
                    "Access-Control-Allow-Origin": "*",
                    "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
                    "Access-Control-Allow-Headers": "*",
                    "Access-Control-Max-Age": "604800",
                    "Connection": "keep-alive",
                }
                await send_cached_response(writer, 200, "OK", cors_headers, b"", is_head=is_head)
                format_log("OPTIONS", "35", f"CORS Preflight Mock -> {target_host}{path}")
                continue

            # ---------------- Rule 2: Mock Endpoints (Zero mock policy in v1.7.1) ----------------
            if MOCK_PATHS and target_host.endswith("granbluefantasy.jp") and path.startswith(MOCK_PATHS):
                mock_headers = {
                    "Content-Type": "application/json",
                    "Access-Control-Allow-Origin": "*",
                    "Connection": "keep-alive",
                }
                await send_cached_response(writer, 200, "OK", mock_headers, b'{"success":true}', is_head=is_head)
                err_msg = ""
                if "error" in path and body:
                    try:
                        err_msg = f" (err: {body.decode('utf-8', errors='ignore')[:120]})"
                    except Exception:
                        pass
                format_log("MOCK-200", "33", f"Direct Mock -> {path}{err_msg}")
                continue

            # ---------------- Rule 3: Static Asset Cache (Strict & Safe) ----------------
            clean_path_lower = path.split("?")[0].lower()
            is_static = (
                method.upper() in ("GET", "HEAD")
                and not path.startswith(DYNAMIC_API_PREFIXES)
                and (
                    _is_gbf_akamai_host(target_host)
                    or target_host.startswith("game-a")
                    or path.startswith(STATIC_PATH_PREFIXES)
                    or any(clean_path_lower.endswith(ext) for ext in STATIC_EXTENSIONS)
                )
            )

            if is_static:
                is_head = (method.upper() == "HEAD")
                loop = asyncio.get_running_loop()
                req_etag = headers.get("if-none-match", "")

                # 1. Zero-executor RAM cache fast path (pure memory, sub-millisecond)
                ram_hit = cache_manager.get_ram_cache(path)
                if ram_hit:
                    c_headers, c_data = ram_hit
                    if req_etag and req_etag == c_headers.get("ETag"):
                        not_mod_headers = {
                            "ETag": c_headers["ETag"],
                            "Cache-Control": c_headers["Cache-Control"],
                            "Access-Control-Allow-Origin": "*",
                            "Connection": "keep-alive",
                        }
                        await send_cached_response(writer, 304, "Not Modified", not_mod_headers, b"", is_head=is_head)
                        PROXY_STATS["hits"] += 1
                        PROXY_STATS["ram_hits"] += 1
                        format_log("CACHE-RAM", "32", f"304 Not Modified -> {path}")
                        continue

                    await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                    PROXY_STATS["hits"] += 1
                    PROXY_STATS["ram_hits"] += 1
                    format_log("CACHE-RAM", "32", f"HIT -> {path} ({len(c_data):,} B)")
                    if not is_head:
                        await maybe_enqueue_prefetch(target_host, path, c_data)
                    continue

                # 2. Fast 304 path for disk cache: answer revalidations from metadata only (.ext sidecar)
                if req_etag:
                    peeked = await loop.run_in_executor(None, cache_manager.peek_cache_meta, path)
                    if peeked is not None:
                        peek_etag, peek_headers, peek_is_ram = peeked
                        if req_etag == peek_etag:
                            await send_cached_response(writer, 304, "Not Modified", peek_headers, b"", is_head=is_head)
                            PROXY_STATS["hits"] += 1
                            if peek_is_ram:
                                PROXY_STATS["ram_hits"] += 1
                            format_log(f"CACHE-{'RAM' if peek_is_ram else 'DISK'}", "32", f"304 Not Modified (meta) -> {path}")
                            continue

                # 3. Disk cache read (in executor so slow disk I/O never blocks the event loop)
                cache_hit = await loop.run_in_executor(None, cache_manager.get_disk_cache, path)
                if cache_hit:
                    c_headers, c_data = cache_hit
                    # Fallback 304 check
                    if req_etag and req_etag == c_headers.get("ETag"):
                        not_mod_headers = {
                            "ETag": c_headers["ETag"],
                            "Cache-Control": c_headers["Cache-Control"],
                            "Access-Control-Allow-Origin": "*",
                            "Connection": "keep-alive",
                        }
                        await send_cached_response(writer, 304, "Not Modified", not_mod_headers, b"", is_head=is_head)
                        PROXY_STATS["hits"] += 1
                        format_log("CACHE-DISK", "32", f"304 Not Modified -> {path}")
                        continue

                    await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                    PROXY_STATS["hits"] += 1
                    format_log("CACHE-DISK", "32", f"HIT -> {path} ({len(c_data):,} B)")
                    if not is_head:
                        await maybe_enqueue_prefetch(target_host, path, c_data)
                    continue

                # SingleFlight: Coalesce concurrent cache-miss requests for the identical asset
                flight_key = f"{target_host}{path}"
                flight_fut = _inflight_fetches.get(flight_key)
                if flight_fut is not None:
                    # Another concurrent request is already fetching this asset from upstream!
                    try:
                        shared_hit = await asyncio.shield(flight_fut)
                    except Exception:
                        shared_hit = None

                    if shared_hit is not None:
                        c_headers, c_data = shared_hit
                        await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                        PROXY_STATS["hits"] += 1
                        format_log("CACHE-FLIGHT", "32", f"COALESCED -> {path} ({len(c_data):,} B)")
                        if not is_head:
                            await maybe_enqueue_prefetch(target_host, path, c_data)
                        continue

                    # If the flight leader failed, check if another task successfully populated cache
                    cache_hit = await loop.run_in_executor(None, cache_manager.get_cache, path)
                    if cache_hit:
                        c_headers, c_data = cache_hit
                        await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                        PROXY_STATS["hits"] += 1
                        format_log("CACHE-DISK", "32", f"HIT -> {path} ({len(c_data):,} B)")
                        continue

                is_flight_leader = False
                if not is_head:
                    flight_fut = loop.create_future()
                    _inflight_fetches[flight_key] = flight_fut
                    is_flight_leader = True

                try:
                    # Cache MISS: fetch via Clash, save & compress
                    start_t = time.perf_counter()
                    url = f"https://{target_host}{path}"
                    clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length", "if-modified-since", "if-none-match")}
                    resp = None
                    max_attempts = 2 if SHIMAKAZE_MODE and method.upper() in ("GET", "HEAD") else 1
                    for attempt in range(max_attempts):
                        try:
                            resp = await request_asset(method, url, headers=clean_headers, content=body)
                            break
                        except httpx.TimeoutException as e:
                            if attempt + 1 < max_attempts:
                                format_log("RETRY", "33", f"Asset timeout ({e.__class__.__name__}), auto-retrying (1/1) -> {url}")
                                continue
                            fb = await loop.run_in_executor(None, cache_manager.get_fallback_cache, path)
                            if fb is not None:
                                c_headers, c_data = fb
                                if is_flight_leader and not flight_fut.done():
                                    flight_fut.set_result((c_headers, c_data))
                                await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                                PROXY_STATS["hits"] += 1
                                fb_src = c_headers.get("X-Proxy-Fallback", "STALE")
                                format_log("FALLBACK", "33", f"Timeout ({e.__class__.__name__}) -> Served fallback cache ({fb_src}) -> {path}")
                                if not is_head:
                                    asyncio.create_task(maybe_enqueue_prefetch(target_host, path, b""))
                                resp = None
                                break
                            format_log("TIMEOUT", "31", f"Timeout fetching asset ({e.__class__.__name__}) -> {url}")
                            err_body = b'{"error": "Upstream Gateway Timeout", "code": 504}'
                            await send_cached_response(writer, 504, "Gateway Timeout", {"Content-Type": "application/json"}, err_body, is_head=is_head)
                            resp = None
                            break
                        except Exception as e:
                            if attempt + 1 < max_attempts and isinstance(e, (httpx.ConnectError, httpx.NetworkError)):
                                format_log("RETRY", "33", f"Asset fetch error, auto-retrying (1/1) -> {url}: {e}")
                                continue
                            fb = await loop.run_in_executor(None, cache_manager.get_fallback_cache, path)
                            if fb is not None:
                                c_headers, c_data = fb
                                if is_flight_leader and not flight_fut.done():
                                    flight_fut.set_result((c_headers, c_data))
                                await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                                PROXY_STATS["hits"] += 1
                                fb_src = c_headers.get("X-Proxy-Fallback", "STALE")
                                format_log("FALLBACK", "33", f"Fetch error ({e}) -> Served fallback cache ({fb_src}) -> {path}")
                                if not is_head:
                                    asyncio.create_task(maybe_enqueue_prefetch(target_host, path, b""))
                                resp = None
                                break
                            format_log("ERROR", "31", f"Error fetching asset -> {url}: {e}")
                            err_body = b'{"error": "Bad Gateway", "code": 502}'
                            await send_cached_response(writer, 502, "Bad Gateway", {"Content-Type": "application/json"}, err_body, is_head=is_head)
                            resp = None
                            break

                    if resp is None:
                        continue

                    elapsed_ms = int((time.perf_counter() - start_t) * 1000)

                    if resp.status_code in (500, 502, 503, 504):
                        fb = await loop.run_in_executor(None, cache_manager.get_fallback_cache, path)
                        if fb is not None:
                            c_headers, c_data = fb
                            if is_flight_leader and not flight_fut.done():
                                flight_fut.set_result((c_headers, c_data))
                            await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                            PROXY_STATS["hits"] += 1
                            fb_src = c_headers.get("X-Proxy-Fallback", "STALE")
                            format_log("FALLBACK", "33", f"Upstream {resp.status_code} -> Served fallback cache ({fb_src}) -> {path}")
                            if not is_head:
                                asyncio.create_task(maybe_enqueue_prefetch(target_host, path, b""))
                            continue

                    if resp.status_code == 200 and resp.content:
                        c_data = resp.content
                        c_headers = cache_manager.build_response_headers(path, dict(resp.headers), len(c_data), c_data)

                        # Instantly register into RAM hot-cache (< 2µs) so subsequent requests immediately hit RAM
                        cache_manager.store_ram_cache(path, dict(resp.headers), c_data)

                        if is_flight_leader and not flight_fut.done():
                            flight_fut.set_result((c_headers, c_data))

                        # Respond-first: deliver asset to browser immediately without waiting for disk I/O
                        await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True, is_head=is_head)
                        PROXY_STATS["downloads"] += 1
                        format_log("FETCH-ASSET", "34", f"200 OK & STREAMED ({elapsed_ms}ms) -> {path}")

                        # Save-async: persist in background executor thread with backpressure;
                        # Retain SingleFlight entry until disk save completes to eliminate the race window
                        if is_flight_leader:
                            is_flight_leader = False  # Transfer cleanup to _bounded_save_cache
                            asyncio.create_task(_bounded_save_cache(path, dict(resp.headers), c_data, flight_key=flight_key))
                        else:
                            asyncio.create_task(_bounded_save_cache(path, dict(resp.headers), c_data))

                        if not is_head:
                            await maybe_enqueue_prefetch(target_host, path, c_data)
                        continue

                    # Fallback if non-200 or unable to cache
                    keep_alive = await forward_upstream_response(writer, headers, resp, is_head=is_head)
                    if not keep_alive:
                        break
                    continue
                finally:
                    if is_flight_leader:
                        _inflight_fetches.pop(flight_key, None)
                        if not flight_fut.done():
                            flight_fut.set_result(None)

            # ---------------- Rule 4: Dynamic Game API (Dedicated api_client, HTTP/1.1 Keep-Alive) ----------------
            with _ActiveApiTracker():
                start_t = time.perf_counter()
                url = f"https://{target_host}{path}"
                clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length")}
                resp = None
                reused = False
                try:
                    resp, reused = await request_api(method, url, headers=clean_headers, content=body, path=path)
                except httpx.TimeoutException as e:
                    elapsed_ms = int((time.perf_counter() - start_t) * 1000)
                    format_log("TIMEOUT", "31", f"API Gateway Timeout ({e.__class__.__name__}, {elapsed_ms}ms) -> {method} {path}")
                    err_body = b'{"error": "Upstream API Gateway Timeout", "code": 504}'
                    await send_cached_response(writer, 504, "Gateway Timeout", {"Content-Type": "application/json"}, err_body, is_head=is_head)
                    resp = None
                except Exception as e:
                    elapsed_ms = int((time.perf_counter() - start_t) * 1000)
                    format_log("API-ERR", "31", f"API Forward Error ({e.__class__.__name__}, {elapsed_ms}ms) -> {method} {path}: {e}")
                    err_body = b'{"error": "Bad Gateway", "code": 502}'
                    await send_cached_response(writer, 502, "Bad Gateway", {"Content-Type": "application/json"}, err_body, is_head=is_head)
                    resp = None

            if resp is None:
                continue

            elapsed_ms = int((time.perf_counter() - start_t) * 1000)
            keep_alive = await forward_upstream_response(writer, headers, resp, is_head=is_head)
            PROXY_STATS["apis"] += 1

            # Highlight slow API responses (>300ms) or errors, with protocol and reuse tag
            color = "31" if resp.status_code >= 400 else ("33" if elapsed_ms > 300 else "37")
            proto_str = f", {resp.http_version}" if hasattr(resp, "http_version") else ""
            reused_str = ", reused" if reused else ", new"
            format_log("BYPASS-API", color, f"{resp.status_code} {method} {target_host}{path} ({elapsed_ms}ms{proto_str}{reused_str})")

            if not keep_alive:
                break

        except (asyncio.CancelledError, ConnectionResetError, BrokenPipeError):
            break
        except Exception as e:
            format_log("SESSION-ERR", "31", f"Unexpected session error on {target_host}: {e}")
            break

    try:
        writer.close()
        await writer.wait_closed()
    except Exception:
        pass

async def client_handler(reader: asyncio.StreamReader, writer: asyncio.StreamWriter, ssl_context: ssl.SSLContext):
    """Entrypoint for all client connections."""
    try:
        peer = writer.get_extra_info("peername")
        client_ip = peer[0] if peer else "127.0.0.1"
        if not is_client_ip_allowed(client_ip):
            format_log("ACL-BLOCK", "31", f"Rejected unauthorized connection from non-LAN / public IP: {client_ip}")
            await _safe_close_writer(writer)
            return

        first_line = await reader.readline()
        if not first_line:
            writer.close()
            return

        parts = first_line.decode("iso-8859-1").strip().split()
        if len(parts) < 2:
            writer.close()
            return

        method, target = parts[0].upper(), parts[1]

        if method == "CONNECT":
            # Read remaining initial headers
            while True:
                line = await reader.readline()
                if not line or line in (b"\r\n", b"\n"):
                    break

            # Block telemetry tunnels immediately
            if any(pat in target for pat in TELEMETRY_PATTERNS):
                writer.write(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                await writer.drain()
                writer.close()
                format_log("BLOCK", "90", f"Blocked telemetry tunnel: {target}")
                return

            # CONNECT host:port
            try:
                if ":" in target:
                    host, port_str = target.rsplit(":", 1)
                    port = int(port_str)
                else:
                    host, port = target, 443
                if not (1 <= port <= 65535) or not host:
                    raise ValueError
            except ValueError:
                writer.write(b"HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                await writer.drain()
                writer.close()
                return

            # Determine whether to MITM or Passthrough (strictly scope Akamai to GBF subdomains)
            host = host.rstrip(".").lower()
            is_gbf_akamaized = _is_gbf_akamai_host(host)
            should_mitm = (
                host not in PASSTHROUGH_HOSTS
                and "analytics" not in host
                and (
                    is_gbf_akamaized
                    or _is_gbf_host(host)
                )
            )

            peer = writer.get_extra_info("peername")
            client_ip = f"[{peer[0]}] " if peer else ""
            format_log("CONNECT", "36", f"{client_ip}{target} -> {'MITM' if should_mitm else 'TUNNEL'}")

            if should_mitm:
                await handle_mitm_session(reader, writer, host, ssl_context)
            else:
                await handle_passthrough(reader, writer, host, port)
        else:
            # Plain HTTP request (e.g. GET /proxy.pac, GET /ca.crt, or proxy request GET http://gbf.game.mbga.jp/...)
            headers: Dict[str, str] = {}
            while True:
                line = await reader.readline()
                if not line or line in (b"\r\n", b"\n"):
                    break
                try:
                    h_str = line.decode("iso-8859-1").strip()
                    if ":" in h_str:
                        k, v = h_str.split(":", 1)
                        headers[k.strip().lower()] = v.strip()
                except Exception:
                    pass

            target_clean = target.split("?")[0].lower()

            # 1. Root CA certificate download endpoint (convenient for iOS / mobile Safari installation)
            if target_clean in ("/ca.crt", "/ca.pem") or target_clean.endswith(("/ca.crt", "/ca.pem")):
                from cert_manager import CA_CERT_PATH, ensure_ca
                ensure_ca()
                if CA_CERT_PATH.is_file():
                    cert_bytes = CA_CERT_PATH.read_bytes()
                    cert_headers = {
                        "Content-Type": "application/x-x509-ca-cert",
                        "Content-Length": str(len(cert_bytes)),
                        "Content-Disposition": 'attachment; filename="gbf_ca.crt"',
                        "Access-Control-Allow-Origin": "*",
                        "Cache-Control": "no-cache",
                        "Connection": "close",
                    }
                    await send_cached_response(writer, 200, "OK", cert_headers, cert_bytes)
                    format_log("CA", "36", f"Served ca.crt to mobile/client {writer.get_extra_info('peername')}")
                    await _safe_close_writer(writer)
                    return

            # 2. Local/LAN PAC script (dynamically resolves host for other LAN devices)
            if target_clean == "/proxy.pac" or target_clean.endswith("/proxy.pac"):
                from app_main import get_pac_content
                from config_manager import get_lan_ip
                host_hdr = headers.get("host", "").split(":")[0].strip()
                if host_hdr and host_hdr not in ("127.0.0.1", "localhost"):
                    pac_host = host_hdr
                elif config_manager.config.get("allow_lan", False):
                    pac_host = get_lan_ip()
                else:
                    pac_host = "127.0.0.1"

                pac_bytes = get_pac_content(LISTEN_PORT, host=pac_host).encode("utf-8")
                pac_headers = {
                    "Content-Type": "application/x-ns-proxy-autoconfig",
                    "Content-Length": str(len(pac_bytes)),
                    "Access-Control-Allow-Origin": "*",
                    "Cache-Control": "no-cache",
                    "Connection": "close",
                }
                await send_cached_response(writer, 200, "OK", pac_headers, pac_bytes)
                format_log("PAC", "36", f"Served /proxy.pac (pointing to {pac_host}:{LISTEN_PORT})")
                await _safe_close_writer(writer)
                return

            # 3. Mobile LAN landing page (when accessing http://<IP>:<PORT>/ in browser)
            if target_clean in ("/", "/index.html"):
                from config_manager import get_lan_ip
                lan_ip = get_lan_ip()
                html = f"""<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>GBF 加速器 局域网配置</title>
    <style>
        body {{ font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Microsoft YaHei", sans-serif; margin: 24px; background: #f8f9fa; color: #212529; }}
        .card {{ background: #ffffff; padding: 24px; border-radius: 12px; max-width: 620px; margin: auto; box-shadow: 0 4px 16px rgba(0,0,0,0.06); }}
        h1 {{ color: #0d6efd; font-size: 18px; margin-top: 0; }}
        .btn {{ display: inline-block; background: #0d6efd; color: #fff; padding: 8px 16px; border-radius: 4px; text-decoration: none; font-size: 14px; margin: 6px 0; }}
        code {{ background: #e9ecef; padding: 2px 6px; border-radius: 4px; font-family: Consolas, monospace; word-break: break-all; }}
        ol {{ padding-left: 20px; line-height: 1.7; }}
        li {{ margin-bottom: 10px; }}
    </style>
</head>
<body>
    <div class="card">
        <h1>GBF 加速器 局域网配置</h1>
        <p>移动设备（iOS / Android）配置指引：</p>
        <ol>
            <li><b>安装根证书：</b><br>
                <a href="/ca.crt" class="btn">下载根证书 (ca.crt)</a>
            </li>
            <li><b>信任证书（iOS 必做）：</b><br>
                系统【设置】&rarr;【通用】&rarr;【关于本机】&rarr;【证书信任设置】，找到 <b>GBF Local Accelerator Root CA</b> 并开启完全信任。
            </li>
            <li><b>配置 Wi-Fi 代理：</b><br>
                系统 Wi-Fi 设置 &rarr; 当前 Wi-Fi 详情 &rarr;【配置代理】：<br>
                &bull; <b>方式 1（自动，推荐）：</b>选择“自动”，URL 填入：<code>http://{lan_ip}:{LISTEN_PORT}/proxy.pac</code><br>
                &bull; <b>方式 2（手动）：</b>服务器填 <code>{lan_ip}</code>，端口填 <code>{LISTEN_PORT}</code>
            </li>
            <li><b>游玩网址与客户端说明：</b><br>
                建议使用手机浏览器（Safari / Chrome 等）访问：<br>
                <a href="https://game.granbluefantasy.jp" target="_blank" style="color: #0d6efd; font-weight: bold;">https://game.granbluefantasy.jp</a><br>
                <span style="font-size: 13px; color: #6c757d; display: inline-block; margin-top: 4px;">
                注：SkyLeap 内置使用的是 <code>gbf.game.mbga.jp</code>，该地址主要用于账号登录和跳转，不包含游戏静态素材，无法触发本地缓存加速；在手机浏览器中访问 <code>game.granbluefantasy.jp</code> 才能正常使用本地缓存。
                </span>
            </li>
        </ol>
    </div>
</body>
</html>"""
                html_bytes = html.encode("utf-8")
                landing_headers = {
                    "Content-Type": "text/html; charset=utf-8",
                    "Content-Length": str(len(html_bytes)),
                    "Access-Control-Allow-Origin": "*",
                    "Connection": "close",
                }
                await send_cached_response(writer, 200, "OK", landing_headers, html_bytes)
                await _safe_close_writer(writer)
                return

            # 4. Block plain HTTP telemetry requests
            if any(pat in target for pat in TELEMETRY_PATTERNS):
                await send_cached_response(writer, 200, "OK", {"Content-Type": "application/json", "Access-Control-Allow-Origin": "*", "Content-Length": "2", "Connection": "close"}, b"{}")
                writer.close()
                await writer.wait_closed()
                return

            try:
                content_length = int(headers.get("content-length", 0))
                if content_length < 0 or content_length > 32 * 1024 * 1024:
                    raise ValueError
            except ValueError:
                writer.write(b"HTTP/1.1 413 Payload Too Large\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                await writer.drain()
                writer.close()
                return
            body = await reader.readexactly(content_length) if content_length > 0 else b""

            clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length")}
            clean_lower = target.split("?")[0].lower()
            if _is_gbf_akamai_host(target) or any(clean_lower.endswith(ext) for ext in STATIC_EXTENSIONS):
                resp = await request_asset(method, target, headers=clean_headers, content=body)
            else:
                resp, _ = await request_api(method, target, headers=clean_headers, content=body, path=target)
            await forward_upstream_response(writer, headers, resp, is_head=(method == "HEAD"))
            format_log(f"HTTP {resp.status_code}", "37", f"{method} {target}")
            writer.close()
            await writer.wait_closed()

    except Exception:
        try:
            writer.close()
        except Exception:
            pass

async def main():
    if config_manager.config.get("clean_zombies", True):
        kill_process_on_port(LISTEN_PORT)

    try:
        if sys.stdout is not None:
            print("=" * 65)
            print("   GBF Speed Proxy - 本地缓存与加速代理")
            print(f"   本地监听: http://{LISTEN_HOST}:{LISTEN_PORT}")
            print(f"   上游转发: {UPSTREAM_PROXY}")
            print(f"   静态缓存: {cache_manager.cache_base}")
            print("=" * 65)
    except Exception:
        pass

    # Suppress unsightly Proactor connection reset noise on Windows
    loop = asyncio.get_running_loop()
    def custom_exception_handler(l, ctx):
        exc = ctx.get("exception")
        if isinstance(exc, (ConnectionResetError, BrokenPipeError)):
            return
        l.default_exception_handler(ctx)
    loop.set_exception_handler(custom_exception_handler)

    await init_http_client()
    ssl_context = get_server_ssl_context()

    server = await asyncio.start_server(
        lambda r, w: client_handler(r, w, ssl_context),
        LISTEN_HOST,
        LISTEN_PORT,
    )

    global proxy_server_instance, prefetch_queue, prefetch_inflight
    proxy_server_instance = server
    PROXY_STATS["is_running"] = True
    PROXY_STATS["last_error"] = ""
    proxy_ready_event.set()

    # Prefetch workers + startup RAM warmup: both stay off the request path.
    # stop_proxy_thread cancels all tasks on this loop, which also retires the workers.
    prefetch_queue = asyncio.PriorityQueue()
    prefetch_inflight = set()
    for _ in range(PREFETCH_WORKERS):
        asyncio.create_task(prefetch_worker())
    if config_manager.config.get("enable_ram_warmup", True):
        asyncio.get_running_loop().run_in_executor(None, cache_manager.warm_ram_cache)

    format_log("READY", "32", "代理服务已成功启动！等待 GBF 请求接入...")

    try:
        async with server:
            await server.serve_forever()
    finally:
        PROXY_STATS["is_running"] = False
        proxy_ready_event.clear()
        await close_http_client()

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\n[*] GBF Speed Proxy 已经安全停止。")
