import os
import re
import json
import time
import hashlib
import mimetypes
import threading
from collections import OrderedDict
from pathlib import Path
from typing import Optional, Tuple, Dict

from config_manager import config_manager

MIME_FALLBACKS = {
    ".png": "image/png",
    ".jpg": "image/jpeg",
    ".jpeg": "image/jpeg",
    ".gif": "image/gif",
    ".mp3": "audio/mpeg",
    ".wav": "audio/wav",
    ".js": "application/javascript",
    ".css": "text/css; charset=UTF-8",
    ".woff": "font/woff",
    ".woff2": "font/woff2",
    ".ttf": "font/ttf",
    ".json": "application/json",
    ".mp4": "video/mp4",
}

class CacheManager:
    def __init__(self, cache_base_dir: Optional[Path] = None):
        self.cache_base = cache_base_dir or config_manager.get_effective_cache_dir(interactive=False)
        self.cache_base.mkdir(parents=True, exist_ok=True)

        # In-Memory Hot Cache (LRU)
        self._ram_lock = threading.Lock()
        self._ram_cache: OrderedDict[str, Tuple[Dict[str, str], bytes, int]] = OrderedDict()
        self._ram_cache_bytes: int = 0
        self._max_item_bytes: int = 5 * 1024 * 1024  # Don't cache assets > 5MB in RAM

    def set_cache_base(self, path: Path):
        self.cache_base = path
        self.cache_base.mkdir(parents=True, exist_ok=True)
        self.clear_ram_cache()

    def clear_ram_cache(self):
        """Clear all in-memory hot cache items."""
        with self._ram_lock:
            self._ram_cache.clear()
            self._ram_cache_bytes = 0

    def get_ram_cache_stats(self) -> Tuple[int, int]:
        """Return (item_count, total_bytes_used) in RAM cache."""
        with self._ram_lock:
            return len(self._ram_cache), self._ram_cache_bytes

    def _get_max_ram_bytes(self) -> int:
        mb = config_manager.config.get("ram_cache_max_mb", 256)
        try:
            return int(mb) * 1024 * 1024
        except Exception:
            return 256 * 1024 * 1024

    def _get_local_path(self, url_path: str) -> Path:
        clean_path = url_path.split("?")[0].lstrip("/")
        return self.cache_base / clean_path

    def _apply_browser_cache_headers(self, headers: Dict[str, str], url_path: str = ""):
        """Inject or omit immutable cache headers according to user config.
        Crucial safety rule: ONLY inject immutable if the URL is confirmed to be versioned
        (e.g. contains numeric timestamp/hash like /assets/1772717316/ or /\d{8,}/).
        Never inject immutable into unversioned assets to prevent serving stale assets after updates.
        """
        enable_browser_cache = config_manager.config.get("enable_browser_cache", False)
        is_versioned = bool(re.search(r"/assets/\d+/", url_path) or re.search(r"/\d{8,}/", url_path))

        if enable_browser_cache and is_versioned:
            headers["Cache-Control"] = "public, max-age=31536000, immutable"
            headers["Expires"] = "Wed, 01 Jan 2038 00:00:00 GMT"
        else:
            headers["Cache-Control"] = "public, max-age=3600"
            if "Expires" in headers:
                del headers["Expires"]

    def get_cache(self, url_path: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        clean_key = url_path.split("?")[0].lstrip("/")
        enable_ram = config_manager.config.get("enable_ram_cache", True)
        enable_auto_repair = config_manager.config.get("enable_auto_repair", True)

        # 1. Try In-Memory Hot Cache (RAM Cache)
        if enable_ram:
            with self._ram_lock:
                if clean_key in self._ram_cache:
                    base_headers, data, _ = self._ram_cache[clean_key]
                    self._ram_cache.move_to_end(clean_key)
                    # Prepare response headers with dynamic browser cache settings
                    headers = dict(base_headers)
                    self._apply_browser_cache_headers(headers, url_path)
                    headers["X-Cache-Source"] = "RAM"
                    return headers, data

        # 2. Read from disk
        file_path = self._get_local_path(url_path)
        if not file_path.is_file():
            # Intelligent fallback: if path does not start with assets/, check under assets/
            if not clean_key.startswith("assets/"):
                fallback_path = self.cache_base / "assets" / clean_key
                if fallback_path.is_file():
                    file_path = fallback_path
                else:
                    return None
            else:
                return None

        # Auto-Repair: Detect and clean 0-byte broken files
        if enable_auto_repair:
            try:
                if file_path.stat().st_size == 0:
                    try:
                        file_path.unlink(missing_ok=True)
                        file_path.with_name(file_path.name + ".ext").unlink(missing_ok=True)
                    except Exception:
                        pass
                    return None
            except Exception:
                return None

        ext_path = file_path.with_name(file_path.name + ".ext")
        content_type = ""
        content_encoding = ""

        if ext_path.is_file():
            try:
                with open(ext_path, "r", encoding="utf-8") as f:
                    meta = json.load(f)
                    content_type = meta.get("ct", "")
                    content_encoding = meta.get("ce", "")
            except Exception:
                pass

        if not content_type:
            suffix = file_path.suffix.lower()
            content_type = MIME_FALLBACKS.get(suffix) or mimetypes.guess_type(file_path.name)[0] or "application/octet-stream"

        try:
            with open(file_path, "rb") as f:
                data = f.read()

            if not data and enable_auto_repair:
                try:
                    file_path.unlink(missing_ok=True)
                    ext_path.unlink(missing_ok=True)
                except Exception:
                    pass
                return None

            mtime = int(file_path.stat().st_mtime)
            etag = f'"{mtime:x}-{len(data):x}"'
            headers = {
                "Content-Type": content_type,
                "Content-Length": str(len(data)),
                "Access-Control-Allow-Origin": "*",
                "ETag": etag,
                "X-Proxy-Cache": "HIT",
                "X-Cache-Source": "DISK",
            }
            self._apply_browser_cache_headers(headers, url_path)

            # Strictly verify gzip signature
            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if is_gzip:
                headers["Content-Encoding"] = "gzip"
            elif "content-encoding" in headers:
                del headers["content-encoding"]

            # Store into RAM Cache for future instant reads
            if enable_ram and len(data) <= self._max_item_bytes:
                with self._ram_lock:
                    max_ram = self._get_max_ram_bytes()
                    item_len = len(data)
                    # Evict old items if needed
                    while self._ram_cache and (self._ram_cache_bytes + item_len > max_ram):
                        _, (_, _, evicted_size) = self._ram_cache.popitem(last=False)
                        self._ram_cache_bytes -= evicted_size

                    self._ram_cache[clean_key] = (dict(headers), data, item_len)
                    self._ram_cache_bytes += item_len

            return headers, data
        except Exception:
            return None

    def is_valid_cache_content(self, url_path: str, headers: Dict[str, str], data: bytes) -> bool:
        """Verify that downloaded static asset is not truncated, empty, or an HTML error page."""
        if not data or len(data) == 0:
            return False

        clean_lower = url_path.split("?")[0].lower()
        non_html_exts = (
            ".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp3", ".wav", ".webm",
            ".js", ".css", ".wasm", ".woff", ".woff2", ".ttf", ".mp4"
        )
        if any(clean_lower.endswith(ext) for ext in non_html_exts):
            ct_lower = (headers.get("content-type") or headers.get("Content-Type", "")).lower()
            if "text/html" in ct_lower:
                return False
            # Check leading bytes for HTML error page markup
            sample = data[:256].strip().lower()
            if sample.startswith(b"<!doctype") or sample.startswith(b"<html") or sample.startswith(b"<head"):
                return False

        return True

    def save_cache(self, url_path: str, headers: Dict[str, str], data: bytes) -> bool:
        if not self.is_valid_cache_content(url_path, headers, data):
            return False

        clean_key = url_path.split("?")[0].lstrip("/")
        enable_ram = config_manager.config.get("enable_ram_cache", True)

        try:
            file_path = self._get_local_path(url_path)
            file_path.parent.mkdir(parents=True, exist_ok=True)

            ct = headers.get("content-type") or headers.get("Content-Type", "")
            ce = headers.get("content-encoding") or headers.get("Content-Encoding", "")
            etag = headers.get("etag") or headers.get("ETag", "")
            last_modified = headers.get("last-modified") or headers.get("Last-Modified", "")

            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if ce.lower() == "gzip" and not is_gzip:
                import gzip
                data = gzip.compress(data)
                is_gzip = True

            # Neuter disruptive alert() in set-error-handler.js
            if url_path.endswith("set-error-handler.js"):
                try:
                    import gzip
                    raw_text = gzip.decompress(data).decode("utf-8") if is_gzip else data.decode("utf-8")
                    target = "t&&alert(t),a&&window.location.reload()"
                    if target in raw_text:
                        raw_text = raw_text.replace(target, 'console.warn("[SpeedProxy] Suppressed RequireJS error:",r)')
                        data = gzip.compress(raw_text.encode("utf-8"))
                        is_gzip = True
                except Exception:
                    pass

            # Atomic file writing via temporary file and fsync
            pid = os.getpid()
            ts = int(time.time() * 1000)
            tmp_data_path = file_path.with_name(f"{file_path.name}.tmp.{pid}.{ts}")
            try:
                with open(tmp_data_path, "wb") as f:
                    f.write(data)
                    f.flush()
                    os.fsync(f.fileno())
                os.replace(tmp_data_path, file_path)
            except Exception:
                if tmp_data_path.is_file():
                    tmp_data_path.unlink(missing_ok=True)
                return False

            ext_path = file_path.with_name(file_path.name + ".ext")
            meta = {
                "LastModified": last_modified,
                "ETag": etag,
                "at": int(time.time()),
                "md5": hashlib.md5(data).hexdigest(),
                "ce": "gzip" if is_gzip else "",
                "ct": ct,
                "v": 1
            }
            tmp_ext_path = ext_path.with_name(f"{ext_path.name}.tmp.{pid}.{ts}")
            try:
                with open(tmp_ext_path, "w", encoding="utf-8") as f:
                    json.dump(meta, f, indent=2)
                    f.flush()
                    os.fsync(f.fileno())
                os.replace(tmp_ext_path, ext_path)
            except Exception:
                if tmp_ext_path.is_file():
                    tmp_ext_path.unlink(missing_ok=True)

            # Also update RAM cache if enabled
            if enable_ram and len(data) <= self._max_item_bytes:
                item_len = len(data)
                max_ram = self._get_max_ram_bytes()
                with self._ram_lock:
                    while self._ram_cache and (self._ram_cache_bytes + item_len > max_ram):
                        _, (_, _, evicted_size) = self._ram_cache.popitem(last=False)
                        self._ram_cache_bytes -= evicted_size

                    saved_headers = {
                        "Content-Type": ct or MIME_FALLBACKS.get(file_path.suffix.lower(), "application/octet-stream"),
                        "Content-Length": str(item_len),
                        "Access-Control-Allow-Origin": "*",
                        "ETag": etag,
                        "X-Proxy-Cache": "HIT",
                    }
                    if is_gzip:
                        saved_headers["Content-Encoding"] = "gzip"
                    self._ram_cache[clean_key] = (saved_headers, data, item_len)
                    self._ram_cache_bytes += item_len

            return True
        except Exception:
            return False

cache_manager = CacheManager()
