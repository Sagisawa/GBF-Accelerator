"""
GBF Accelerator - Control Plane Server
Provides a local REST API and SSE (Server-Sent Events) stream for Web Dashboard monitoring,
system status, configuration hot-reloading, cache maintenance, and proxy lifecycle control.

Key architectural properties:
1. Runs on an independent daemon thread with its own asyncio event loop, completely
   isolated from the proxy loop (stopping the proxy never terminates this control service).
2. Strictly bound to 127.0.0.1 by default (never exposed to external LAN).
3. Zero external heavy web framework dependencies (standard library asyncio & json).
4. Serves embedded / web/dist SPA assets with automatic client-side routing fallback.
"""

import asyncio
import json
import mimetypes
import os
import sys
import time
import threading
import urllib.parse
from pathlib import Path
from typing import Dict, Any, Optional, Tuple, List

from config_manager import (
    get_base_dir,
    get_resource_dir,
    config_manager,
    is_port_open,
)
from cache_manager import cache_manager
import gbf_proxy
from update_manager import APP_VERSION

# MIME types initialization
if not mimetypes.inited:
    mimetypes.init()
mimetypes.add_type("application/javascript", ".js")
mimetypes.add_type("text/css", ".css")
mimetypes.add_type("image/svg+xml", ".svg")
mimetypes.add_type("application/json", ".json")

# Global thread and loop state for the control server
_control_thread: Optional[threading.Thread] = None
_control_loop: Optional[asyncio.AbstractEventLoop] = None
_control_server: Optional[asyncio.AbstractServer] = None
_control_ready_event = threading.Event()
_control_lock = threading.Lock()

# SSE client tracking
_sse_clients: List[asyncio.Queue] = []
_sse_lock = threading.Lock()

# Background task state tracking (e.g. cache audit/slim)
_bg_tasks: Dict[str, Dict[str, Any]] = {}
_bg_tasks_lock = threading.Lock()

START_TIME = time.time()


def _get_web_dist_dir() -> Optional[Path]:
    """Locate web/dist directory in base dir, resource dir, or next to source."""
    candidates = [
        get_base_dir() / "web" / "dist",
        get_resource_dir() / "web" / "dist",
        Path(__file__).parent / "web" / "dist",
    ]
    for c in candidates:
        if c.is_dir():
            return c
    return None


def get_runtime_status() -> Dict[str, Any]:
    """Collect real-time status of proxy, config, active trackers, and system health."""
    proxy_running = bool(
        gbf_proxy.proxy_thread
        and gbf_proxy.proxy_thread.is_alive()
        and gbf_proxy.PROXY_STATS.get("is_running", False)
    )

    listen_host = config_manager.get_effective_listen_host()
    listen_port = config_manager.get_listen_port()
    control_port = config_manager.get_control_port()
    upstream = config_manager.get_effective_upstream_proxy()
    direct_mode = bool(config_manager.config.get("direct_mode", False))
    allow_lan = bool(config_manager.config.get("allow_lan", False))

    active_api = getattr(gbf_proxy, "ACTIVE_API_COUNT", 0)
    active_fg = getattr(gbf_proxy, "ACTIVE_FOREGROUND_ASSETS", 0)

    # Cache quick stats
    ram_items, ram_bytes = cache_manager.get_ram_cache_stats()

    stats = gbf_proxy.PROXY_STATS

    return {
        "version": APP_VERSION,
        "engine": "python",
        "proxy_running": proxy_running,
        "listen_host": listen_host,
        "listen_port": listen_port,
        "control_port": control_port,
        "upstream_proxy": upstream,
        "direct_mode": direct_mode,
        "allow_lan": allow_lan,
        "lan_ip": config_manager.get_lan_ip() if allow_lan else None,
        "active_api_count": active_api,
        "active_foreground_assets": active_fg,
        "uptime_seconds": round(time.time() - START_TIME, 1),
        "requests": {
            "total_apis": stats.get("apis", 0),
            "total_assets": stats.get("downloads", 0),
            "total_hits": stats.get("hits", 0),
            "ram_hits": stats.get("cache_ram_hit", 0) or stats.get("ram_hits", 0),
            "disk_hits": stats.get("cache_disk_hit", 0),
            "cache_misses": stats.get("cache_miss", 0),
            "prefetch_requests": stats.get("prefetch_asset_requests", 0),
            "prefetch_reused": stats.get("prefetch_reused", 0),
            "api_retries": stats.get("api_retry_count", 0),
        },
        "cache": {
            "ram_items": ram_items,
            "ram_mb": round(ram_bytes / (1024 * 1024), 2),
            "cache_dir": str(cache_manager.cache_base),
        },
        "last_error": stats.get("last_error", ""),
    }


