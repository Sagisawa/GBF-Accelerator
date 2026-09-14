import re
import sys
from dataclasses import dataclass
from typing import Optional, Tuple, Dict, Any
import httpx

APP_VERSION = "1.6.0"
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
