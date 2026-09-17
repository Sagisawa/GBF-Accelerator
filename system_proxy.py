"""
GBF Accelerator - System PAC Proxy Manager
Enables and disables system-wide PAC proxy.
- Windows: Uses WinINet API and registry (AutoConfigURL).
- macOS: Uses native networksetup CLI commands across active network services.
Requires NO browser extensions - works out-of-the-box in Chrome, Safari, Edge, and Chromium browsers.
"""

import atexit
import os
import re
import subprocess
import sys
from typing import Optional, Dict, Tuple, List

# Windows registry constants
INTERNET_SETTINGS_KEY = r"Software\Microsoft\Windows\CurrentVersion\Internet Settings"
INTERNET_OPTION_SETTINGS_CHANGED = 39
INTERNET_OPTION_REFRESH = 37

_original_pac_url: Optional[str] = None
_mac_original_settings: Dict[str, Tuple[Optional[str], bool]] = {}
_is_managing_proxy: bool = False


# ================= Windows Implementation =================

def notify_wininet():
    """Notify Windows and all active browsers that Internet Settings have changed."""
    if sys.platform != "win32":
        return
    try:
        import ctypes
        wininet = ctypes.windll.wininet
        wininet.InternetSetOptionW(0, INTERNET_OPTION_SETTINGS_CHANGED, 0, 0)
        wininet.InternetSetOptionW(0, INTERNET_OPTION_REFRESH, 0, 0)
    except Exception:
        pass


def _win_get_current_pac_url() -> Optional[str]:
    try:
        import winreg
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, INTERNET_SETTINGS_KEY, 0, winreg.KEY_READ) as key:
            val, _ = winreg.QueryValueEx(key, "AutoConfigURL")
            return val if val else None
    except Exception:
        return None


def _win_enable_pac_proxy(pac_url: str) -> bool:
    global _original_pac_url, _is_managing_proxy
    try:
        import winreg
        current = _win_get_current_pac_url()
        is_our_pac = bool(current and ("/proxy.pac" in current and ("127.0.0.1" in current or "localhost" in current)))
        if current and not is_our_pac:
            _original_pac_url = current

        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, INTERNET_SETTINGS_KEY, 0, winreg.KEY_SET_VALUE) as key:
            winreg.SetValueEx(key, "AutoConfigURL", 0, winreg.REG_SZ, pac_url)

        notify_wininet()
        _is_managing_proxy = True
        return True
    except Exception:
        return False


def _win_disable_pac_proxy(force: bool = False) -> bool:
    global _original_pac_url, _is_managing_proxy
    try:
        import winreg
        current = _win_get_current_pac_url()
        is_our_pac = bool(current and ("/proxy.pac" in current and ("127.0.0.1" in current or "localhost" in current)))

        if not force and not _is_managing_proxy and not is_our_pac:
            return True

        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, INTERNET_SETTINGS_KEY, 0, winreg.KEY_SET_VALUE) as key:
            if _original_pac_url:
                winreg.SetValueEx(key, "AutoConfigURL", 0, winreg.REG_SZ, _original_pac_url)
                _original_pac_url = None
            else:
                try:
                    winreg.DeleteValue(key, "AutoConfigURL")
                except FileNotFoundError:
                    pass

        notify_wininet()
        _is_managing_proxy = False
        return True
    except Exception:
        return False


# ================= macOS Implementation =================

