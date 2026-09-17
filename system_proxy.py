"""
GBF Accelerator - Windows System PAC Proxy Manager
Enables and disables Windows system-wide PAC proxy using WinINet API and registry.
Requires NO browser extensions - works out-of-the-box in Edge, Chrome, and Chromium browsers.
"""

import atexit
import ctypes
import sys
import winreg
from typing import Optional

INTERNET_SETTINGS_KEY = r"Software\Microsoft\Windows\CurrentVersion\Internet Settings"
INTERNET_OPTION_SETTINGS_CHANGED = 39
INTERNET_OPTION_REFRESH = 37

_original_pac_url: Optional[str] = None
_is_managing_proxy: bool = False

def notify_wininet():
    """Notify Windows and all active browsers that Internet Settings have changed."""
    if sys.platform != "win32":
        return
    try:
        wininet = ctypes.windll.wininet
        wininet.InternetSetOptionW(0, INTERNET_OPTION_SETTINGS_CHANGED, 0, 0)
        wininet.InternetSetOptionW(0, INTERNET_OPTION_REFRESH, 0, 0)
    except Exception:
        pass

def get_current_pac_url() -> Optional[str]:
    """Retrieve current Windows AutoConfigURL if configured."""
    if sys.platform != "win32":
        return None
    try:
        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, INTERNET_SETTINGS_KEY, 0, winreg.KEY_READ) as key:
            val, _ = winreg.QueryValueEx(key, "AutoConfigURL")
            return val if val else None
    except FileNotFoundError:
        return None
    except Exception:
        return None

def is_pac_proxy_enabled(port: Optional[int] = None) -> bool:
    """Check whether system proxy PAC is currently set to our local port."""
    url = get_current_pac_url()
    if not url:
        return False
    url_lower = url.lower()
    if port is not None:
        return f":{port}/proxy.pac" in url_lower and ("127.0.0.1" in url_lower or "localhost" in url_lower)
    return "/proxy.pac" in url_lower and ("127.0.0.1" in url_lower or "localhost" in url_lower)

def check_proxy_conflict(port: Optional[int] = None) -> Optional[str]:
    """Check if an external system proxy or external PAC script is configured in Windows.
    Returns a descriptive string of the conflict source if detected, or None if clear.
    """
    if sys.platform != "win32":
        return None
    try:
        conflicts = []
        pac_url = get_current_pac_url()
        external_pac = None
        if pac_url and not is_pac_proxy_enabled(port):
            external_pac = pac_url
        elif _original_pac_url and ("/proxy.pac" not in _original_pac_url.lower() or ("127.0.0.1" not in _original_pac_url.lower() and "localhost" not in _original_pac_url.lower())):
            external_pac = _original_pac_url

        if external_pac:
            conflicts.append(f"外部 PAC 脚本 ({external_pac})")

        try:
            with winreg.OpenKey(winreg.HKEY_CURRENT_USER, INTERNET_SETTINGS_KEY, 0, winreg.KEY_READ) as key:
                try:
                    proxy_enable, _ = winreg.QueryValueEx(key, "ProxyEnable")
                except FileNotFoundError:
                    proxy_enable = 0
                if proxy_enable == 1 or str(proxy_enable).strip() == "1":
                    try:
                        proxy_server, _ = winreg.QueryValueEx(key, "ProxyServer")
                    except FileNotFoundError:
                        proxy_server = ""
                    proxy_server_str = str(proxy_server).strip() if proxy_server else ""
                    if proxy_server_str:
                        conflicts.append(f"手动系统代理 ({proxy_server_str})")
                    else:
                        conflicts.append("手动系统代理")
        except FileNotFoundError:
            pass

        if conflicts:
            return "、".join(conflicts)
    except Exception:
        pass
    return None

def enable_pac_proxy(pac_url: str = "http://127.0.0.1:8124/proxy.pac") -> bool:
    """Enable Windows system PAC proxy pointing to local accelerator server."""
    global _original_pac_url, _is_managing_proxy
    if sys.platform != "win32":
        return False
    try:
        current = get_current_pac_url()
        is_our_pac = bool(current and ("/proxy.pac" in current and ("127.0.0.1" in current or "localhost" in current)))
        if current and not is_our_pac:
            _original_pac_url = current

        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, INTERNET_SETTINGS_KEY, 0, winreg.KEY_SET_VALUE) as key:
            winreg.SetValueEx(key, "AutoConfigURL", 0, winreg.REG_SZ, pac_url)

        notify_wininet()
        _is_managing_proxy = True
        return True
    except Exception as e:
        return False

def disable_pac_proxy(force: bool = False) -> bool:
    """Restore or remove Windows system PAC proxy.
    Safeguard: only remove/restore if this app actually enabled it or if current PAC points to our proxy.
    """
    global _original_pac_url, _is_managing_proxy
    if sys.platform != "win32":
        return False
    try:
        current = get_current_pac_url()
        is_our_pac = bool(current and ("/proxy.pac" in current and ("127.0.0.1" in current or "localhost" in current)))

        # Never touch external system proxy settings if we didn't set them and it doesn't point to us
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
    except Exception as e:
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
