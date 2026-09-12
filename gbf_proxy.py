import asyncio
import ssl
import time
import sys
import urllib.parse
from pathlib import Path
from typing import Dict, Tuple, Optional

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
LISTEN_HOST = config_manager.config.get("listen_host", "127.0.0.1")
LISTEN_PORT = config_manager.get_listen_port()
UPSTREAM_PROXY = config_manager.get_effective_upstream_proxy()

# Host patterns to perform SSL MITM inspection & caching
MITM_SUFFIXES = (
    "granbluefantasy.jp",
    "granbluefantasy.com",
    "akamaized.net",
    "mbga.jp",
)

# Raw passthrough domains (no SSL MITM, direct low-latency TCP stream)
PASSTHROUGH_HOSTS = {
    "ws.game.granbluefantasy.jp",
}

# Endpoints to mock locally with 200 OK
MOCK_PATHS = (
    "/rest/error/js",
    "/user/nickname.woff",
    "/ob/r",
)

# Third-party telemetry, ad, and tracking domains to block/mock locally
TELEMETRY_PATTERNS = (
    "smbeat.jp",
    "smrtbeat.com",
    "rcv.a-i-ad.com",
    "datadoghq-browser-agent",
    "datadoghq.com",
    "spdmg-backend.i-mobile.co.jp",
    "creativecdn.com",
    "google-analytics.com",
    "googletagmanager.com",
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

# Global HTTP client pool for upstream requests through Clash
http_client: Optional[httpx.AsyncClient] = None

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

def run_proxy_in_thread():
    global proxy_loop, proxy_server_instance
    proxy_loop = asyncio.new_event_loop()
    asyncio.set_event_loop(proxy_loop)
    try:
        proxy_loop.run_until_complete(main())
    except (asyncio.CancelledError, KeyboardInterrupt):
        pass
    except Exception as e:
        PROXY_STATS["last_error"] = str(e)
    finally:
        PROXY_STATS["is_running"] = False

def start_proxy_thread():
    global proxy_thread
    PROXY_STATS["is_running"] = True
    if proxy_thread and proxy_thread.is_alive():
        return
    import threading
    proxy_thread = threading.Thread(target=run_proxy_in_thread, daemon=True)
    proxy_thread.start()

def stop_proxy_thread():
    global proxy_loop, proxy_server_instance, proxy_thread
    PROXY_STATS["is_running"] = False
    if proxy_server_instance:
        try:
            proxy_server_instance.close()
        except Exception:
            pass
    if proxy_loop and proxy_loop.is_running():
        try:
            for task in asyncio.all_tasks(proxy_loop):
                task.cancel()
            proxy_loop.call_soon_threadsafe(proxy_loop.stop)
        except Exception:
            pass
    if proxy_thread and proxy_thread.is_alive():
        try:
            proxy_thread.join(timeout=1.5)
        except Exception:
            pass
    proxy_thread = None

def format_log(level: str, color_code: str, msg: str):
    # ANSI colored console log (safe for windowed GUI mode)
    try:
        if sys.stdout is not None:
            print(f"[{level}] {msg}")
    except Exception:
        pass

async def init_http_client():
    global http_client
    verify_tls = config_manager.config.get("verify_upstream_tls", True)
    limits = httpx.Limits(max_keepalive_connections=50, max_connections=100, keepalive_expiry=60.0)
    http_client = httpx.AsyncClient(
        proxy=UPSTREAM_PROXY,
        verify=verify_tls,
        timeout=httpx.Timeout(15.0, connect=8.0),
        limits=limits,
        follow_redirects=False,
    )

async def close_http_client():
    global http_client
    if http_client:
        await http_client.aclose()

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
        # Connect to upstream proxy (Clash/v2rayN)
        parsed = urllib.parse.urlparse(UPSTREAM_PROXY)
        proxy_h = parsed.hostname or "127.0.0.1"
        proxy_p = parsed.port or 7897
        upstream_reader, upstream_writer = await asyncio.wait_for(
            asyncio.open_connection(proxy_h, proxy_p),
            timeout=8.0,
        )
        connect_req = f"CONNECT {target_host}:{target_port} HTTP/1.1\r\nHost: {target_host}:{target_port}\r\n\r\n"
        upstream_writer.write(connect_req.encode("ascii"))
        await upstream_writer.drain()

        # Read CONNECT response from Clash
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

        format_log("PASS-TCP", "36", f"Tunneling {target_host}:{target_port} directly via Clash")

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
    """Read and parse a full HTTP request with Chunked support and timeouts."""
    try:
        req_line = await asyncio.wait_for(reader.readline(), timeout=30.0)
    except (asyncio.TimeoutError, ConnectionResetError, OSError):
        return None

    if not req_line:
        return None

    try:
        line_str = req_line.decode("iso-8859-1").strip()
        parts = line_str.split()
        if len(parts) < 3:
            return None
        method, path, version = parts[0], parts[1], parts[2]
    except Exception:
        return None

    headers: Dict[str, str] = {}
    total_header_bytes = len(req_line)
    MAX_HEADERS_BYTES = 64 * 1024  # 64KB max header
    MAX_HEADER_COUNT = 128

    while True:
        try:
            header_line = await asyncio.wait_for(reader.readline(), timeout=15.0)
        except (asyncio.TimeoutError, ConnectionResetError, OSError):
            return None

        if not header_line or header_line in (b"\r\n", b"\n"):
            break

        total_header_bytes += len(header_line)
        if total_header_bytes > MAX_HEADERS_BYTES or len(headers) > MAX_HEADER_COUNT:
            return None

        try:
            h_str = header_line.decode("iso-8859-1").strip()
            if ":" in h_str:
                k, v = h_str.split(":", 1)
                headers[k.strip().lower()] = v.strip()
        except Exception:
            pass

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
):
    """Send HTTP response for local cache hits, mock endpoints, preflights, or synthetic errors."""
    filtered_headers: Dict[str, str] = {}
    for k, v in headers.items():
        k_lower = k.lower()
        if k_lower in ("content-length", "transfer-encoding", "connection"):
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
    writer.write(raw_header + body)
    await writer.drain()