def _mac_get_services() -> List[str]:
    """Return all valid network services on macOS."""
    try:
        res = subprocess.run(
            ["networksetup", "-listallnetworkservices"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=2,
        )
        services = []
        for line in res.stdout.splitlines():
            s = line.strip()
            if not s or s.startswith("An asterisk") or s.startswith("*"):
                continue
            services.append(s)
        return services
    except Exception:
        return ["Wi-Fi"]


def _mac_get_primary_service() -> str:
    """Detect the active primary network service on macOS (e.g. 'Wi-Fi')."""
    try:
        res = subprocess.run(
            ["route", "-n", "get", "default"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=2,
        )
        dev = None
        for line in res.stdout.splitlines():
            if "interface:" in line:
                dev = line.split(":", 1)[1].strip()
                break
        if dev:
            order_res = subprocess.run(
                ["networksetup", "-listnetworkserviceorder"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=2,
            )
            cur_service = None
            for line in order_res.stdout.splitlines():
                m_svc = re.match(r"^\(\d+\)\s+(.+)$", line)
                if m_svc:
                    cur_service = m_svc.group(1).strip()
                elif cur_service and f"Device: {dev}" in line:
                    return cur_service
    except Exception:
        pass
    services = _mac_get_services()
    if "Wi-Fi" in services:
        return "Wi-Fi"
    return services[0] if services else "Wi-Fi"


def _mac_get_autoproxy_info(service: str) -> Tuple[Optional[str], bool]:
    """Return (pac_url, is_enabled) for the specified macOS network service."""
    try:
        res = subprocess.run(
            ["networksetup", "-getautoproxyurl", service],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=3,
        )
        if res.returncode != 0:
            return None, False
        url = None
        enabled = False
        for line in res.stdout.splitlines():
            if line.startswith("URL:"):
                val = line.split(":", 1)[1].strip()
                if val and val != "(null)":
                    url = val
            elif line.startswith("Enabled:"):
                enabled = (line.split(":", 1)[1].strip().lower() == "yes")
        return url, enabled
    except Exception:
        return None, False


def _mac_get_current_pac_url() -> Optional[str]:
    primary = _mac_get_primary_service()
    url, enabled = _mac_get_autoproxy_info(primary)
    if enabled and url:
        return url
    for svc in _mac_get_services():
        if svc == primary:
            continue
        u, en = _mac_get_autoproxy_info(svc)
        if en and u:
            return u
    return url


def _mac_enable_pac_proxy(pac_url: str) -> bool:
    global _mac_original_settings, _is_managing_proxy
    services = _mac_get_services()
    if not services:
        services = [_mac_get_primary_service()]

    success_any = False
    for svc in services:
        try:
            curr_url, curr_en = _mac_get_autoproxy_info(svc)
            is_our_pac = bool(curr_url and ("/proxy.pac" in curr_url and ("127.0.0.1" in curr_url or "localhost" in curr_url)))
            if svc not in _mac_original_settings and not is_our_pac:
                _mac_original_settings[svc] = (curr_url, curr_en)

            r1 = subprocess.run(
                ["networksetup", "-setautoproxyurl", svc, pac_url],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=3,
            )
            r2 = subprocess.run(
                ["networksetup", "-setautoproxystate", svc, "on"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=3,
            )
            if r1.returncode == 0 and r2.returncode == 0:
                success_any = True
        except Exception:
            pass

    if success_any:
        _is_managing_proxy = True
    return success_any


def _mac_disable_pac_proxy(force: bool = False) -> bool:
    global _mac_original_settings, _is_managing_proxy
    services = _mac_get_services()
    if not services:
        services = [_mac_get_primary_service()]

    success_all = True
    for svc in services:
        try:
            curr_url, curr_en = _mac_get_autoproxy_info(svc)
            is_our_pac = bool(curr_url and ("/proxy.pac" in curr_url and ("127.0.0.1" in curr_url or "localhost" in curr_url)))

            if not force and not _is_managing_proxy and not is_our_pac:
                continue

            orig = _mac_original_settings.pop(svc, None)
            if orig:
                orig_url, orig_en = orig
                if orig_url:
                    subprocess.run(
                        ["networksetup", "-setautoproxyurl", svc, orig_url],
                        stdout=subprocess.DEVNULL,
                        stderr=subprocess.DEVNULL,
                        timeout=3,
                    )
                state = "on" if orig_en else "off"
                subprocess.run(
                    ["networksetup", "-setautoproxystate", svc, state],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    timeout=3,
                )
            else:
                subprocess.run(
                    ["networksetup", "-setautoproxystate", svc, "off"],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    timeout=3,
                )
        except Exception:
            success_all = False

    _is_managing_proxy = False
    return success_all


# ================= Unified Public API =================

def get_current_pac_url() -> Optional[str]:
    """Retrieve current system AutoConfigURL if configured."""
    if sys.platform == "win32":
        return _win_get_current_pac_url()
    elif sys.platform == "darwin":
        return _mac_get_current_pac_url()
    return None


def is_pac_proxy_enabled(port: Optional[int] = None) -> bool:
    """Check whether system proxy PAC is currently set to our local port."""
    if sys.platform == "win32":
        url = _win_get_current_pac_url()
        if not url:
            return False
        if port is not None:
            return f":{port}/proxy.pac" in url
        return "/proxy.pac" in url and ("127.0.0.1" in url or "localhost" in url)
    elif sys.platform == "darwin":
        primary = _mac_get_primary_service()
        url, enabled = _mac_get_autoproxy_info(primary)
        if not enabled or not url:
            for svc in _mac_get_services():
                if svc == primary:
                    continue
                u, en = _mac_get_autoproxy_info(svc)
                if en and u:
                    url, enabled = u, en
                    break
        if not enabled or not url:
            return False
        if port is not None:
            return f":{port}/proxy.pac" in url
        return "/proxy.pac" in url and ("127.0.0.1" in url or "localhost" in url)
    return False


def enable_pac_proxy(pac_url: str = "http://127.0.0.1:8124/proxy.pac") -> bool:
    """Enable system PAC proxy pointing to local accelerator server."""
    if sys.platform == "win32":
        return _win_enable_pac_proxy(pac_url)
    elif sys.platform == "darwin":
        return _mac_enable_pac_proxy(pac_url)
    return False


def disable_pac_proxy(force: bool = False) -> bool:
    """Restore or remove system PAC proxy.
    Safeguard: only remove/restore if this app actually enabled it or if current PAC points to our proxy.
    """
    if sys.platform == "win32":
        return _win_disable_pac_proxy(force=force)
    elif sys.platform == "darwin":
        return _mac_disable_pac_proxy(force=force)
    return False


def cleanup_on_exit():
    """Safety cleanup hook to guarantee system proxy is restored on application termination."""
    if _is_managing_proxy:
        disable_pac_proxy()


atexit.register(cleanup_on_exit)

if __name__ == "__main__":
    print("Testing System Proxy Manager:")
    print("Current PAC URL:", get_current_pac_url())
    print("Enabling local PAC...")
    enable_pac_proxy()
    print("Enabled status:", is_pac_proxy_enabled(), "URL:", get_current_pac_url())
    print("Disabling PAC...")
    disable_pac_proxy()
    print("Final status:", is_pac_proxy_enabled(), "URL:", get_current_pac_url())
    print("Done!")

