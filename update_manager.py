import os
import re
import sys
import time
import threading
import zipfile
from dataclasses import dataclass
from pathlib import Path
from typing import Optional, Tuple, Dict, Any, Callable
import httpx

APP_VERSION = "1.7.0"
GITHUB_REPO = "Sagisawa/GBF-Accelerator"
RELEASES_API_URL = f"https://api.github.com/repos/{GITHUB_REPO}/releases/latest"

@dataclass
class UpdateInfo:
    has_update: bool
    latest_version: str
    current_version: str
    release_title: str = ""
    release_notes: str = ""
    html_url: str = ""
    download_url: Optional[str] = None
    published_at: str = ""
    error: Optional[str] = None

def parse_version(v: str) -> Tuple[int, ...]:
    """Parse semver string like 'v1.4.0', '1.4.1-rc1' into comparable tuple of ints (1, 4, 0)."""
    if not v:
        return (0, 0, 0)
    cleaned = v.strip().lstrip("vV")
    # Extract digit sequences separated by dots
    parts = []
    for segment in cleaned.split("."):
        m = re.match(r"^(\d+)", segment)
        if m:
            parts.append(int(m.group(1)))
        else:
            parts.append(0)
    # Ensure at least 3 components (major, minor, patch)
    while len(parts) < 3:
        parts.append(0)
    return tuple(parts)

def is_newer_version(remote: str, current: str = APP_VERSION) -> bool:
    """Return True if remote version is strictly greater than current version."""
    return parse_version(remote) > parse_version(current)

def check_for_updates(
    upstream_proxy: Optional[str] = None,
    timeout: float = 8.0,
    current_ver: str = APP_VERSION,
) -> UpdateInfo:
    """
    Check GitHub Releases for the latest version.
    Supports routing through configured upstream proxy (Clash / v2rayN)
    or direct connection. Falls back to direct if proxy fails.
    """
    headers = {
        "User-Agent": f"GBF-Accelerator/{current_ver}",
        "Accept": "application/vnd.github+json",
    }

    proxies_to_try = []
    if upstream_proxy and upstream_proxy.lower() not in ("auto", "none", "", "direct"):
        proxies_to_try.append(upstream_proxy)
    proxies_to_try.append(None)  # direct fallback

    last_error = ""
    data: Optional[Dict[str, Any]] = None

    for proxy in proxies_to_try:
        try:
            with httpx.Client(
                proxy=proxy,
                timeout=timeout,
                verify=True,
                follow_redirects=True,
                trust_env=False,
            ) as client:
                resp = client.get(RELEASES_API_URL, headers=headers)
                if resp.status_code == 200:
                    data = resp.json()
                    break
                elif resp.status_code == 403 and "rate limit" in resp.text.lower():
                    last_error = "GitHub API 访问频次受限，请稍后再试"
                else:
                    last_error = f"GitHub API 返回错误 HTTP {resp.status_code}"
        except httpx.TimeoutException:
            last_error = "连接 GitHub API 超时"
        except httpx.NetworkError as e:
            last_error = f"网络连接失败: {e}"
        except Exception as e:
            last_error = str(e)

    if not data:
        return UpdateInfo(
            has_update=False,
            latest_version=current_ver,
            current_version=current_ver,
            error=last_error or "无法获取更新信息",
        )

    tag_name = data.get("tag_name", "").strip()
    latest_ver = tag_name.lstrip("vV") if tag_name else current_ver
    release_title = data.get("name", "") or tag_name
    release_notes = data.get("body", "")
    html_url = data.get("html_url", "")
    published_at = data.get("published_at", "")[:10]  # YYYY-MM-DD

    # Find portable GUI zip or exe asset download URL
    download_url = None
    assets = data.get("assets", [])
    for asset in assets:
        name = asset.get("name", "")
        if name.endswith(".zip") and "GUI" in name:
            download_url = asset.get("browser_download_url")
            break
        elif name.endswith(".zip"):
            download_url = asset.get("browser_download_url")

    has_update = is_newer_version(latest_ver, current_ver)

    return UpdateInfo(
        has_update=has_update,
        latest_version=latest_ver,
        current_version=current_ver,
        release_title=release_title,
        release_notes=release_notes,
        html_url=html_url,
        download_url=download_url,
        published_at=published_at,
    )

def get_default_download_dir() -> Path:
    """Return standard user Downloads directory with home fallback."""
    try:
        downloads = Path.home() / "Downloads"
        if downloads.is_dir():
            return downloads
        downloads_cn = Path.home() / "下载"
        if downloads_cn.is_dir():
            return downloads_cn
    except Exception:
        pass
    return Path.home()

