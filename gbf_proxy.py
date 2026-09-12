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
LISTEN_PORT = int(config_manager.config.get("listen_port", 8124))
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

# In-memory Raid Socket URI cache: (path, uid) -> (timestamp, status, headers, body)
RAID_SOCKET_CACHE: Dict[Tuple[str, str], Tuple[float, int, Dict[str, str], bytes]] = {}
RAID_CACHE_TTL = 60.0  # seconds

# Global HTTP client pool for upstream requests through Clash
http_client: Optional[httpx.AsyncClient] = None

# Real-time statistics dictionary for GUI
PROXY_STATS = {
    "hits": 0,
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
    limits = httpx.Limits(max_keepalive_connections=50, max_connections=100, keepalive_expiry=60.0)
    http_client = httpx.AsyncClient(
        proxy=UPSTREAM_PROXY,
        verify=False,  # Skip upstream TLS verification on proxy to maximize speed
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
        upstream_reader, upstream_writer = await asyncio.open_connection(proxy_h, proxy_p)
        connect_req = f"CONNECT {target_host}:{target_port} HTTP/1.1\r\nHost: {target_host}:{target_port}\r\n\r\n"
        upstream_writer.write(connect_req.encode("ascii"))
        await upstream_writer.drain()

        # Read CONNECT response from Clash
        resp_line = await upstream_reader.readline()
        if b"200" not in resp_line:
            client_writer.write(b"HTTP/1.1 502 Bad Gateway\r\n\r\n")
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
    """Read and parse a full HTTP request from reader."""
    req_line = await reader.readline()
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
    while True:
        header_line = await reader.readline()
        if not header_line or header_line in (b"\r\n", b"\n"):
            break
        try:
            h_str = header_line.decode("iso-8859-1").strip()
            if ":" in h_str:
                k, v = h_str.split(":", 1)
                headers[k.strip().lower()] = v.strip()
        except Exception:
            pass

    body = b""
    content_length = int(headers.get("content-length", 0))
    if content_length > 0:
        body = await reader.readexactly(content_length)

    return method, path, version, headers, body

async def send_http_response(writer: asyncio.StreamWriter, status_code: int, status_text: str, headers: Dict[str, str], body: bytes, keep_content_encoding: bool = False):
    """Send HTTP response to client with clean, deduplicated header framing."""
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
    # Ensure strictly ONE valid CORS origin header to prevent W3C CORS duplicate violations
    filtered_headers["access-control-allow-origin"] = "*"

    res_lines = [f"HTTP/1.1 {status_code} {status_text}"]
    for k, v in filtered_headers.items():
        res_lines.append(f"{k}: {v}")

    raw_header = ("\r\n".join(res_lines) + "\r\n\r\n").encode("iso-8859-1")
    writer.write(raw_header + body)
    await writer.drain()

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
                await send_http_response(writer, 200, "OK", cors_headers, b"")
                format_log("OPTIONS", "35", f"CORS Preflight Mock -> {target_host}{path}")
                continue

            # ---------------- Rule 2: Mock Endpoints ----------------
            if target_host.endswith("granbluefantasy.jp") and path.startswith(MOCK_PATHS):
                mock_headers = {
                    "Content-Type": "application/json",
                    "Access-Control-Allow-Origin": "*",
                    "Connection": "keep-alive",
                }
                await send_http_response(writer, 200, "OK", mock_headers, b'{"success":true}')
                err_msg = ""
                if "error" in path and body:
                    try:
                        err_msg = f" (err: {body.decode('utf-8', errors='ignore')[:120]})"
                    except Exception:
                        pass
                format_log("MOCK-200", "33", f"Direct Mock -> {path}{err_msg}")
                continue

            # ---------------- Rule 3: Static Asset Cache (*.akamaized.net) ----------------
            if "akamaized.net" in target_host:
                cache_hit = cache_manager.get_cache(path)
                if cache_hit:
                    c_headers, c_data = cache_hit
                    await send_http_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True)
                    PROXY_STATS["hits"] += 1
                    format_log("0ms CACHE", "32", f"HIT {path} ({len(c_data):,} B)")
                    continue

                # Cache MISS: fetch via Clash, save & compress, then serve through verified cache pipeline
                start_t = time.perf_counter()
                url = f"https://{target_host}{path}"
                # Strip conditional headers so Akamai always returns full 200 OK body instead of 304
                clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length", "if-modified-since", "if-none-match")}
                resp = await http_client.request(method, url, headers=clean_headers, content=body)
                elapsed_ms = int((time.perf_counter() - start_t) * 1000)

                if resp.status_code == 200 and resp.content:
                    cache_manager.save_cache(path, dict(resp.headers), resp.content)
                    PROXY_STATS["downloads"] += 1
                    format_log("DOWNLOAD", "34", f"FETCHED & CACHED ({elapsed_ms}ms) -> {path}")
                    # Serve immediately from verified cache to ensure 100% consistent headers and compression
                    verified_cache = cache_manager.get_cache(path)
                    if verified_cache:
                        c_headers, c_data = verified_cache
                        await send_http_response(writer, 200, "OK", c_headers, c_data, keep_content_encoding=True)
                        continue

                # Fallback if non-200 or unable to cache
                resp_headers = dict(resp.headers)
                await send_http_response(writer, resp.status_code, resp.reason_phrase, resp_headers, resp.content)
                continue

            # ---------------- Rule 4: Raid Socket URI In-Memory Cache ----------------
            if path.startswith(("/socket/chat/raid/uri/raid", "/socket/uri/raid")):
                parsed_qs = urllib.parse.parse_qs(urllib.parse.urlparse(path).query)
                uid = parsed_qs.get("uid", [""])[0]
                clean_path = path.split("?")[0]
                cache_key = (clean_path, uid)

                now = time.time()
                if cache_key in RAID_SOCKET_CACHE:
                    cached_t, c_status, c_headers, c_body = RAID_SOCKET_CACHE[cache_key]
                    if now - cached_t < RAID_CACHE_TTL:
                        c_headers["Connection"] = "keep-alive"
                        c_headers["X-Raid-Cache"] = "HIT"
                        await send_http_response(writer, c_status, "OK", c_headers, c_body)
                        PROXY_STATS["hits"] += 1
                        format_log("RAID-CACHE", "32", f"RAM Socket HIT -> {path}")
                        continue

                # Fetch from upstream
                url = f"https://{target_host}{path}"
                clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length")}
                resp = await http_client.request(method, url, headers=clean_headers, content=body)
                resp_headers = dict(resp.headers)
                resp_headers["Connection"] = "keep-alive"
                await send_http_response(writer, resp.status_code, resp.reason_phrase, resp_headers, resp.content)

                if resp.status_code == 200:
                    RAID_SOCKET_CACHE[cache_key] = (now, resp.status_code, resp_headers, resp.content)
                    format_log("RAID-CACHE", "36", f"Updated RAM Socket -> {path}")
                continue

            # ---------------- Rule 5: Dynamic Game API ----------------
            start_t = time.perf_counter()
            url = f"https://{target_host}{path}"
            clean_headers = {k: v for k, v in headers.items() if k not in ("host", "content-length")}
            resp = await http_client.request(method, url, headers=clean_headers, content=body)
            elapsed_ms = int((time.perf_counter() - start_t) * 1000)

            resp_headers = dict(resp.headers)
            resp_headers["Connection"] = "keep-alive"
            await send_http_response(writer, resp.status_code, resp.reason_phrase, resp_headers, resp.content)
            PROXY_STATS["apis"] += 1

            # Highlight slow API responses (>300ms) or errors
            color = "31" if resp.status_code >= 400 else ("33" if elapsed_ms > 300 else "37")
            format_log(f"API {resp.status_code}", color, f"{method} {path} ({elapsed_ms}ms)")

        except (asyncio.CancelledError, ConnectionResetError, BrokenPipeError):
            break
        except Exception as e:
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
            resp_headers = dict(resp.headers)
            await send_http_response(writer, resp.status_code, resp.reason_phrase, resp_headers, resp.content)
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
