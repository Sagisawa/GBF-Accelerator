import os
import json
import time
import hashlib
import mimetypes
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

    def set_cache_base(self, path: Path):
        self.cache_base = path
        self.cache_base.mkdir(parents=True, exist_ok=True)

    def _get_local_path(self, url_path: str) -> Path:
        clean_path = url_path.split("?")[0].lstrip("/")
        # URL path typically starts with "assets/..."
        return self.cache_base / clean_path

    def get_cache(self, url_path: str) -> Optional[Tuple[Dict[str, str], bytes]]:
        file_path = self._get_local_path(url_path)
        if not file_path.is_file():
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

            mtime = int(file_path.stat().st_mtime)
            etag = f'"{mtime:x}-{len(data):x}"'
            headers = {
                "Content-Type": content_type,
                "Content-Length": str(len(data)),
                "Access-Control-Allow-Origin": "*",
                "Cache-Control": "public, max-age=31536000, immutable",
                "Expires": "Wed, 01 Jan 2038 00:00:00 GMT",
                "ETag": etag,
                "X-Proxy-Cache": "HIT",
            }
            # Strictly verify if data is genuinely gzip compressed before claiming Content-Encoding: gzip
            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if is_gzip:
                headers["Content-Encoding"] = "gzip"
            elif "content-encoding" in headers:
                del headers["content-encoding"]

            return headers, data
        except Exception:
            return None

    def save_cache(self, url_path: str, headers: Dict[str, str], data: bytes) -> bool:
        if not data:
            return False
        try:
            file_path = self._get_local_path(url_path)
            file_path.parent.mkdir(parents=True, exist_ok=True)

            ct = headers.get("content-type") or headers.get("Content-Type", "")
            ce = headers.get("content-encoding") or headers.get("Content-Encoding", "")
            etag = headers.get("etag") or headers.get("ETag", "")
            last_modified = headers.get("last-modified") or headers.get("Last-Modified", "")

            # If upstream was gzip or content is text/js/css, ensure we save it properly compressed
            is_gzip = len(data) >= 2 and data[0] == 0x1f and data[1] == 0x8b
            if ce.lower() == "gzip" and not is_gzip:
                import gzip
                data = gzip.compress(data)
                is_gzip = True

            # If saving set-error-handler.js, neuter the disruptive alert() popup automatically
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

            with open(file_path, "wb") as f:
                f.write(data)

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
            with open(ext_path, "w", encoding="utf-8") as f:
                json.dump(meta, f, indent=2)
            return True
        except Exception:
            return False

cache_manager = CacheManager()