async def forward_upstream_response(
    writer: asyncio.StreamWriter,
    client_headers: Dict[str, str],
    upstream_resp: httpx.Response,
) -> bool:
    """Forward dynamic API response strictly preserving original upstream headers without CORS tampering."""
    # Since httpx automatically decompresses content into upstream_resp.content,
    # content-encoding must be stripped so clients don't attempt double decompression.
    hop_by_hop = {"transfer-encoding", "trailer", "te", "upgrade", "content-encoding"}
    out_headers: Dict[str, str] = {}

    for k, v in upstream_resp.headers.items():
        k_lower = k.lower()
        if k_lower in hop_by_hop or k_lower == "content-length":
            continue
        out_headers[k_lower] = v

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

    raw_header = ("\r\n".join(res_lines) + "\r\n\r\n").encode("iso-8859-1")
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

            # ---------------- Rule 1: CORS OPTIONS ----------------
            if method.upper() == "OPTIONS":
                cors_headers = {
                    "Access-Control-Allow-Origin": "*",
                    "Access-Control-Allow-Methods": "GET, POST, PUT, DELETE, OPTIONS",
                    "Access-Control-Allow-Headers": "*",
                    "Access-Control-Max-Age": "604800",
                    "Connection": "keep-alive",
                }
                await send_cached_response(writer, 200, "OK", cors_headers, b"")
                format_log("OPTIONS", "35", f"CORS Preflight Mock -> {target_host}{path}")
                continue

            # ---------------- Rule 2: Mock Endpoints ----------------
            if target_host.endswith("granbluefantasy.jp") and path.startswith(MOCK_PATHS):
                mock_headers = {
                    "Content-Type": "application/json",
                    "Access-Control-Allow-Origin": "*",
                    "Connection": "keep-alive",
                }
                await send_cached_response(writer, 200, "OK", mock_headers, b'{"success":true}')
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
                    "akamaized.net" in target_host
                    or target_host.startswith("game-a")
                    or path.startswith(STATIC_PATH_PREFIXES)
                    or any(clean_path_lower.endswith(ext) for ext in STATIC_EXTENSIONS)
                )
            )

            if is_static:
                cache_hit = cache_manager.get_cache(path)
                if cache_hit:
                    c_headers, c_data = cache_hit
                    is_ram = c_headers.get("X-Cache-Source") == "RAM"
                    # Check 304 Not Modified from browser cache
                    req_etag = headers.get("if-none-match", "")
                    if req_etag and req_etag == c_headers.get("ETag"):
                        not_mod_headers = {
                            "ETag": c_headers["ETag"],
                            "Cache-Control": c_headers["Cache-Control"],
                            "Access-Control-Allow-Origin": "*",
                            "Connection": "keep-alive",
                        }
                        await send_cached_response(writer, 304, "Not Modified", not_mod_headers, b"")
                        PROXY_STATS["hits"] += 1
                        if is_ram:
                            PROXY_STATS["ram_hits"] += 1
                        format_log("304 HIT", "32", f"{'RAM' if is_ram else 'DISK'} 304 -> {path}")
                        continue

                    await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True)
                    PROXY_STATS["hits"] += 1
                    if is_ram:
                        PROXY_STATS["ram_hits"] += 1
                    format_log("CACHE HIT", "32", f"{'RAM' if is_ram else 'DISK'} -> {path} ({len(c_data):,} B)")
                    continue

                # Cache MISS: fetch via Clash, save & compress
                start_t = time.perf_counter()
                url = f"https://{target_host}{path}"
                clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length", "if-modified-since", "if-none-match")}
                try:
                    resp = await http_client.request(method, url, headers=clean_headers, content=body)
                except httpx.TimeoutException:
                    format_log("TIMEOUT", "31", f"Timeout fetching asset -> {url}")
                    err_body = b'{"error": "Upstream Gateway Timeout", "code": 504}'
                    await send_cached_response(writer, 504, "Gateway Timeout", {"Content-Type": "application/json"}, err_body)
                    continue
                except Exception as e:
                    format_log("ERROR", "31", f"Error fetching asset -> {url}: {e}")
                    err_body = b'{"error": "Bad Gateway", "code": 502}'
                    await send_cached_response(writer, 502, "Bad Gateway", {"Content-Type": "application/json"}, err_body)
                    continue

                elapsed_ms = int((time.perf_counter() - start_t) * 1000)

                if resp.status_code == 200 and resp.content:
                    cache_manager.save_cache(path, dict(resp.headers), resp.content)
                    PROXY_STATS["downloads"] += 1
                    format_log("DOWNLOAD", "34", f"FETCHED & CACHED ({elapsed_ms}ms) -> {path}")
                    verified_cache = cache_manager.get_cache(path)
                    if verified_cache:
                        c_headers, c_data = verified_cache
                        await send_cached_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True)
                        continue

                # Fallback if non-200 or unable to cache
                keep_alive = await forward_upstream_response(writer, headers, resp)
                if not keep_alive:
                    break
                continue

            # ---------------- Rule 4: Dynamic Game API (100% Pristine Forwarding) ----------------
            start_t = time.perf_counter()
            url = f"https://{target_host}{path}"
            clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length")}
            try:
                resp = await http_client.request(method, url, headers=clean_headers, content=body)
            except httpx.TimeoutException:
                format_log("TIMEOUT", "31", f"API Gateway Timeout -> {method} {path}")
                err_body = b'{"error": "Upstream API Gateway Timeout", "code": 504}'
                await send_cached_response(writer, 504, "Gateway Timeout", {"Content-Type": "application/json"}, err_body)
                continue
            except Exception as e:
                format_log("API-ERR", "31", f"API Forward Error -> {method} {path}: {e}")
                err_body = b'{"error": "Bad Gateway", "code": 502}'
                await send_cached_response(writer, 502, "Bad Gateway", {"Content-Type": "application/json"}, err_body)
                continue

            elapsed_ms = int((time.perf_counter() - start_t) * 1000)
            keep_alive = await forward_upstream_response(writer, headers, resp)
            PROXY_STATS["apis"] += 1

            # Highlight slow API responses (>300ms) or errors
            color = "31" if resp.status_code >= 400 else ("33" if elapsed_ms > 300 else "37")
            format_log(f"API {resp.status_code}", color, f"{method} {path} ({elapsed_ms}ms)")

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
            if ":" in target:
                host, port_str = target.split(":", 1)
                port = int(port_str)
            else:
                host, port = target, 443

            # Determine whether to MITM or Passthrough
            should_mitm = (
                host not in PASSTHROUGH_HOSTS
                and "analytics" not in host
                and any(host == s or host.endswith("." + s) for s in MITM_SUFFIXES)
            )

            if should_mitm:
                await handle_mitm_session(reader, writer, host, ssl_context)
            else:
                await handle_passthrough(reader, writer, host, port)
        else:
            # 1. Check if client is requesting the local PAC script
            if target == "/proxy.pac" or target.endswith("/proxy.pac"):
                from app_main import get_pac_content
                pac_bytes = get_pac_content(LISTEN_PORT).encode("utf-8")
                pac_headers = {
                    "Content-Type": "application/x-ns-proxy-autoconfig",
                    "Content-Length": str(len(pac_bytes)),
                    "Access-Control-Allow-Origin": "*",
                    "Cache-Control": "no-cache",
                    "Connection": "close",
                }
                await send_cached_response(writer, 200, "OK", pac_headers, pac_bytes)
                format_log("PAC", "36", f"Served /proxy.pac (port {LISTEN_PORT}) to browser/system")
                writer.close()
                await writer.wait_closed()
                return

            # 2. Block plain HTTP telemetry requests
            if any(pat in target for pat in TELEMETRY_PATTERNS):
                await send_cached_response(writer, 200, "OK", {"Content-Type": "application/json", "Access-Control-Allow-Origin": "*", "Content-Length": "2", "Connection": "close"}, b"{}")
                writer.close()
                await writer.wait_closed()
                return

            # Plain HTTP request (e.g. GET http://gbf.game.mbga.jp/...)
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

            content_length = int(headers.get("content-length", 0))
            body = await reader.readexactly(content_length) if content_length > 0 else b""

            clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length")}
            resp = await http_client.request(method, target, headers=clean_headers, content=body)
            await forward_upstream_response(writer, headers, resp)
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
            print("   GBF Speed Proxy - 本地极速缓存与加速代理")
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

    global proxy_server_instance
    proxy_server_instance = server
    PROXY_STATS["is_running"] = True
    PROXY_STATS["last_error"] = ""

    format_log("READY", "32", f"代理服务已成功启动！等待 GBF 请求接入...\n")

    try:
        async with server:
            await server.serve_forever()
    finally:
        PROXY_STATS["is_running"] = False
        await close_http_client()

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        print("\n[*] GBF Speed Proxy 已经安全停止。")