def broadcast_event(event_type: str, data: Any):
    """Thread-safe event broadcast to all connected SSE clients."""
    with _sse_lock:
        if not _sse_clients or not _control_loop or not _control_loop.is_running():
            return
        msg = f"event: {event_type}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n"
        for q in list(_sse_clients):
            try:
                _control_loop.call_soon_threadsafe(q.put_nowait, msg)
            except Exception:
                pass


def _on_proxy_log(level: str, color_code: str, msg: str):
    """Callback registered with gbf_proxy to stream console logs via SSE."""
    broadcast_event("log", {
        "time": datetime_now_str(),
        "level": level,
        "msg": msg,
    })


def datetime_now_str() -> str:
    return time.strftime("%H:%M:%S", time.localtime())


# Hook into proxy log listener
gbf_proxy.register_log_listener(_on_proxy_log)


class ControlHttpHandler:
    """Lightweight HTTP/1.1 Handler for REST APIs and SPA Static Serving."""

    def __init__(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
        self.reader = reader
        self.writer = writer
        self.dist_dir = _get_web_dist_dir()

    async def handle(self):
        try:
            line = await self.reader.readline()
            if not line:
                return

            request_line = line.decode("utf-8", errors="replace").strip()
            parts = request_line.split()
            if len(parts) < 2:
                return
            method, raw_path = parts[0].upper(), parts[1]

            # Read headers
            headers = {}
            while True:
                h_line = await self.reader.readline()
                if not h_line or h_line == b"\r\n" or h_line == b"\n":
                    break
                h_str = h_line.decode("utf-8", errors="replace").strip()
                if ":" in h_str:
                    k, v = h_str.split(":", 1)
                    headers[k.strip().lower()] = v.strip()

            # Read request body if Content-Length is present
            body_bytes = b""
            content_length = int(headers.get("content-length", 0))
            if content_length > 0:
                body_bytes = await self.reader.readexactly(min(content_length, 10 * 1024 * 1024))

            # Dispatch
            await self.dispatch(method, raw_path, headers, body_bytes)
        except (asyncio.IncompleteReadError, ConnectionResetError):
            pass
        except Exception as e:
            try:
                await self.send_json(500, {"error": "Internal Server Error", "detail": str(e)})
            except Exception:
                pass
        finally:
            try:
                self.writer.close()
                await self.writer.wait_closed()
            except Exception:
                pass

    async def dispatch(self, method: str, raw_path: str, headers: Dict[str, str], body: bytes):
        parsed = urllib.parse.urlparse(raw_path)
        path = parsed.path
        query = urllib.parse.parse_qs(parsed.query)

        # CORS preflight
        if method == "OPTIONS":
            await self.send_cors_preflight()
            return

        # 1. API Endpoints
        if path.startswith("/api/"):
            await self.dispatch_api(method, path, query, headers, body)
            return

        # 2. Static Web Assets & SPA fallback
        if method == "GET" or method == "HEAD":
            await self.dispatch_static(path, method == "HEAD")
            return

        await self.send_json(405, {"error": f"Method {method} not allowed"})

    async def send_cors_preflight(self):
        cors_headers = (
            "HTTP/1.1 204 No Content\r\n"
            "Access-Control-Allow-Origin: *\r\n"
            "Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS\r\n"
            "Access-Control-Allow-Headers: Content-Type, Authorization, X-Requested-With\r\n"
            "Access-Control-Max-Age: 86400\r\n"
            "Connection: close\r\n\r\n"
        )
        self.writer.write(cors_headers.encode("utf-8"))
        await self.writer.drain()

    async def send_json(self, status: int, data: Any, extra_headers: Optional[Dict[str, str]] = None):
        body = json.dumps(data, ensure_ascii=False, indent=2).encode("utf-8")
        status_text = {200: "OK", 400: "Bad Request", 404: "Not Found", 405: "Method Not Allowed", 500: "Server Error"}.get(status, "Status")
        headers = [
            f"HTTP/1.1 {status} {status_text}",
            "Content-Type: application/json; charset=utf-8",
            f"Content-Length: {len(body)}",
            "Access-Control-Allow-Origin: *",
            "Cache-Control: no-store, no-cache, must-revalidate",
            "Connection: close",
        ]
        if extra_headers:
            for k, v in extra_headers.items():
                headers.append(f"{k}: {v}")

        header_bytes = ("\r\n".join(headers) + "\r\n\r\n").encode("utf-8")
        self.writer.write(header_bytes + body)
        await self.writer.drain()

    async def dispatch_api(self, method: str, path: str, query: Dict[str, list], headers: Dict[str, str], body: bytes):
        # 1. /api/status
        if path == "/api/status" and method == "GET":
            await self.send_json(200, get_runtime_status())
            return

        # 2. /api/proxy/start
        if path == "/api/proxy/start" and method == "POST":
            if not (gbf_proxy.proxy_thread and gbf_proxy.proxy_thread.is_alive()):
                gbf_proxy.start_proxy_thread()
            await asyncio.sleep(0.3)
            await self.send_json(200, {"ok": True, "message": "Proxy started", "status": get_runtime_status()})
            return

        # 3. /api/proxy/stop
        if path == "/api/proxy/stop" and method == "POST":
            if gbf_proxy.proxy_thread and gbf_proxy.proxy_thread.is_alive():
                gbf_proxy.stop_proxy_thread()
            await asyncio.sleep(0.2)
            await self.send_json(200, {"ok": True, "message": "Proxy stopped", "status": get_runtime_status()})
            return

        # 4. /api/config
        if path == "/api/config" and method == "GET":
            await self.send_json(200, {"ok": True, "config": config_manager.config})
            return

        # 5. /api/config/apply
        if path == "/api/config/apply" and method == "POST":
            try:
                new_cfg = json.loads(body.decode("utf-8") if body else "{}")
                if not isinstance(new_cfg, dict):
                    await self.send_json(400, {"error": "Config payload must be a JSON object"})
                    return
                if "port" in new_cfg and "listen_port" not in new_cfg:
                    new_cfg["listen_port"] = new_cfg["port"]
                for k, v in new_cfg.items():
                    if k in config_manager.config:
                        config_manager.config[k] = v
                if "listen_port" in new_cfg:
                    try:
                        p = int(new_cfg["listen_port"])
                        if 0 < p < 65536:
                            config_manager.config["listen_port"] = p
                            gbf_proxy.LISTEN_PORT = p
                    except (ValueError, TypeError):
                        pass
                config_manager.save_config()
                if "direct_mode" in new_cfg:
                    gbf_proxy.DIRECT_MODE = bool(new_cfg["direct_mode"])
                if "upstream_proxy" in new_cfg:
                    gbf_proxy.UPSTREAM_PROXY = config_manager.get_effective_upstream_proxy()
                if "cache_dir" in new_cfg:
                    from pathlib import Path
                    cache_manager.set_cache_base(Path(config_manager.get_effective_cache_dir()).resolve())
                await self.send_json(200, {"ok": True, "message": "Configuration updated", "config": config_manager.config})
            except Exception as e:
                await self.send_json(400, {"error": f"Failed to apply config: {e}"})
            return

        # 6. /api/cache/stats
        if path == "/api/cache/stats" and method == "GET":
            ram_items, ram_bytes = cache_manager.get_ram_cache_stats()
            stats = gbf_proxy.PROXY_STATS
            total_lookups = stats.get("hits", 0) + stats.get("cache_miss", 0)
            hit_ratio = round((stats.get("hits", 0) / total_lookups) * 100, 1) if total_lookups > 0 else 0.0
            await self.send_json(200, {
                "ok": True,
                "cache_base": str(cache_manager.cache_base),
                "ram_items": ram_items,
                "ram_bytes": ram_bytes,
                "ram_mb": round(ram_bytes / (1024 * 1024), 2),
                "ram_max_mb": config_manager.config.get("ram_cache_max_mb", 256),
                "hits_total": stats.get("hits", 0),
                "hits_ram": stats.get("cache_ram_hit", 0),
                "hits_disk": stats.get("cache_disk_hit", 0),
                "misses": stats.get("cache_miss", 0),
                "hit_ratio_percent": hit_ratio,
            })
            return

        # 7. /api/cache/clear
        if path == "/api/cache/clear" and method == "POST":
            ram_only = query.get("ram_only", ["false"])[0].lower() in ("true", "1")
            cache_manager.clear_ram_cache()
            disk_deleted, disk_freed = (0, 0)
            if not ram_only:
                disk_deleted, disk_freed = cache_manager.clear_all_cache()
            await self.send_json(200, {
                "ok": True,
                "ram_cleared": True,
                "disk_cleared": not ram_only,
                "disk_files_deleted": disk_deleted,
                "disk_bytes_freed": disk_freed,
            })
            return

        # 7b. /api/cache/open-folder
        if path == "/api/cache/open-folder" and method == "POST":
            from pathlib import Path
            folder = Path(cache_manager.cache_base).resolve()
            folder.mkdir(parents=True, exist_ok=True)
            if sys.platform == "win32":
                os.startfile(str(folder))
            elif sys.platform == "darwin":
                subprocess.Popen(["open", str(folder)])
            else:
                subprocess.Popen(["xdg-open", str(folder)])
            await self.send_json(200, {"ok": True, "path": str(folder)})
            return

        # 7c. /api/utils/browse-dir
        if path == "/api/utils/browse-dir" and method == "POST":
            chosen = ""
            if sys.platform == "win32":
                ps_cmd = (
                    "[System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms') | Out-Null; "
                    "$f = New-Object System.Windows.Forms.FolderBrowserDialog; "
                    "$f.Description = '选择 GBF 本地静态缓存保存目录'; "
                    "if ($f.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { Write-Output $f.SelectedPath }"
                )
                res = subprocess.run(
                    ["powershell", "-NoProfile", "-Command", ps_cmd],
                    capture_output=True,
                    text=True,
                    creationflags=0x08000000 if hasattr(subprocess, "CREATE_NO_WINDOW") else 0
                )
                chosen = res.stdout.strip()
            await self.send_json(200, {"ok": True, "path": chosen})
            return

        # 8. /api/cache/audit
        if path == "/api/cache/audit" and method == "POST":
            def run_audit_job():
                with _bg_tasks_lock:
                    _bg_tasks["audit"] = {"status": "running", "start_time": time.time()}
                try:
                    res = cache_manager.audit_and_repair_cache()
                    with _bg_tasks_lock:
                        _bg_tasks["audit"] = {"status": "completed", "result": res, "finish_time": time.time()}
                    broadcast_event("task_finished", {"task": "audit", "result": res})
                except Exception as ex:
                    with _bg_tasks_lock:
                        _bg_tasks["audit"] = {"status": "failed", "error": str(ex)}

            t = threading.Thread(target=run_audit_job, daemon=True)
            t.start()
            await self.send_json(200, {"ok": True, "message": "Cache audit started in background", "task": "audit"})
            return

        # 9. /api/cache/slim
        if path == "/api/cache/slim" and method == "POST":
            keep_count = int(query.get("keep", ["8"])[0])
            def run_slim_job():
                with _bg_tasks_lock:
                    _bg_tasks["slim"] = {"status": "running", "start_time": time.time()}
                try:
                    deleted_dirs, deleted_files, freed_bytes = cache_manager.prune_stale_version_cache(keep_count=keep_count)
                    res = {"deleted_dirs": deleted_dirs, "deleted_files": deleted_files, "freed_bytes": freed_bytes}
                    with _bg_tasks_lock:
                        _bg_tasks["slim"] = {"status": "completed", "result": res, "finish_time": time.time()}
                    broadcast_event("task_finished", {"task": "slim", "result": res})
                except Exception as ex:
                    with _bg_tasks_lock:
                        _bg_tasks["slim"] = {"status": "failed", "error": str(ex)}

            t = threading.Thread(target=run_slim_job, daemon=True)
            t.start()
            await self.send_json(200, {"ok": True, "message": "Cache slim started in background", "task": "slim"})
            return

        # 10. /api/prefetch/status
        if path == "/api/prefetch/status" and method == "GET":
            pq = getattr(gbf_proxy, "prefetch_queue", None)
            q_size = pq.qsize() if pq is not None else 0
            yielding = (getattr(gbf_proxy, "ACTIVE_API_COUNT", 0) > 0)
            stats = gbf_proxy.PROXY_STATS
            await self.send_json(200, {
                "ok": True,
                "enabled": config_manager.config.get("enable_prefetch", True),
                "queue_size": q_size,
                "is_yielding": yielding,
                "active_api_count": getattr(gbf_proxy, "ACTIVE_API_COUNT", 0),
                "prefetch_requests": stats.get("prefetch_asset_requests", 0),
                "prefetch_successes": stats.get("prefetch_asset_successes", 0),
                "prefetch_reused": stats.get("prefetch_reused", 0),
            })
            return

        # 11. /api/telemetry
        if path == "/api/telemetry" and method == "GET":
            summary = gbf_proxy.get_telemetry_summary()
            await self.send_json(200, {"ok": True, "telemetry": summary})
            return

        # 12. /api/logs
        if path == "/api/logs" and method == "GET":
            logs = gbf_proxy.get_recent_logs()
            await self.send_json(200, {"ok": True, "logs": logs[-100:]})
            return

        # 13. /api/events (SSE Stream)
        if path == "/api/events" and method == "GET":
            await self.handle_sse()
            return

        await self.send_json(404, {"error": f"API endpoint {path} not found"})

    async def handle_sse(self):
        """Handle persistent Server-Sent Events (SSE) stream for real-time Web dashboard."""
        sse_headers = (
            "HTTP/1.1 200 OK\r\n"
            "Content-Type: text/event-stream; charset=utf-8\r\n"
            "Cache-Control: no-cache, no-transform\r\n"
            "Connection: keep-alive\r\n"
            "Access-Control-Allow-Origin: *\r\n"
            "\r\n"
        )
        self.writer.write(sse_headers.encode("utf-8"))
        await self.writer.drain()

        client_q: asyncio.Queue = asyncio.Queue(maxsize=100)
        with _sse_lock:
            _sse_clients.append(client_q)

        try:
            status_data = get_runtime_status()
            init_msg = (
                f"event: status\ndata: {json.dumps(status_data, ensure_ascii=False)}\n\n"
            )
            self.writer.write(init_msg.encode("utf-8"))
            await self.writer.drain()

            recent_logs = gbf_proxy.get_recent_logs()
            if recent_logs:
                for l in recent_logs[-20:]:
                    self.writer.write(f"event: log\ndata: {json.dumps(l, ensure_ascii=False)}\n\n".encode("utf-8"))
                await self.writer.drain()

            last_heartbeat = time.time()

            while True:
                try:
                    msg = await asyncio.wait_for(client_q.get(), timeout=0.5)
                    self.writer.write(msg.encode("utf-8"))
                    await self.writer.drain()
                except asyncio.TimeoutError:
                    pass

                now = time.time()
                if now - last_heartbeat >= 1.0:
                    last_heartbeat = now
                    pulse_data = {
                        "time": datetime_now_str(),
                        "uptime": round(now - START_TIME, 1),
                        "active_api": getattr(gbf_proxy, "ACTIVE_API_COUNT", 0),
                        "active_fg": getattr(gbf_proxy, "ACTIVE_FOREGROUND_ASSETS", 0),
                        "telemetry": gbf_proxy.get_telemetry_summary(),
                        "hits": gbf_proxy.PROXY_STATS.get("hits", 0),
                        "misses": gbf_proxy.PROXY_STATS.get("cache_miss", 0),
                    }
                    pulse_msg = f"event: metrics\ndata: {json.dumps(pulse_data, ensure_ascii=False)}\n\n"
                    self.writer.write(pulse_msg.encode("utf-8"))
                    await self.writer.drain()

        except (ConnectionResetError, BrokenPipeError, asyncio.CancelledError):
            pass
        finally:
            with _sse_lock:
                if client_q in _sse_clients:
                    _sse_clients.remove(client_q)

    async def dispatch_static(self, path: str, head_only: bool = False):
        """Serve static files from web/dist or provide built-in fallback landing page."""
        clean_path = path.lstrip("/")
        file_to_serve: Optional[Path] = None

        if self.dist_dir and self.dist_dir.is_dir():
            candidate = (self.dist_dir / clean_path).resolve()
            if candidate.is_file() and str(candidate).startswith(str(self.dist_dir)):
                file_to_serve = candidate
            elif not clean_path or clean_path == "index.html":
                index_p = self.dist_dir / "index.html"
                if index_p.is_file():
                    file_to_serve = index_p
            elif "." not in clean_path:
                # SPA Route Fallback
                index_p = self.dist_dir / "index.html"
                if index_p.is_file():
                    file_to_serve = index_p

        if file_to_serve and file_to_serve.is_file():
            content_type, _ = mimetypes.guess_type(str(file_to_serve))
            content_type = content_type or "application/octet-stream"
            try:
                data = file_to_serve.read_bytes()
                cache_ctrl = "public, max-age=3600" if "assets" in clean_path else "no-cache"
                headers = [
                    "HTTP/1.1 200 OK",
                    f"Content-Type: {content_type}",
                    f"Content-Length: {len(data)}",
                    "Access-Control-Allow-Origin: *",
                    f"Cache-Control: {cache_ctrl}",
                    "Connection: close",
                ]
                header_bytes = ("\r\n".join(headers) + "\r\n\r\n").encode("utf-8")
                self.writer.write(header_bytes)
                if not head_only:
                    self.writer.write(data)
                await self.writer.drain()
                return
            except Exception:
                pass

        # If web/dist is not built yet, return built-in fallback page for root /
        if not clean_path or clean_path in ("index.html", "dashboard"):
            await self.send_fallback_landing_page(head_only)
            return

        await self.send_json(404, {"error": "File not found", "path": path})

    async def send_fallback_landing_page(self, head_only: bool = False):
        """Return a clean fallback landing page when web/dist is not yet built."""
        status = get_runtime_status()
        html = f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>GBF-Accelerator 控制台 (Control Plane)</title>
    <style>
        :root {{ --bg: #0f172a; --card: #1e293b; --text: #f8fafc; --accent: #38bdf8; --green: #22c55e; }}
        body {{ font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: var(--bg); color: var(--text); margin: 0; padding: 2rem; }}
        .container {{ max-width: 800px; margin: 0 auto; }}
        .card {{ background: var(--card); border-radius: 12px; padding: 1.5rem; margin-bottom: 1.5rem; border: 1px solid #334155; }}
        h1 {{ margin-top: 0; color: var(--accent); display: flex; align-items: center; gap: 0.5rem; font-size: 1.5rem; }}
        .badge {{ background: #15803d; color: white; padding: 0.2rem 0.6rem; border-radius: 9999px; font-size: 0.8rem; }}
        .grid {{ display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 1rem; margin-top: 1rem; }}
        .stat {{ background: #0f172a; padding: 1rem; border-radius: 8px; }}
        .stat-label {{ color: #94a3b8; font-size: 0.85rem; }}
        .stat-val {{ font-size: 1.25rem; font-weight: bold; color: #38bdf8; margin-top: 0.25rem; }}
        .api-list a {{ color: #38bdf8; text-decoration: none; display: block; margin: 0.3rem 0; font-family: monospace; }}
        .api-list a:hover {{ text-decoration: underline; }}
    </style>
</head>
<body>
    <div class="container">
        <div class="card">
            <h1>GBF-Accelerator 控制平面 <span class="badge">v{status['version']} 在线</span></h1>
            <p style="color: #94a3b8; font-size: 0.95rem;">
                本地独立控制服务已就绪（端口 127.0.0.1:{status['control_port']}）。Web 前端正在构建或可在开发模式访问。
            </p>
            <div class="grid">
                <div class="stat"><div class="stat-label">代理核心状态</div><div class="stat-val" style="color: {'#22c55e' if status['proxy_running'] else '#ef4444'}">{'● 正在运行' if status['proxy_running'] else '○ 已停止'}</div></div>
                <div class="stat"><div class="stat-label">游戏代理端口</div><div class="stat-val">{status['listen_port']}</div></div>
                <div class="stat"><div class="stat-label">上游代理模式</div><div class="stat-val">{'直连模式' if status['direct_mode'] else status['upstream_proxy']}</div></div>
                <div class="stat"><div class="stat-label">RAM 缓存热项</div><div class="stat-val">{status['cache']['ram_items']} 项 ({status['cache']['ram_mb']} MB)</div></div>
            </div>
        </div>

        <div class="card">
            <h3 style="margin-top: 0; color: #cbd5e1;">REST API 与 SSE 调试端点</h3>
            <div class="api-list">
                <a href="/api/status" target="_blank">GET /api/status - 系统运行状态</a>
                <a href="/api/config" target="_blank">GET /api/config - 全局配置参数</a>
                <a href="/api/cache/stats" target="_blank">GET /api/cache/stats - 缓存命中与分布统计</a>
                <a href="/api/prefetch/status" target="_blank">GET /api/prefetch/status - 预加载队列与动态避让</a>
                <a href="/api/telemetry" target="_blank">GET /api/telemetry - 连接复用与 P50/P95 延迟</a>
                <a href="/api/logs" target="_blank">GET /api/logs - 最近运行日志</a>
                <a href="/api/events" target="_blank">GET /api/events - 实时 SSE 事件流</a>
            </div>
        </div>
    </div>
</body>
</html>
"""
        data = html.encode("utf-8")
        headers = [
            "HTTP/1.1 200 OK",
            "Content-Type: text/html; charset=utf-8",
            f"Content-Length: {len(data)}",
            "Access-Control-Allow-Origin: *",
            "Cache-Control: no-cache",
            "Connection: close",
        ]
        header_bytes = ("\r\n".join(headers) + "\r\n\r\n").encode("utf-8")
        self.writer.write(header_bytes)
        if not head_only:
            self.writer.write(data)
        await self.writer.drain()


async def _client_connection_cb(reader: asyncio.StreamReader, writer: asyncio.StreamWriter):
    handler = ControlHttpHandler(reader, writer)
    await handler.handle()


def _run_control_server_loop(host: str, port: int):
    """Entry point for the isolated control server daemon thread."""
    global _control_loop, _control_server
    _control_loop = asyncio.new_event_loop()
    asyncio.set_event_loop(_control_loop)

    def _loop_exception_handler(loop, context):
        exc = context.get("exception")
        if isinstance(exc, OSError) and getattr(exc, "winerror", 0) in (995, 10054, 10038):
            return
        msg = str(context.get("message", ""))
        if "due to thread exit or application request" in msg or "已中止 I/O 操作" in msg:
            return
        try:
            loop.default_exception_handler(context)
        except Exception:
            pass

    _control_loop.set_exception_handler(_loop_exception_handler)

    async def start_server():
        global _control_server
        try:
            _control_server = await asyncio.start_server(_client_connection_cb, host, port)
            _control_ready_event.set()
            async with _control_server:
                await _control_server.serve_forever()
        except asyncio.CancelledError:
            pass
        except Exception as e:
            _control_ready_event.set()

    try:
        _control_loop.run_until_complete(start_server())
    finally:
        try:
            pending = asyncio.all_tasks(_control_loop)
            for task in pending:
                task.cancel()
            _control_loop.run_until_complete(asyncio.gather(*pending, return_exceptions=True))
            _control_loop.close()
        except Exception:
            pass
        _control_loop = None


def start_control_server(port: Optional[int] = None, host: str = "127.0.0.1", timeout: float = 3.0) -> bool:
    """Start the control server daemon thread on the specified host and port (default 127.0.0.1:8125)."""
    global _control_thread
    with _control_lock:
        if _control_thread and _control_thread.is_alive():
            return True

        target_port = port or config_manager.get_control_port()
        _control_ready_event.clear()

        _control_thread = threading.Thread(
            target=_run_control_server_loop,
            args=(host, target_port),
            name="ControlPlaneServerThread",
            daemon=True,
        )
        _control_thread.start()

    ready = _control_ready_event.wait(timeout=timeout)
    return ready and bool(_control_server and _control_server.is_serving())


def stop_control_server():
    """Stop the control server daemon thread cleanly."""
    global _control_thread, _control_loop, _control_server
    with _control_lock:
        if _control_server:
            try:
                _control_server.close()
            except Exception:
                pass
            _control_server = None

        if _control_loop and _control_loop.is_running():
            try:
                _control_loop.call_soon_threadsafe(_control_loop.stop)
            except Exception:
                pass

        if _control_thread and _control_thread.is_alive():
            try:
                _control_thread.join(timeout=2.0)
            except Exception:
                pass
            _control_thread = None

        _control_ready_event.clear()


def is_control_server_running() -> bool:
    return bool(
        _control_thread
        and _control_thread.is_alive()
        and _control_server
        and _control_server.is_serving()
    )


if __name__ == "__main__":
    print(f"[*] Starting GBF-Accelerator Control Server standalone on http://127.0.0.1:8125 ...")
    if start_control_server(port=8125):
        print(f"[+] Control server is running at http://127.0.0.1:8125")
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            print("\n[*] Stopping control server...")
            stop_control_server()
            print("[+] Stopped.")
    else:
        print("[!] Failed to start control server.")
