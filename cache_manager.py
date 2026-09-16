import os
import re
import json
import time
import hashlib
import mimetypes
import threading
from collections import OrderedDict
from pathlib import Path
from typing import Optional, Tuple, Dict, Any

from config_manager import config_manager, normalize_cache_dir

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

FALLBACK_SAFE_EXTENSIONS = frozenset({
    ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico",
    ".mp3", ".wav", ".ogg", ".m4a", ".mp4", ".webm",
    ".woff", ".woff2", ".ttf", ".otf",
    ".css",
})

class CacheManager:
    def __init__(self, cache_base_dir: Optional[Path] = None):
        raw_base = cache_base_dir or config_manager.get_effective_cache_dir(interactive=False)
        self.cache_base = normalize_cache_dir(raw_base)
        self.cache_base.mkdir(parents=True, exist_ok=True)

        # In-Memory Hot Cache (LRU)
        self._ram_lock = threading.Lock()
        self._ram_cache: OrderedDict[str, Tuple[Dict[str, str], bytes, int]] = OrderedDict()
        self._ram_cache_bytes: int = 0

        # Bounded negative cache for missing files (skips repetitive slow Windows stat/is_file checks)
        self._missing_lock = threading.Lock()
        self._known_missing: OrderedDict[str, None] = OrderedDict()

        # Cached version directory names for fast cross-version fallback (< 0.1ms)
        self._version_dirs_lock = threading.Lock()
        self._cached_version_dirs: Dict[str, list] = {}
        self._cached_version_dirs_ts: Dict[str, float] = {}

    def _mark_missing(self, clean_key: str):
        with self._missing_lock:
            self._known_missing[clean_key] = None
            if len(self._known_missing) > 4096:
                self._known_missing.popitem(last=False)

    def _get_max_item_bytes(self) -> int:
        """Per-item RAM admission cap. Admission is otherwise "first touch wins":
        any asset is promoted to RAM on its first access, LRU keeps the hot ones.
        Relaxed from 5MB to 16MB so BGM / large art also stay in RAM; shrinks with
        a small budget so one big file can't monopolize it."""
        return min(16 * 1024 * 1024, max(4 * 1024 * 1024, self._get_max_ram_bytes() // 4))

    def set_cache_base(self, path: Path):
        norm_path = normalize_cache_dir(path)
        self.cache_base = norm_path
        self.cache_base.mkdir(parents=True, exist_ok=True)
        self.clear_ram_cache()
        with self._missing_lock:
            self._known_missing.clear()
        with self._version_dirs_lock:
            self._cached_version_dirs.clear()
            self._cached_version_dirs_ts.clear()

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

    def store_ram_cache(self, url_path: str, headers: Dict[str, str], data: bytes) -> bool:
        """Instantly store freshly downloaded static asset into the RAM cache (hot path).
        Runs synchronously on the asyncio event loop (< 2 microseconds), ensuring subsequent
        concurrent requests immediately hit RAM even before the disk write completes.
        """
        if not config_manager.config.get("enable_ram_cache", True):
            return False
        if not data or len(data) > self._get_max_item_bytes():
            return False

        clean_key = url_path.split("?")[0].lstrip("/")
        ct = headers.get("content-type") or headers.get("Content-Type", "")
        if not ct:
            suffix = Path(url_path.split("?")[0]).suffix.lower()
            ct = MIME_FALLBACKS.get(suffix) or mimetypes.guess_type(clean_key)[0] or "application/octet-stream"

        etag = headers.get("etag") or headers.get("ETag", "")
        if not etag:
            etag = f'"{int(time.time()):x}-{len(data):x}"'

        ce = headers.get("content-encoding") or headers.get("Content-Encoding", "")
        is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
        item_len = len(data)
        max_ram = self._get_max_ram_bytes()

        with self._ram_lock:
            if clean_key in self._ram_cache:
                _, _, old_sz = self._ram_cache.pop(clean_key)
                self._ram_cache_bytes -= old_sz

            while self._ram_cache and (self._ram_cache_bytes + item_len > max_ram):
                _, (_, _, evicted_size) = self._ram_cache.popitem(last=False)
                self._ram_cache_bytes -= evicted_size

            saved_headers = {
                "Content-Type": ct,
                "Content-Length": str(item_len),
                "Access-Control-Allow-Origin": "*",
                "ETag": etag,
            }
            if is_gzip:
                saved_headers["Content-Encoding"] = "gzip"

            self._ram_cache[clean_key] = (saved_headers, data, item_len)
            self._ram_cache_bytes += item_len
            return True

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

    def get_ram_cache(self, url_path: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        """Fast path: read directly from in-memory RAM cache synchronously (microseconds, zero executor)."""
        if not config_manager.config.get("enable_ram_cache", True):
            return None
        clean_key = url_path.split("?")[0].lstrip("/")
        with self._ram_lock:
            if clean_key in self._ram_cache:
                base_headers, data, _ = self._ram_cache[clean_key]
                self._ram_cache.move_to_end(clean_key)
                headers = dict(base_headers)
                self._apply_browser_cache_headers(headers, url_path)
                headers["X-Cache-Source"] = "RAM"
                return headers, data
        return None

    def get_disk_cache(self, url_path: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        """Slow path: read from local disk via thread pool executor and promote to RAM cache."""
        clean_key = url_path.split("?")[0].lstrip("/")
        enable_ram = config_manager.config.get("enable_ram_cache", True)
        enable_auto_repair = config_manager.config.get("enable_auto_repair", True)

        with self._missing_lock:
            if clean_key in self._known_missing:
                self._known_missing.move_to_end(clean_key)
                return None

        # Read from disk with path validation
        file_path = self._get_local_path(url_path)
        if file_path is None or not file_path.is_file():
            # Intelligent fallback 1: if path does not start with assets/, check under assets/
            if not clean_key.startswith("assets/"):
                fallback_path = self._get_local_path("assets/" + clean_key)
                if fallback_path is not None and fallback_path.is_file():
                    file_path = fallback_path
                else:
                    self._mark_missing(clean_key)
                    return None
            else:
                # Intelligent fallback 2: if path starts with assets/ but cache_base points directly to assets
                fallback_path = self._get_local_path(clean_key[7:].lstrip("/"))
                if fallback_path is not None and fallback_path.is_file():
                    file_path = fallback_path
                else:
                    self._mark_missing(clean_key)
                    return None

        # Auto-Repair: Detect and clean 0-byte broken files
        try:
            st = file_path.stat()
        except OSError:
            self._mark_missing(clean_key)
            return None

        if enable_auto_repair and st.st_size == 0:
            try:
                file_path.unlink(missing_ok=True)
                file_path.with_name(file_path.name + ".ext").unlink(missing_ok=True)
            except Exception:
                pass
            self._mark_missing(clean_key)
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

        # Quarantine legacy tampered set-error-handler.js if detected
        if clean_key.endswith("set-error-handler.js"):
            if self._check_and_quarantine_tampered_js(file_path):
                self._mark_missing(clean_key)
                return None

        try:
            with open(file_path, "rb") as f:
                data = f.read()

            if not data and enable_auto_repair:
                try:
                    file_path.unlink(missing_ok=True)
                    ext_path.unlink(missing_ok=True)
                except Exception:
                    pass
                self._mark_missing(clean_key)
                return None

            # Content Integrity Auto-Repair: Detect corrupt HTML error pages or malformed files
            if not self.is_valid_cache_content(url_path, {"content-type": content_type}, data):
                if enable_auto_repair:
                    try:
                        file_path.unlink(missing_ok=True)
                        ext_path.unlink(missing_ok=True)
                    except Exception:
                        pass
                self._mark_missing(clean_key)
                return None

            mtime = int(st.st_mtime)
            # Consistent ETag: prefer upstream ETag stored in .ext, fallback to mtime-size
            etag = cached_etag or f'"{mtime:x}-{len(data):x}"'
            headers = {
                "Content-Type": content_type,
                "Content-Length": str(len(data)),
                "Access-Control-Allow-Origin": "*",
                "ETag": etag,
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

    def get_cache(self, url_path: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        """Unified cache lookup (RAM first, fallback to disk)."""
        ram_hit = self.get_ram_cache(url_path)
        if ram_hit is not None:
            return ram_hit
        return self.get_disk_cache(url_path)

    def _is_fallback_safe_asset(self, clean_path: str) -> bool:
        """Strict whitelist: only pure media, styles, and CJS animation scripts are eligible for fallback.
        Core application logic (app.js, main.js, user models) is strictly excluded to prevent logic mismatch.
        """
        clean = clean_path.split("?")[0].lower()
        suffix = Path(clean).suffix
        if suffix in FALLBACK_SAFE_EXTENSIONS:
            return True
        if suffix == ".js":
            # Strictly allow only CreateJS animation timelines and model manifests
            if "/js/cjs/" in clean or "/js/model/manifest/" in clean:
                return True
        return False

    def _get_version_dirs(self, prefix: str = "assets") -> list:
        """Return cached version directories sorted descending (newest first). Refreshed at most once per 60s."""
        now = time.time()
        with self._version_dirs_lock:
            ts = self._cached_version_dirs_ts.get(prefix, 0.0)
            cached = self._cached_version_dirs.get(prefix)
            if (now - ts) < 60.0 and cached is not None:
                return cached

            try:
                target_dir = self.cache_base / prefix
                if not target_dir.is_dir():
                    if self.cache_base.name == prefix or self.cache_base.name == "https":
                        target_dir = self.cache_base / prefix if (self.cache_base / prefix).is_dir() else self.cache_base
                if target_dir.is_dir():
                    v_dirs = [d.name for d in target_dir.iterdir() if d.is_dir() and d.name.isdigit()]
                    v_dirs.sort(key=int, reverse=True)
                    self._cached_version_dirs[prefix] = v_dirs
                    self._cached_version_dirs_ts[prefix] = now
                    return v_dirs
            except Exception:
                pass
            return []

    def _read_fallback_file(self, candidate_url: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        """Read and validate a candidate fallback file from disk without caching into RAM."""
        file_path = self._get_local_path(candidate_url)
        clean_key = candidate_url.split("?")[0].lstrip("/")
        if file_path is None or not file_path.is_file():
            if not clean_key.startswith("assets/"):
                fallback_p = self._get_local_path("assets/" + clean_key)
                if fallback_p is not None and fallback_p.is_file():
                    file_path = fallback_p
                else:
                    return None
            else:
                fallback_p = self._get_local_path(clean_key[7:].lstrip("/"))
                if fallback_p is not None and fallback_p.is_file():
                    file_path = fallback_p
                else:
                    return None

        try:
            st = file_path.stat()
            if st.st_size == 0:
                return None
            with open(file_path, "rb") as f:
                data = f.read()
            if not data:
                return None

            ext_path = file_path.with_name(file_path.name + ".ext")
            content_type = ""
            cached_etag = ""
            if ext_path.is_file():
                try:
                    with open(ext_path, "r", encoding="utf-8") as f:
                        meta = json.load(f)
                        content_type = meta.get("ct", "")
                        cached_etag = meta.get("ETag", "")
                except Exception:
                    pass

            if not content_type:
                suffix = file_path.suffix.lower()
                content_type = MIME_FALLBACKS.get(suffix) or mimetypes.guess_type(file_path.name)[0] or "application/octet-stream"

            # Integrity check: reject HTML error pages or empty content
            if not self.is_valid_cache_content(candidate_url, {"content-type": content_type}, data):
                return None

            etag = cached_etag or f'"{int(st.st_mtime):x}-{len(data):x}"'
            headers = {
                "Content-Type": content_type,
                "Content-Length": str(len(data)),
                "Access-Control-Allow-Origin": "*",
                "ETag": etag,
                "Cache-Control": "public, max-age=60",
                "X-Proxy-Cache": "FALLBACK",
                "X-Cache-Source": "DISK-FALLBACK",
            }
            # Strictly verify gzip signature
            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if is_gzip:
                headers["Content-Encoding"] = "gzip"

            return headers, data
        except Exception:
            return None

    def get_fallback_cache(self, url_path: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        """Retrieve a stale or alternative valid cached asset when upstream fetch fails (timeout/5xx).
        Strictly restricted to pure media, styles, and CJS animation scripts to protect game logic.
        Provides cross-version fallback (e.g. /assets/<NEW_VER>/... -> /assets/<OLD_VER>/...)
        and cross-language fallback (/assets_en/... <-> /assets/...).
        Returns (headers, data) with short TTL (max-age=60) and X-Proxy-Fallback header.
        NEVER writes to the requested new path to prevent stale cache contamination.
        """
        if not config_manager.config.get("enable_cache_fallback", True):
            return None

        clean_path = url_path.split("?")[0]
        if not self._is_fallback_safe_asset(clean_path):
            return None

        # 1. First, check if exact file already exists on disk
        direct = self.get_disk_cache(url_path)
        if direct is not None:
            return direct

        # 2. Cross-version fallback for versioned assets: /(assets(?:_(?:en|jp))?)/(\d+)/(.+)
        m_ver = re.match(r"^/(assets(?:_(?:en|jp))?)/(\d+)/(.+)$", clean_path)
        if m_ver:
            prefix, req_ver, subpath = m_ver.group(1), m_ver.group(2), m_ver.group(3)
            # Try same prefix across recent versions (check up to top 8 versions)
            for v in self._get_version_dirs(prefix)[:8]:
                if v == req_ver:
                    continue
                candidate_url = f"/{prefix}/{v}/{subpath}"
                candidate_hit = self._read_fallback_file(candidate_url)
                if candidate_hit is not None:
                    headers, data = candidate_hit
                    headers["X-Proxy-Fallback"] = f"STALE-VERSION-{v}"
                    return headers, data

            # If not found and prefix is not standard assets, try assets across versions
            if prefix != "assets":
                for v in self._get_version_dirs("assets")[:8]:
                    candidate_url = f"/assets/{v}/{subpath}"
                    candidate_hit = self._read_fallback_file(candidate_url)
                    if candidate_hit is not None:
                        headers, data = candidate_hit
                        headers["X-Proxy-Fallback"] = f"STALE-LANG-VERSION-{v}"
                        return headers, data

        # 3. Cross-language fallback for unversioned assets (e.g. /assets_en/img/... <-> /assets/img/...)
        if clean_path.startswith("/assets_en/"):
            alt_url = "/assets/" + clean_path[len("/assets_en/"):]
            alt_hit = self._read_fallback_file(alt_url)
            if alt_hit is not None:
                headers, data = alt_hit
                headers["X-Proxy-Fallback"] = "CROSS-LANG-JP"
                return headers, data
        elif clean_path.startswith("/assets/"):
            alt_url = "/assets_en/" + clean_path[len("/assets/"):]
            alt_hit = self._read_fallback_file(alt_url)
            if alt_hit is not None:
                headers, data = alt_hit
                headers["X-Proxy-Fallback"] = "CROSS-LANG-EN"
                return headers, data

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
            else:
                file_path = self._get_local_path(clean_key[7:].lstrip("/"))
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
        else:
            return _exists(self._get_local_path(clean_key[7:].lstrip("/")))
        return False

    def warm_ram_cache(self, max_items: Optional[int] = None) -> int:
        """Startup warmup: preload high-frequency static assets (active versions, core UI,
        fonts, navigation, common SE) into the RAM cache so initial requests of a session
        never hit the disk. Runs in a background executor thread, never on the request path.
        Returns the number of items preloaded.
        """
        if not config_manager.config.get("enable_ram_cache", True):
            return 0
        if max_items is None:
            max_items = int(config_manager.config.get("ram_warmup_max_items", 1500))

        t0 = time.perf_counter()
        max_ram = self._get_max_ram_bytes()
        # Budget target: up to 70% of max RAM cache capacity, leaving headroom for runtime writes
        target_budget = int(max_ram * 0.70)

        candidates = []
        seen_paths = set()

        try:
            # 1. Target active version directories (top 1-2 newest versions only, skipping dead historical versions)
            active_version_targets = []
            for prefix in ("assets", "assets_en"):
                root_p = self.cache_base / prefix if (self.cache_base / prefix).is_dir() else (self.cache_base if self.cache_base.name.lower() == prefix else None)
                if not root_p or not root_p.is_dir():
                    continue
                v_dirs = self._get_version_dirs(prefix)
                for v in v_dirs[:2]:
                    vp = root_p / v
                    if vp.is_dir():
                        active_version_targets.append((0, 2 * 1024 * 1024, vp))

            # 2. Target high-frequency core directories with tiered priorities & file size limits:
            # Priority 0: Active version JS/CSS, web fonts
            # Priority 1: Core UI components (icons, frames, buttons), CSS backgrounds
            # Priority 2: Common sound effects, main navigation UI (submenu, top, mypage)
            targets = list(active_version_targets)
            for prefix in ("assets", "assets_en"):
                root_p = self.cache_base / prefix if (self.cache_base / prefix).is_dir() else (self.cache_base if self.cache_base.name.lower() == prefix else None)
                if not root_p or not root_p.is_dir():
                    continue
                if (root_p / "font").is_dir():
                    targets.append((0, 1024 * 1024, root_p / "font"))
                for sub in ("ui", "css_img"):
                    p = root_p / "img" / "sp" / sub
                    if p.is_dir():
                        targets.append((1, 512 * 1024, p))
                if (root_p / "sound" / "se").is_dir():
                    targets.append((2, 512 * 1024, root_p / "sound" / "se"))
                for sub in ("submenu", "top", "mypage"):
                    p = root_p / "img" / "sp" / sub
                    if p.is_dir():
                        targets.append((2, 512 * 1024, p))

            # 3. Collect candidates from targeted directories
            for prio, cap, target in targets:
                for root, _dirs, files in os.walk(target):
                    for name in files:
                        if name.endswith(".ext") or ".tmp." in name:
                            continue
                        fp = Path(root) / name
                        if fp in seen_paths:
                            continue
                        seen_paths.add(fp)
                        try:
                            sz = fp.stat().st_size
                            if 0 < sz <= cap:
                                candidates.append((prio, sz, fp))
                        except OSError:
                            continue

            # Fallback for non-standard, custom, or empty cache directories
            if not candidates:
                for root, _dirs, files in os.walk(self.cache_base):
                    root_lower = root.lower().replace("\\", "/")
                    if any(bad in root_lower for bad in ("/voice/", "/bgm/", "/comic/", "/test/")):
                        continue
                    for name in files:
                        if name.endswith(".ext") or ".tmp." in name:
                            continue
                        fp = Path(root) / name
                        try:
                            sz = fp.stat().st_size
                        except OSError:
                            continue
                        if 0 < sz <= 512 * 1024:
                            candidates.append((1, sz, fp))
                    if len(candidates) >= max_items:
                        break

            # Sort candidates by (priority asc, file size asc)
            candidates.sort(key=lambda t: (t[0], t[1]))
        except Exception:
            return 0

        loaded = 0
        loaded_bytes = 0
        for _prio, _sz, fp in candidates:
            if loaded >= max_items:
                break
            with self._ram_lock:
                if self._ram_cache_bytes >= target_budget or self._ram_cache_bytes >= max_ram:
                    break
            try:
                rel = "/" + fp.relative_to(self.cache_base).as_posix()
            except ValueError:
                continue

            # get_cache builds proper headers and inserts into the RAM cache itself
            hit = self.get_cache(rel)
            if hit is not None:
                loaded += 1
                loaded_bytes += len(hit[1])

        try:
            import gbf_proxy
            if hasattr(gbf_proxy, "format_log"):
                mb_loaded = loaded_bytes / (1024 * 1024)
                gbf_proxy.format_log(
                    "RAM-WARM", "32",
                    f"Prewarm complete: loaded {loaded:,} hot assets ({mb_loaded:.1f} MB) in {time.perf_counter() - t0:.2f}s"
                )
        except Exception:
            pass

        return loaded

    def is_valid_cache_content(self, url_path: str, headers: Dict[str, str], data: bytes) -> bool:
        """Verify that downloaded static asset is not truncated, empty, an HTML error page,
        or corrupted data whose Magic Bytes do not match the expected media format.
        """
        if not data or len(data) == 0:
            return False

        clean_lower = url_path.split("?")[0].lower()
        non_html_exts = (
            ".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp3", ".wav", ".webm",
            ".js", ".css", ".wasm", ".woff", ".woff2", ".ttf", ".otf", ".mp4"
        )
        if any(clean_lower.endswith(ext) for ext in non_html_exts):
            ct_lower = (headers.get("content-type") or headers.get("Content-Type", "")).lower()
            if "text/html" in ct_lower:
                return False

            # Decompress leading chunk if gzip compressed (safe streaming inspect)
            sample = data[:512]
            if len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b:
                try:
                    import zlib
                    decomp = zlib.decompressobj(16 + zlib.MAX_WBITS)
                    sample = decomp.decompress(data[:min(len(data), 1024)])
                    if not sample and len(data) > 0:
                        import gzip
                        sample = gzip.decompress(data)[:512]
                except Exception:
                    # Malformed gzip stream
                    return False

            sample_lower = sample.strip().lower()
            if sample_lower.startswith(b"<!doctype") or sample_lower.startswith(b"<html") or sample_lower.startswith(b"<head"):
                return False

            # Magic Bytes format verification for media assets (using unstripped binary sample)
            # 1. PNG: 89 50 4E 47 0D 0A 1A 0A
            if clean_lower.endswith(".png"):
                if not sample.startswith(b"\x89PNG\r\n\x1a\n"):
                    return False

            # 2. JPEG: FF D8 FF
            elif clean_lower.endswith((".jpg", ".jpeg")):
                if not sample.startswith(b"\xff\xd8\xff"):
                    return False

            # 3. WebP: RIFF....WEBP
            elif clean_lower.endswith(".webp"):
                if len(sample) < 12 or not (sample[:4] == b"RIFF" and sample[8:12] == b"WEBP"):
                    return False

            # 4. GIF: GIF87a or GIF89a
            elif clean_lower.endswith(".gif"):
                if not (sample.startswith(b"GIF87a") or sample.startswith(b"GIF89a")):
                    return False

            # 5. MP3: ID3 or frame syncword (FF FB / FF F3 / FF F2)
            elif clean_lower.endswith(".mp3"):
                is_id3 = sample.startswith(b"ID3")
                is_sync = len(sample) >= 2 and sample[0] == 0xff and (sample[1] & 0xe0) == 0xe0
                if not (is_id3 or is_sync):
                    return False

            # 6. WebFonts (WOFF2 / WOFF / TTF / OTF)
            elif clean_lower.endswith(".woff2"):
                if not sample.startswith(b"wOF2"):
                    return False
            elif clean_lower.endswith(".woff"):
                if not sample.startswith(b"wOFF"):
                    return False
            elif clean_lower.endswith(".ttf") or clean_lower.endswith(".otf"):
                if not (sample.startswith(b"\x00\x01\x00\x00") or sample.startswith(b"OTTO") or sample.startswith(b"true")):
                    return False

        return True

    def audit_and_repair_cache(self, progress_callback=None, cancel_event=None) -> Dict[str, Any]:
        """Audit all static cache files on disk. Detects and deletes 0-byte files,
        HTML error pages saved as static assets, and corrupted files whose Magic Bytes
        do not match their extension. Evicts any deleted items from memory.
        Runs in worker thread; invokes progress_callback(scanned, corrupted) periodically.
        Returns summary statistics dictionary.
        """
        start_t = time.perf_counter()
        scanned = 0
        healthy = 0
        corrupted = 0
        base = self.cache_base

        if not base.is_dir():
            return {"scanned": 0, "healthy": 0, "corrupted": 0, "elapsed": 0.0}

        cleaned_keys = []

        try:
            for root, _dirs, files in os.walk(base):
                if cancel_event and cancel_event.is_set():
                    break
                for fname in files:
                    if cancel_event and cancel_event.is_set():
                        break
                    if fname.endswith(".ext"):
                        continue

                    fp = Path(root) / fname

                    # Clean orphaned temporary files from interrupted writes (.tmp.)
                    if ".tmp." in fname:
                        scanned += 1
                        corrupted += 1
                        try:
                            fp.unlink(missing_ok=True)
                        except Exception:
                            pass
                        continue

                    scanned += 1
                    try:
                        st = fp.stat()
                    except OSError:
                        continue

                    # 1. Check for legacy tampered set-error-handler.js and quarantine it
                    if fname.lower().endswith("set-error-handler.js"):
                        if self._check_and_quarantine_tampered_js(fp):
                            corrupted += 1
                            try:
                                rel_k = "/" + fp.relative_to(base).as_posix()
                                cleaned_keys.append(rel_k.split("?")[0].lstrip("/"))
                            except Exception:
                                pass
                            continue

                    # 2. Check 0-byte corrupt files
                    is_bad = False
                    if st.st_size == 0:
                        is_bad = True
                    else:
                        clean_name = fname.lower()
                        if clean_name.endswith((
                            ".png", ".jpg", ".jpeg", ".gif", ".webp", ".mp3",
                            ".woff", ".woff2", ".ttf", ".otf", ".js", ".css"
                        )):
                            try:
                                with open(fp, "rb") as f:
                                    head_sample = f.read(1024)
                                if not self.is_valid_cache_content(fname, {}, head_sample):
                                    is_bad = True
                            except Exception:
                                is_bad = True

                    if is_bad:
                        corrupted += 1
                        try:
                            fp.unlink(missing_ok=True)
                            fp.with_name(fp.name + ".ext").unlink(missing_ok=True)
                            try:
                                rel_k = "/" + fp.relative_to(base).as_posix()
                                cleaned_keys.append(rel_k.split("?")[0].lstrip("/"))
                            except Exception:
                                pass
                        except Exception:
                            pass
                    else:
                        healthy += 1

                    if progress_callback and scanned % 1000 == 0:
                        try:
                            progress_callback(scanned, corrupted)
                        except Exception:
                            pass
        except Exception:
            pass

        # Evict cleaned keys from RAM cache & negative cache
        if cleaned_keys:
            with self._ram_lock:
                for k in cleaned_keys:
                    if k in self._ram_cache:
                        _, (_, _, sz) = self._ram_cache.pop(k, (None, (None, None, 0)))
                        self._ram_cache_bytes = max(0, self._ram_cache_bytes - sz)
            with self._missing_lock:
                for k in cleaned_keys:
                    self._known_missing.pop(k, None)

        if progress_callback:
            try:
                progress_callback(scanned, corrupted)
            except Exception:
                pass

        elapsed = round(time.perf_counter() - start_t, 2)
        return {
            "scanned": scanned,
            "healthy": healthy,
            "corrupted": corrupted,
            "elapsed": elapsed,
        }

    def _check_and_quarantine_tampered_js(self, file_path: Path) -> bool:
        """Detect legacy tampered set-error-handler.js (containing void 0 replacement),
        quarantine it to a backup file (.quarantine), and trigger safe re-fetch of upstream original.
        """
        if not file_path.is_file() or not file_path.name.endswith("set-error-handler.js"):
            return False
        try:
            with open(file_path, "rb") as f:
                data = f.read()
            if not data:
                return False
            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if is_gzip:
                import gzip
                raw_text = gzip.decompress(data).decode("utf-8", errors="ignore")
            else:
                raw_text = data.decode("utf-8", errors="ignore")

            # Exact legacy patch fingerprint check:
            # The historical 1.7.0 patch specifically replaced `t&&alert(t),a&&window.location.reload()`
            # inside error callback functions with `void 0`, creating `function(t,a){void 0}`.
            # We strictly match this exact hollowed-out function structure rather than loose "void 0"
            # tokens to completely prevent false positives on legitimate modern JS minification (e.g. `x === void 0`).
            has_hollowed_func_structure = bool(
                re.search(
                    r'(?:function(?:\s+[a-zA-Z0-9_]+)?\s*\([a-zA-Z0-9_,\s]*\)\s*|\([a-zA-Z0-9_,\s]*\)\s*=>\s*)\{\s*(?:void\s+0\s*;?|;?)\s*\}',
                    raw_text
                )
            )
            has_error_handler_signature = ("error" in raw_text.lower() or "onerror" in raw_text.lower())
            is_missing_original_handlers = ("window.location.reload" not in raw_text and "alert(" not in raw_text)

            if has_hollowed_func_structure and has_error_handler_signature and is_missing_original_handlers:
                ts = int(time.time())
                quarantine_target = file_path.with_name(f"{file_path.name}.quarantine.{ts}")
                file_path.rename(quarantine_target)
                ext_file = file_path.with_name(file_path.name + ".ext")
                if ext_file.is_file():
                    ext_file.rename(file_path.with_name(f"{file_path.name}.ext.quarantine.{ts}"))
                return True
        except Exception:
            pass
        return False

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
            self.store_ram_cache(url_path, headers, data)
            with self._missing_lock:
                self._known_missing.pop(clean_key, None)
            return True
        except Exception:
            return False

    def clear_all_cache(self) -> Tuple[int, int]:
        """Clear all in-memory hot cache items and wipe disk cache directory.
        Returns (deleted_files_count, freed_bytes).
        """
        self.clear_ram_cache()
        with self._missing_lock:
            self._known_missing.clear()
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

