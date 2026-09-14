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

    def _get_max_item_bytes(self) -> int:
        """Per-item RAM admission cap. Admission is otherwise "first touch wins":
        any asset is promoted to RAM on its first access, LRU keeps the hot ones.
        Relaxed from 5MB to 16MB so BGM / large art also stay in RAM; shrinks with
        a small budget so one big file can't monopolize it."""
        return min(16 * 1024 * 1024, max(4 * 1024 * 1024, self._get_max_ram_bytes() // 4))

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
            mb = int(mb)
        except Exception:
            return 256 * 1024 * 1024
        # Clamp to a sane range so a typo in the GUI/config can't disable or explode the cache
        mb = max(16, min(mb, 8192))
        return mb * 1024 * 1024

    def enforce_ram_limit(self):
        """Immediately evict LRU items until RAM usage fits the configured cap
        (called after the user lowers the limit in the GUI)."""
        with self._ram_lock:
            max_ram = self._get_max_ram_bytes()
            while self._ram_cache and self._ram_cache_bytes > max_ram:
                _, (_, _, evicted_size) = self._ram_cache.popitem(last=False)
                self._ram_cache_bytes -= evicted_size

    def _get_local_path(self, url_path: str) -> Optional[Path]:
        """Safely compute the on-disk cache path for url_path, strictly preventing path traversal.
        Rejects Windows drive letters (e.g. C:), NTFS streams, and directory traversal (..)
        Ensures the resolved path is strictly inside self.cache_base.
        """
        clean = url_path.split("?")[0].lstrip("/\\")
        # Reject drive letters, UNC roots, and stream separators
        if ":" in clean or clean.startswith("\\"):
            return None

        import posixpath
        clean_norm = posixpath.normpath(clean)
        if clean_norm.startswith("..") or "/../" in clean_norm or clean_norm == ".":
            return None

        try:
            target_path = (self.cache_base / clean_norm).resolve()
            base_resolved = self.cache_base.resolve()
            if not target_path.is_relative_to(base_resolved):
                return None
            return target_path
        except Exception:
            return None

    def _apply_browser_cache_headers(self, headers: Dict[str, str], url_path: str = ""):
        """Inject or omit immutable cache headers according to user config.
        Crucial safety rule: ONLY inject immutable if the URL is confirmed to be versioned
        (e.g. contains numeric timestamp/hash like /assets/1772717316/ or /\d{8,}/).
        Never inject immutable into unversioned assets to prevent serving stale assets after updates.
        """
        enable_browser_cache = config_manager.config.get("enable_browser_cache", True)
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

        # 2. Read from disk with path validation
        file_path = self._get_local_path(url_path)
        if file_path is None or not file_path.is_file():
            # Intelligent fallback: if path does not start with assets/, check under assets/
            if not clean_key.startswith("assets/"):
                fallback_path = self._get_local_path("assets/" + clean_key)
                if fallback_path is not None and fallback_path.is_file():
                    file_path = fallback_path
                else:
                    return None
            else:
                return None

        # Auto-Repair: Detect and clean 0-byte broken files
        try:
            st = file_path.stat()
        except OSError:
            return None

        if enable_auto_repair and st.st_size == 0:
            try:
                file_path.unlink(missing_ok=True)
                file_path.with_name(file_path.name + ".ext").unlink(missing_ok=True)
            except Exception:
                pass
            return None

        ext_path = file_path.with_name(file_path.name + ".ext")
        content_type = ""
        content_encoding = ""
        cached_etag = ""

        if ext_path.is_file():
            try:
                with open(ext_path, "r", encoding="utf-8") as f:
                    meta = json.load(f)
                    content_type = meta.get("ct", "")
                    content_encoding = meta.get("ce", "")
                    cached_etag = meta.get("ETag", "")
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

            mtime = int(st.st_mtime)
            # Consistent ETag: prefer upstream ETag stored in .ext, fallback to mtime-size
            etag = cached_etag or f'"{mtime:x}-{len(data):x}"'
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
            if enable_ram and len(data) <= self._get_max_item_bytes():
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

    def peek_cache_meta(self, url_path: str) -> Optional[Tuple[str, Dict[str, str], bool]]:
        """Lightweight metadata-only lookup for fast 304 responses.
        Returns (etag, response_headers, is_ram) without reading the asset body,
        so a browser revalidation costs a stat + tiny .ext JSON read instead of a full file load.
        The ETag fallback mirrors get_cache exactly (mtime-size) to stay consistent.
        """
        clean_key = url_path.split("?")[0].lstrip("/")
        enable_ram = config_manager.config.get("enable_ram_cache", True)

        if enable_ram:
            with self._ram_lock:
                if clean_key in self._ram_cache:
                    etag = self._ram_cache[clean_key][0].get("ETag", "")
                    if etag:
                        headers = {"ETag": etag}
                        self._apply_browser_cache_headers(headers, url_path)
                        return etag, headers, True

        file_path = self._get_local_path(url_path)
        if file_path is None or not file_path.is_file():
            if not clean_key.startswith("assets/"):
                file_path = self._get_local_path("assets/" + clean_key)
            if file_path is None or not file_path.is_file():
                return None

        enable_auto_repair = config_manager.config.get("enable_auto_repair", True)
        try:
            st = file_path.stat()
            if enable_auto_repair and st.st_size == 0:
                try:
                    file_path.unlink(missing_ok=True)
                    file_path.with_name(file_path.name + ".ext").unlink(missing_ok=True)
                except Exception:
                    pass
                return None

            etag = ""
            ext_path = file_path.with_name(file_path.name + ".ext")
            if ext_path.is_file():
                try:
                    with open(ext_path, "r", encoding="utf-8") as f:
                        etag = json.load(f).get("ETag", "") or ""
                except Exception:
                    etag = ""
            if not etag:
                etag = f'"{int(st.st_mtime):x}-{st.st_size:x}"'

            headers = {"ETag": etag}
            self._apply_browser_cache_headers(headers, url_path)
            return etag, headers, False
        except OSError:
            return None

    def has_cache(self, url_path: str) -> bool:
        """Cheap existence check (RAM + disk stat only, no body read) used by the prefetcher."""
        clean_key = url_path.split("?")[0].lstrip("/")
        if config_manager.config.get("enable_ram_cache", True):
            with self._ram_lock:
                if clean_key in self._ram_cache:
                    return True

        def _exists(fp: Optional[Path]) -> bool:
            try:
                if fp is None or not fp.is_file():
                    return False
                if config_manager.config.get("enable_auto_repair", True) and fp.stat().st_size == 0:
                    return False
                return True
            except OSError:
                return False

        file_path = self._get_local_path(url_path)
        if _exists(file_path):
            return True
        if not clean_key.startswith("assets/"):
            return _exists(self._get_local_path("assets/" + clean_key))
        return False

    def warm_ram_cache(self, max_items: int = 2000) -> int:
        """Startup warmup: preload small high-frequency files (smallest first) into the RAM
        cache so the first requests of a session never hit the disk. Runs in a background
        executor thread, never on the request path. Returns the number of items preloaded.
        """
        if not config_manager.config.get("enable_ram_cache", True):
            return 0
        try:
            candidates = []
            for root, _dirs, files in os.walk(self.cache_base):
                for name in files:
                    if name.endswith(".ext") or ".tmp." in name:
                        continue
                    fp = Path(root) / name
                    try:
                        sz = fp.stat().st_size
                    except OSError:
                        continue
                    if 0 < sz <= self._get_max_item_bytes():
                        candidates.append((sz, fp))
            candidates.sort(key=lambda t: t[0])
        except Exception:
            return 0

        loaded = 0
        for _sz, fp in candidates:
            if loaded >= max_items:
                break
            with self._ram_lock:
                if self._ram_cache_bytes >= self._get_max_ram_bytes():
                    break
            try:
                rel = "/" + fp.relative_to(self.cache_base).as_posix()
            except ValueError:
                continue
            # get_cache builds proper headers and inserts into the RAM cache itself
            if self.get_cache(rel) is not None:
                loaded += 1
        return loaded

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

    def patch_error_handler(self, data: bytes) -> bytes:
        """Neuter disruptive alert() in set-error-handler.js without custom signatures."""
        try:
            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            import gzip
            raw_text = gzip.decompress(data).decode("utf-8") if is_gzip else data.decode("utf-8")
            target = "t&&alert(t),a&&window.location.reload()"
            if target in raw_text:
                raw_text = raw_text.replace(target, "void 0")
                return gzip.compress(raw_text.encode("utf-8"), 6) if is_gzip else raw_text.encode("utf-8")
        except Exception:
            pass
        return data

    def build_response_headers(self, url_path: str, upstream_headers: Dict[str, str], data_len: int, data: bytes) -> Dict[str, str]:
        """Construct client HTTP headers for freshly downloaded assets before background disk save."""
        ct = upstream_headers.get("content-type") or upstream_headers.get("Content-Type", "")
        if not ct:
            suffix = Path(url_path.split("?")[0]).suffix.lower()
            ct = MIME_FALLBACKS.get(suffix) or mimetypes.guess_type(url_path)[0] or "application/octet-stream"

        etag = upstream_headers.get("etag") or upstream_headers.get("ETag", "")
        if not etag:
            etag = f'"{int(time.time()):x}-{data_len:x}"'

        headers = {
            "Content-Type": ct,
            "Content-Length": str(data_len),
            "Access-Control-Allow-Origin": "*",
            "ETag": etag,
            "X-Proxy-Cache": "MISS-CACHED",
        }
        is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
        if is_gzip:
            headers["Content-Encoding"] = "gzip"

        self._apply_browser_cache_headers(headers, url_path)
        return headers

    def save_cache(self, url_path: str, headers: Dict[str, str], data: bytes) -> bool:
        if not self.is_valid_cache_content(url_path, headers, data):
            return False

        clean_key = url_path.split("?")[0].lstrip("/")
        enable_ram = config_manager.config.get("enable_ram_cache", True)

        try:
            file_path = self._get_local_path(url_path)
            if file_path is None:
                return False
            file_path.parent.mkdir(parents=True, exist_ok=True)

            ct = headers.get("content-type") or headers.get("Content-Type", "")
            ce = headers.get("content-encoding") or headers.get("Content-Encoding", "")
            etag = headers.get("etag") or headers.get("ETag", "")
            last_modified = headers.get("last-modified") or headers.get("Last-Modified", "")

            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if ce.lower() == "gzip" and not is_gzip:
                import gzip
                # Level 6: near-default ratio at a fraction of the CPU cost
                data = gzip.compress(data, 6)
                is_gzip = True

            # Neuter disruptive alert() in set-error-handler.js
            if url_path.endswith("set-error-handler.js"):
                data = self.patch_error_handler(data)
                is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b

            # Atomic file writing via temporary file and replace (without heavy fsync)
            pid = os.getpid()
            ts = int(time.time() * 1000)
            tmp_data_path = file_path.with_name(f"{file_path.name}.tmp.{pid}.{ts}")
            try:
                with open(tmp_data_path, "wb") as f:
                    f.write(data)
                    f.flush()
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
                os.replace(tmp_ext_path, ext_path)
            except Exception:
                if tmp_ext_path.is_file():
                    tmp_ext_path.unlink(missing_ok=True)

            # Also update RAM cache if enabled
            if enable_ram and len(data) <= self._get_max_item_bytes():
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

    def clear_all_cache(self) -> Tuple[int, int]:
        """Clear all in-memory hot cache items and wipe disk cache directory.
        Returns (deleted_files_count, freed_bytes).
        """
        self.clear_ram_cache()
        deleted_count = 0
        freed_bytes = 0
        if self.cache_base.is_dir():
            for root, dirs, files in os.walk(self.cache_base, topdown=False):
                for name in files:
                    fp = Path(root) / name
                    try:
                        sz = fp.stat().st_size
                        fp.unlink(missing_ok=True)
                        deleted_count += 1
                        freed_bytes += sz
                    except Exception:
                        pass
                for name in dirs:
                    dp = Path(root) / name
                    try:
                        dp.rmdir()
                    except Exception:
                        pass
        return deleted_count, freed_bytes

cache_manager = CacheManager()