def get_asset_filename(url: str, fallback_version: str = "") -> str:
    """Derive filename from asset URL, falling back to a standard naming pattern."""
    if url:
        part = url.split("/")[-1].split("?")[0]
        if part.endswith(".zip"):
            return part
    ver = f"v{fallback_version}" if fallback_version else "latest"
    return f"GBF_Accelerator_{ver}_GUI.zip"

def _safe_unlink(p: Path):
    try:
        if p.is_file():
            p.unlink()
    except Exception:
        pass

def download_release_asset(
    url: str,
    dest_path: Path,
    upstream_proxy: Optional[str] = None,
    progress_cb: Optional[Callable[[int, int, float], None]] = None,
    cancel_event: Optional[threading.Event] = None,
    timeout: float = 30.0,
    chunk_size: int = 65536,
) -> Tuple[bool, str, Optional[Path]]:
    """
    Download a release asset with stream chunking, proxy-first fallback,
    progress reporting, cancellation check, and zip file integrity validation.
    Writes to dest_path with .part extension during download.
    Returns: (success, message, final_path)
    """
    if not url:
        return (False, "下载链接为空", None)

    dest_path = Path(dest_path).resolve()
    try:
        dest_path.parent.mkdir(parents=True, exist_ok=True)
    except Exception as e:
        return (False, f"无法创建保存目录: {e}", None)

    part_path = dest_path.with_name(dest_path.name + ".part")
    _safe_unlink(part_path)

    headers = {
        "User-Agent": f"GBF-Accelerator/{APP_VERSION}",
        "Accept": "application/octet-stream",
    }

    proxies_to_try = []
    if upstream_proxy and upstream_proxy.lower() not in ("auto", "none", "", "direct"):
        proxies_to_try.append(upstream_proxy)
    proxies_to_try.append(None)  # direct fallback

    last_error = ""

    for proxy in proxies_to_try:
        if cancel_event and cancel_event.is_set():
            _safe_unlink(part_path)
            return (False, "用户取消下载", None)

        try:
            with httpx.Client(
                proxy=proxy,
                timeout=timeout,
                verify=True,
                follow_redirects=True,
                trust_env=False,
            ) as client:
                with client.stream("GET", url, headers=headers) as resp:
                    if resp.status_code != 200:
                        last_error = f"服务器返回 HTTP {resp.status_code}"
                        continue

                    total_bytes = 0
                    try:
                        content_len = resp.headers.get("content-length")
                        if content_len:
                            total_bytes = int(content_len)
                    except Exception:
                        total_bytes = 0

                    downloaded_bytes = 0
                    t_start = time.perf_counter()
                    last_time = t_start
                    last_bytes = 0
                    speed = 0.0

                    with open(part_path, "wb") as f:
                        for chunk in resp.iter_bytes(chunk_size=chunk_size):
                            if cancel_event and cancel_event.is_set():
                                f.close()
                                _safe_unlink(part_path)
                                return (False, "用户取消下载", None)
                            if chunk:
                                f.write(chunk)
                                downloaded_bytes += len(chunk)
                                now = time.perf_counter()
                                if now - last_time >= 0.15:
                                    elapsed = now - last_time
                                    speed = (downloaded_bytes - last_bytes) / elapsed if elapsed > 0 else 0.0
                                    last_time = now
                                    last_bytes = downloaded_bytes
                                    if progress_cb:
                                        progress_cb(downloaded_bytes, total_bytes, speed)

                    # Finished streaming
                    if progress_cb:
                        progress_cb(downloaded_bytes, total_bytes, speed)

            # Successfully downloaded into part_path, now validate
            if not part_path.is_file() or part_path.stat().st_size == 0:
                last_error = "下载文件大小为 0"
                _safe_unlink(part_path)
                continue

            # Check zip file integrity
            if not zipfile.is_zipfile(part_path):
                last_error = "下载的文件非有效 zip 压缩包（可能是网络跳转错误页）"
                _safe_unlink(part_path)
                continue

            # Atomic replace
            _safe_unlink(dest_path)
            os.replace(part_path, dest_path)
            return (True, "下载完成", dest_path)

        except httpx.TimeoutException:
            last_error = "下载超时"
            _safe_unlink(part_path)
        except httpx.NetworkError as e:
            last_error = f"网络连接失败: {e}"
            _safe_unlink(part_path)
        except Exception as e:
            last_error = str(e)
            _safe_unlink(part_path)

    return (False, f"下载失败: {last_error or '未知网络错误'}", None)
