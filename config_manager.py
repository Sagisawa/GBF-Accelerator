import json
import os
import re
import socket
import subprocess
import sys
import urllib.parse
from pathlib import Path
from typing import Optional, Dict, Any, List, Tuple

def get_base_dir() -> Path:
    """Return directory where executable or main script is located."""
    if getattr(sys, "frozen", False):
        # Running as PyInstaller bundled executable
        return Path(sys.executable).parent.resolve()
    return Path(__file__).parent.resolve()

CONFIG_FILE = get_base_dir() / "config.json"

DEFAULT_CONFIG: Dict[str, Any] = {
    "listen_host": "127.0.0.1",
    "listen_port": 8124,
    "allow_lan": False,        # Allow other devices on the same local network (LAN) to connect
    "upstream_proxy": "auto",  # auto-probe 7897, 7890, 10808, 10809
    "direct_mode": False,       # connect directly while retaining local cache
    "cache_dir": "auto",       # auto-detect ACGPower or use ./cache/gbf/https
    "clean_zombies": True,
    "auto_system_proxy": True, # Automatically mount PAC in Windows Internet Settings
    "auto_start": False,       # Start the GUI with Windows and minimize to tray
    "enable_ram_cache": True,  # In-memory LRU hot cache (fast RAM lookup)
    "ram_cache_max_mb": 256,   # Max RAM allocation for hot cache (in MB)
    "enable_browser_cache": True,  # Inject immutable only on versioned assets (safety guard built-in)
    "enable_auto_repair": True, # Auto-detect and clean 0-byte or corrupted cache files
    "enable_prefetch": True,   # Parse scene JS/JSON references and prefetch missing assets in background
    "enable_ram_warmup": True, # Preload small high-frequency files into RAM cache at startup
    "ram_warmup_max_items": 1500, # Max assets to preload during startup RAM warmup (fast ~1.5s SSD window)
    "verify_upstream_tls": True, # Upstream TLS certificate verification for security
    "shimakaze_mode": False,   # ShimakazeGo optimization mode (relaxed timeout, retry, self-signed CA)
    "auto_check_update": True, # Automatically check for newer releases on startup
    "api_max_connections": 16,     # Dynamic API dedicated connection pool limit
    "api_max_keepalive": 4,        # Max idle keep-alive connections for dynamic API (1/2/4/8)
    "api_keepalive_expiry": 20.0,  # Max seconds an idle API connection is kept alive
    "asset_max_connections": 100,  # Static asset & prefetch connection pool limit
    "asset_max_keepalive": 40,     # Max idle keep-alive connections for static assets
    "asset_keepalive_expiry": 60.0,# Max seconds an idle asset connection is kept alive
    "enable_api_telemetry": True,  # Track API latency percentiles and connection reuse rate
}

KNOWN_ACGPOWER_PATHS = [
    Path(r"D:\acgpower\cache\gbf\https"),
    Path(r"C:\acgpower\cache\gbf\https"),
    Path(r"E:\acgpower\cache\gbf\https"),
    Path(r"F:\acgpower\cache\gbf\https"),
]

PROBE_PROXY_PORTS = [
    (7897, "Clash Verge (Mixed Port)"),
    (7891, "Clash Verge / Mihomo (Mixed Port)"),
    (7890, "Clash Default (HTTP)"),
    (10808, "v2rayN (HTTP)"),
    (10809, "v2rayN (SOCKS/HTTP)"),
    (8099, "岛风 GO (HTTP)"),
]

def get_lan_ip() -> str:
    """Return the primary local IPv4 address (e.g. 192.168.x.x, 10.x.x.x) for LAN sharing."""
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        # 114.114.114.114 is a widely known public DNS; connect() on UDP does not transmit any packets,
        # but prompts the OS routing stack to determine the primary outbound adapter IP.
        s.connect(("114.114.114.114", 80))
        ip = s.getsockname()[0]
        s.close()
        if ip and not ip.startswith("127."):
            return ip
    except Exception:
        pass
    try:
        hostname = socket.gethostname()
        for ip in socket.gethostbyname_ex(hostname)[2]:
            if not ip.startswith("127.") and ":" not in ip:
                return ip
    except Exception:
        pass
    return "127.0.0.1"

def is_port_open(host: str, port: int, timeout: float = 0.3) -> bool:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except (socket.timeout, ConnectionRefusedError, OSError):
        return False

def detect_upstream_proxies() -> List[Tuple[str, str]]:
    """Return every reachable local proxy candidate as (url, display name)."""
    detected: List[Tuple[str, str]] = []
    for port, name in PROBE_PROXY_PORTS:
        if is_port_open("127.0.0.1", port):
            # Distinguish a pure SOCKS5 listener (common v2rayN 10809) from HTTP.
            # 岛风 GO 8099 is intentionally treated as HTTP.
            scheme = "http"
            if port != 8099:
                try:
                    with socket.create_connection(("127.0.0.1", port), timeout=0.4) as sock:
                        sock.settimeout(0.4)
                        sock.sendall(b"\x05\x01\x00")
                        if sock.recv(2) == b"\x05\x00":
                            scheme = "socks5"
                except (socket.timeout, ConnectionResetError, OSError):
                    pass
            detected.append((f"{scheme}://127.0.0.1:{port}", name))
    return detected


def auto_detect_upstream_proxy() -> str:
    """Probe common local proxy ports and return the first active proxy URL."""
    detected = detect_upstream_proxies()
    if detected:
        return detected[0][0]
    return "http://127.0.0.1:7897"  # Default fallback

def normalize_cache_dir(path: Any) -> Path:
    """Intelligently normalize cache directory downwards:
    If user selected an ACGPower root directory, 'cache', or 'cache/gbf',
    automatically resolve downward to the actual '.../cache/gbf/https' folder.
    For custom folders without ACGPower sub-structure, retains the user's choice untouched.
    """
    if not path:
        return (get_base_dir() / "cache" / "gbf" / "https").resolve()
    p = Path(path).resolve()
    if not p.is_dir():
        return p

    # 0. Foolproof: If user selected an 'assets' folder directly (e.g. D:\1111\assets or ...\https\assets),
    # where the parent directory is the actual cache root.
    if p.name.lower() == "assets" and not (p / "assets").is_dir():
        return p.parent.resolve()

    # 1. If 'assets' directory is already present, it is already the exact target root
    if (p / "assets").is_dir():
        return p

    # 2. Check downward subpaths for existing ACGPower structure
    # Case A: User selected .../cache/gbf
    if (p / "https" / "assets").is_dir() or (p.name.lower() == "gbf" and (p / "https").is_dir()):
        return (p / "https").resolve()

    # Case B: User selected .../cache
    if (p / "gbf" / "https" / "assets").is_dir() or (p.name.lower() == "cache" and (p / "gbf" / "https").is_dir()):
        return (p / "gbf" / "https").resolve()

    # Case C: User selected ACGPower root folder (e.g. D:\acgpower)
    if (p / "cache" / "gbf" / "https" / "assets").is_dir() or (
        (p / "cache" / "gbf" / "https").is_dir() and any((p / exe).is_file() for exe in ("ACGPower.exe", "acgpower.exe"))
    ):
        return (p / "cache" / "gbf" / "https").resolve()

    # Case D: Check if any general subfolder has assets
    if (p / "cache" / "gbf" / "https").is_dir():
        return (p / "cache" / "gbf" / "https").resolve()

    # Case E: Mac ACGPower structure (cache/gbf/assets) without 'https' subfolder
    if (p / "cache" / "gbf" / "assets").is_dir():
        return (p / "cache" / "gbf").resolve()
    if (p / "gbf" / "assets").is_dir() or (p.name.lower() == "cache" and (p / "gbf" / "assets").is_dir()):
        return (p / "gbf").resolve()

    return p

def _get_running_acgpower_path() -> Optional[Path]:
    """Lightweight check if ACGPower.exe is currently running.
    Uses kernel32 CreateToolhelp32Snapshot (pure ctypes, ~2ms, zero dependencies, no console window).
    """
    if sys.platform != "win32":
        return None
    try:
        import ctypes
        from ctypes import wintypes

        kernel32 = ctypes.windll.kernel32
        PROCESS_QUERY_LIMITED_INFORMATION = 0x1000

        class PROCESSENTRY32(ctypes.Structure):
            _fields_ = [
                ("dwSize", wintypes.DWORD),
                ("cntUsage", wintypes.DWORD),
                ("th32ProcessID", wintypes.DWORD),
                ("th32DefaultHeapID", ctypes.c_void_p),
                ("th32ModuleID", wintypes.DWORD),
                ("cntThreads", wintypes.DWORD),
                ("th32ParentProcessID", wintypes.DWORD),
                ("pcPriClassBase", wintypes.LONG),
                ("dwFlags", wintypes.DWORD),
                ("szExeFile", ctypes.c_wchar * 260),
            ]

        hSnapshot = kernel32.CreateToolhelp32Snapshot(0x00000002, 0)
        if hSnapshot == -1 or not hSnapshot:
            return None

        pe = PROCESSENTRY32()
        pe.dwSize = ctypes.sizeof(PROCESSENTRY32)

        matched_pid = None
        if kernel32.Process32FirstW(hSnapshot, ctypes.byref(pe)):
            while True:
                if "acgpower" in pe.szExeFile.lower():
                    matched_pid = pe.th32ProcessID
                    break
                if not kernel32.Process32NextW(hSnapshot, ctypes.byref(pe)):
                    break
        kernel32.CloseHandle(hSnapshot)

        if matched_pid:
            hProc = kernel32.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, False, matched_pid)
            if hProc:
                buf = ctypes.create_unicode_buffer(1024)
                size = wintypes.DWORD(1024)
                if kernel32.QueryFullProcessImageNameW(hProc, 0, buf, ctypes.byref(size)):
                    kernel32.CloseHandle(hProc)
                    return Path(buf.value).parent.resolve()
                kernel32.CloseHandle(hProc)
    except Exception:
        pass
    return None

def auto_detect_acgpower_cache() -> Optional[Path]:
    """Check running process, relative paths, and common drive paths for ACGPower cache."""
    # 1. Check if ACGPower.exe is actively running in background (instant 100% precision)
    running_dir = _get_running_acgpower_path()
    if running_dir:
        cand = normalize_cache_dir(running_dir)
        if cand.is_dir() and ((cand / "assets").is_dir() or cand.name.lower() == "https"):
            return cand

    # 2. Check base_dir parents and siblings (portable bundle placement)
    base_dir = get_base_dir()
    for p in [
        base_dir / "cache" / "gbf" / "https",
        base_dir.parent / "cache" / "gbf" / "https",
        base_dir.parent.parent / "cache" / "gbf" / "https",
        base_dir.parent / "acgpower",
        base_dir.parent / "ACGPower",
    ]:
        norm = normalize_cache_dir(p)
        if norm.is_dir() and (norm / "assets").is_dir():
            return norm

    # 3. Dynamic system drives & common game/software installation paths
    candidate_roots = []
    if sys.platform == "win32":
        try:
            import ctypes
            bitmask = ctypes.windll.kernel32.GetLogicalDrives()
            for i in range(26):
                if bitmask & (1 << i):
                    drive = f"{chr(ord('A') + i)}:/"
                    for sub in (
                        "acgpower",
                        "ACGPower",
                        "Games/acgpower",
                        "Games/ACGPower",
                        "Game/acgpower",
                        "Program Files/acgpower",
                        "Program Files (x86)/acgpower",
                        "Software/acgpower",
                        "Tools/acgpower",
                    ):
                        candidate_roots.append(Path(f"{drive}{sub}"))
        except Exception:
            pass

        # Fallback to standard known paths if drive enumeration was empty
        if not candidate_roots:
            candidate_roots = [
                Path(r"D:\acgpower"),
                Path(r"C:\acgpower"),
                Path(r"E:\acgpower"),
                Path(r"F:\acgpower"),
            ]
    elif sys.platform == "darwin":
        mac_downloads = Path.home() / "Downloads"
        for sub in (
            "ACGPOWER-MAC-2",
            "acgpower",
            "ACGPower",
            "ACGPOWER-MAC",
        ):
            if (mac_downloads / sub).is_dir():
                candidate_roots.append(mac_downloads / sub)
            if (Path.home() / sub).is_dir():
                candidate_roots.append(Path.home() / sub)
        if (Path.home() / "Library" / "Application Support" / "GBF-Accelerator" / "cache").is_dir():
            candidate_roots.append(Path.home() / "Library" / "Application Support" / "GBF-Accelerator" / "cache")

    for root in candidate_roots:
        if root.is_dir():
            norm = normalize_cache_dir(root)
            if norm.is_dir() and (norm / "assets").is_dir():
                return norm

    return None

LEGACY_LEAKED_CA_SHA1 = "51e9aa40a64fb8dc63f18f4b1a11b98d1cf8d3ff"

def _extract_cert_from_registry_blob(blob: bytes):
    """Extract an X509 certificate object from a Windows registry certificate property blob."""
    import struct
    import warnings
    from cryptography import x509
    offset = 0
    with warnings.catch_warnings():
        warnings.simplefilter("ignore")
        while offset + 12 <= len(blob):
            prop_id, flags, cb_data = struct.unpack("<III", blob[offset:offset+12])
            offset += 12
            if offset + cb_data > len(blob):
                break
            prop_data = blob[offset:offset+cb_data]
            offset += cb_data
            if prop_id == 32:  # CERT_CERT_PROP_ID
                try:
                    return x509.load_der_x509_certificate(prop_data)
                except Exception:
                    pass
        # Fallback: maybe raw DER
        try:
            return x509.load_der_x509_certificate(blob)
        except Exception:
            return None

def find_installed_gbf_ca_thumbprints() -> Dict[str, str]:
    """Find all installed GBF-related Root CA certificates in CurrentUser / LocalMachine / Keychain Root stores.
    Returns a mapping of thumbprint (uppercase hex) -> description/subject.
    """
    found: Dict[str, str] = {}
    gbf_keywords = ["gbf", "granblue", "granbluefantasy"]

    if sys.platform == "win32":
        try:
            import winreg
            for hive, hive_name in [(winreg.HKEY_CURRENT_USER, "当前用户"), (winreg.HKEY_LOCAL_MACHINE, "本地计算机")]:
                try:
                    reg_path = r"Software\Microsoft\SystemCertificates\Root\Certificates"
                    with winreg.OpenKey(hive, reg_path) as root_k:
                        i = 0
                        while True:
                            try:
                                sub_key_name = winreg.EnumKey(root_k, i)
                                i += 1
                                thumb = sub_key_name.strip().upper()

                                if thumb == LEGACY_LEAKED_CA_SHA1.upper():
                                    found[thumb] = f"GBF Speed CA (已废弃公开证书, {hive_name})"
                                    continue

                                try:
                                    with winreg.OpenKey(root_k, sub_key_name) as sub_k:
                                        blob, _ = winreg.QueryValueEx(sub_k, "Blob")
                                        cert = _extract_cert_from_registry_blob(blob)
                                        if cert:
                                            subj_str = cert.subject.rfc4514_string()
                                            issuer_str = cert.issuer.rfc4514_string()
                                            combined = (subj_str + " " + issuer_str).lower()
                                            if any(k in combined for k in gbf_keywords):
                                                found[thumb] = f"{subj_str} ({hive_name})"
                                except Exception:
                                    pass
                            except OSError:
                                break
                except Exception:
                    pass
        except Exception:
            pass
    elif sys.platform == "darwin":
        try:
            res = subprocess.run(
                ["security", "find-certificate", "-a", "-c", "GBF", "-Z"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=3,
            )
            if res.returncode == 0:
                current_sha1 = None
                for line in res.stdout.splitlines():
                    if "SHA-1 hash:" in line:
                        current_sha1 = line.split(":", 1)[1].strip().upper()
                    elif ("alis" in line or "labl" in line) and current_sha1:
                        val = line.split("=", 1)[1].strip().strip('"')
                        found[current_sha1] = val
                        current_sha1 = None
        except Exception:
            pass

    # Also check local ca.crt if present
    ca_crt_path = get_base_dir() / "certs" / "ca.crt"
    if ca_crt_path.is_file():
        try:
            from cryptography import x509
            from cryptography.hazmat.primitives import hashes
            local_cert = x509.load_pem_x509_certificate(ca_crt_path.read_bytes())
            local_sha1 = local_cert.fingerprint(hashes.SHA1()).hex().upper()
            if local_sha1 not in found and is_ca_installed():
                found[local_sha1] = f"{local_cert.subject.rfc4514_string()} (当前安装)"
        except Exception:
            pass

    return found

def _remove_cert_by_thumbprint(thumbprint: str) -> bool:
    """Remove a certificate from system Root store by SHA1 thumbprint."""
    clean_thumb = thumbprint.strip().replace(" ", "").upper()
    removed = False

    if sys.platform == "win32":
        # 1. Delete from HKCU SystemCertificates\Root\Certificates
        try:
            import winreg
            reg_path = rf"Software\Microsoft\SystemCertificates\Root\Certificates\{clean_thumb}"
            winreg.DeleteKey(winreg.HKEY_CURRENT_USER, reg_path)
            removed = True
        except FileNotFoundError:
            pass
        except Exception:
            pass

        # 2. Also attempt deletion from HKLM if user has admin privileges
        try:
            import winreg
            reg_path_lm = rf"Software\Microsoft\SystemCertificates\Root\Certificates\{clean_thumb}"
            winreg.DeleteKey(winreg.HKEY_LOCAL_MACHINE, reg_path_lm)
            removed = True
        except Exception:
            pass

        # 3. Secondary cleanup: certutil -f -user -delstore Root <thumbprint>
        try:
            res = subprocess.run(
                ["certutil", "-f", "-user", "-delstore", "Root", clean_thumb],
                input=b"y\r\n",
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            if res.returncode == 0:
                removed = True
        except Exception:
            pass

        # Also certutil machine store if admin
        try:
            res_lm = subprocess.run(
                ["certutil", "-f", "-delstore", "Root", clean_thumb],
                input=b"y\r\n",
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            if res_lm.returncode == 0:
                removed = True
        except Exception:
            pass
    elif sys.platform == "darwin":
        try:
            res = subprocess.run(
                ["security", "delete-certificate", "-Z", clean_thumb, "-t"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=5,
            )
            if res.returncode == 0:
                removed = True
        except Exception:
            pass

    return removed

def is_ca_installed() -> bool:
    """Check if the current unique Root CA (from certs/ca.crt) is installed in system Root store.
    Strictly verifies by SHA-1 hash of the local certificate to prevent mismatch with legacy/other certs.
    """
    ca_crt_path = get_base_dir() / "certs" / "ca.crt"
    if not ca_crt_path.is_file():
        return False
    try:
        from cryptography import x509
        from cryptography.hazmat.primitives import hashes
        cert_data = ca_crt_path.read_bytes()
        cert = x509.load_pem_x509_certificate(cert_data)
        sha1 = cert.fingerprint(hashes.SHA1()).hex().upper()

        if sys.platform == "win32":
            # Fast path: check Windows Registry HKCU / HKLM (instant, 0ms)
            try:
                import winreg
                for hive in (winreg.HKEY_CURRENT_USER, winreg.HKEY_LOCAL_MACHINE):
                    try:
                        reg_path = rf"Software\Microsoft\SystemCertificates\Root\Certificates\{sha1}"
                        with winreg.OpenKey(hive, reg_path):
                            return True
                    except OSError:
                        pass
            except Exception:
                pass

            # Fallback: check via certutil -user -verifystore
            res = subprocess.run(
                ["certutil", "-user", "-verifystore", "Root", sha1],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            return res.returncode == 0
        elif sys.platform == "darwin":
            res = subprocess.run(
                ["security", "find-certificate", "-a", "-c", "GBF Local Accelerator Root CA", "-Z"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=3,
            )
            if res.returncode == 0:
                out_upper = res.stdout.upper().replace(" ", "")
                if sha1 in out_upper:
                    return True
                return "GBF Local Accelerator Root CA" in res.stdout
            return False
        return True
    except Exception:
        return False

def check_legacy_leaked_ca_installed() -> bool:
    """Check whether the old public leaked CA ('GBF Speed CA') is present in Root store."""
    if sys.platform == "win32":
        # Check registry first
        try:
            import winreg
            for hive in (winreg.HKEY_CURRENT_USER, winreg.HKEY_LOCAL_MACHINE):
                try:
                    reg_path = rf"Software\Microsoft\SystemCertificates\Root\Certificates\{LEGACY_LEAKED_CA_SHA1.upper()}"
                    with winreg.OpenKey(hive, reg_path):
                        return True
                except OSError:
                    pass
        except Exception:
            pass

        try:
            res = subprocess.run(
                ["certutil", "-user", "-verifystore", "Root", LEGACY_LEAKED_CA_SHA1],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            if res.returncode == 0:
                return True
            res_name = subprocess.run(
                ["certutil", "-user", "-verifystore", "Root", "GBF Speed CA"],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            return res_name.returncode == 0
        except Exception:
            return False
    elif sys.platform == "darwin":
        try:
            res = subprocess.run(
                ["security", "find-certificate", "-c", "GBF Speed CA"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=3,
            )
            return res.returncode == 0
        except Exception:
            return False
    return False

def clean_legacy_leaked_ca() -> bool:
    """Remove the old leaked CA certificate from Root store."""
    if sys.platform == "win32":
        cleaned = False
        if _remove_cert_by_thumbprint(LEGACY_LEAKED_CA_SHA1):
            cleaned = True
        try:
            chk = subprocess.run(
                ["certutil", "-user", "-verifystore", "Root", "GBF Speed CA"],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            if chk.returncode == 0:
                res = subprocess.run(
                    ["certutil", "-f", "-user", "-delstore", "Root", "GBF Speed CA"],
                    input=b"y\r\n",
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    timeout=3,
                )
                if res.returncode == 0:
                    cleaned = True
        except Exception:
            pass
        return cleaned
    elif sys.platform == "darwin":
        try:
            res = subprocess.run(
                ["security", "delete-certificate", "-c", "GBF Speed CA", "-t"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=5,
            )
            return res.returncode == 0
        except Exception:
            return False
    return True

def install_ca_certificate(ca_path: Path) -> bool:
    """Install Root CA into User / CurrentUser Root store."""
    if not ca_path.is_file():
        return False
    if sys.platform == "win32":
        try:
            res = subprocess.run(
                ["certutil", "-addstore", "-user", "Root", str(ca_path)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
            )
            return res.returncode == 0
        except Exception:
            return False
    elif sys.platform == "darwin":
        try:
            keychain = Path.home() / "Library" / "Keychains" / "login.keychain-db"
            if not keychain.exists():
                keychain = Path.home() / "Library" / "Keychains" / "login.keychain"
            res = subprocess.run(
                ["security", "add-trusted-cert", "-r", "trustRoot", "-k", str(keychain), str(ca_path)],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
            )
            return res.returncode == 0
        except Exception:
            return False
    return False

def uninstall_ca_certificate() -> Tuple[bool, str]:
    """Uninstall and remove all GBF Root CAs from Root store."""
    if sys.platform not in ("win32", "darwin"):
        return False, "非 Windows/macOS 系统无需卸载"
    
    messages = []
    targets = find_installed_gbf_ca_thumbprints()
    
    # Also ensure local ca.crt sha1 is added to targets if local file exists
    ca_crt_path = get_base_dir() / "certs" / "ca.crt"
    if ca_crt_path.is_file():
        try:
            from cryptography import x509
            from cryptography.hazmat.primitives import hashes
            cert = x509.load_pem_x509_certificate(ca_crt_path.read_bytes())
            local_sha1 = cert.fingerprint(hashes.SHA1()).hex().upper()
            if local_sha1 not in targets and is_ca_installed():
                targets[local_sha1] = "GBF 本机根证书"
        except Exception:
            pass

    if LEGACY_LEAKED_CA_SHA1.upper() not in targets and check_legacy_leaked_ca_installed():
        targets[LEGACY_LEAKED_CA_SHA1.upper()] = "GBF Speed CA (旧版公开泄露证书)"

    if not targets:
        if sys.platform == "darwin":
            try:
                r = subprocess.run(
                    ["security", "delete-certificate", "-c", "GBF Local Accelerator Root CA", "-t"],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    timeout=5,
                )
                if r.returncode == 0:
                    return True, "已成功从钥匙串中注销 GBF 根证书"
            except Exception:
                pass
        return False, "未在系统中找到已安装的 GBF 根证书"

    success = False
    for thumb, desc in targets.items():
        if _remove_cert_by_thumbprint(thumb):
            success = True
            messages.append(f"已清理证书 [{thumb[:8]}...]: {desc}")

    # Double check clean legacy by name
    clean_legacy_leaked_ca()

    if success:
        return True, "\n".join(messages)
    return False, "注销证书失败，若证书安装在系统级存储区，请在证书管理工具中手动删除"

def check_upstream_connectivity(upstream_url: str) -> Tuple[bool, str]:
    """Test TCP connectivity to the upstream proxy server."""
    if not upstream_url or upstream_url == "direct":
        return True, "直连模式"
    try:
        parsed = urllib.parse.urlparse(upstream_url)
        host = parsed.hostname or "127.0.0.1"
        port = parsed.port or (8099 if "8099" in upstream_url else (7890 if "7890" in upstream_url else 7897))
        with socket.create_connection((host, port), timeout=1.5):
            return True, f"成功连通上游代理 {host}:{port}"
    except (socket.timeout, ConnectionRefusedError):
        return False, f"上游代理无法连通（连接被拒绝或超时，请检查 Clash 是否已启动）"
    except Exception as e:
        return False, f"上游代理连接异常: {e}"

def kill_process_on_port(port: int) -> bool:
    """Safely terminate previous GBF_Accelerator instances listening on port.
    Guarantees exact port boundary matching and strictly inspects process identity.
    """
    if port in (7890, 7897, 10808, 10809, 80, 443):
        return False

    if sys.platform == "win32":
        try:
            output = subprocess.check_output("netstat -aon", shell=True, encoding="gbk", errors="replace", timeout=3)
            port_pattern = re.compile(rf":{port}\s+.*LISTENING\s+(\d+)", re.IGNORECASE)
            pids = set()
            for line in output.splitlines():
                m = port_pattern.search(line)
                if m:
                    pids.add(m.group(1))

            current_pid = str(os.getpid())
            for pid in pids:
                if not pid.isdigit() or pid in ("0", "4", current_pid):
                    continue
                proc_info = subprocess.check_output(f'tasklist /FI "PID eq {pid}" /FO CSV /NH', shell=True, encoding="gbk", errors="replace", timeout=3).strip()
                
                should_kill = False
                if "GBF_Accelerator" in proc_info:
                    should_kill = True
                elif "python" in proc_info.lower():
                    try:
                        cmd_out = subprocess.check_output(
                            ["powershell", "-NoProfile", "-Command", f"(Get-CimInstance Win32_Process -Filter 'ProcessId={pid}').CommandLine"],
                            encoding="gbk",
                            errors="replace",
                            timeout=4,
                        )
                        if any(target in cmd_out for target in ("gbf_proxy", "app_main", "GBFAccelerator")):
                            should_kill = True
                    except Exception:
                        pass

                if should_kill:
                    subprocess.run(f"taskkill /F /PID {pid}", shell=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3)
                else:
                    return False
            return True
        except Exception:
            return False
    else:
        # macOS / POSIX
        try:
            res = subprocess.run(
                ["lsof", "-ti", f"tcp:{port}"],
                stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL,
                text=True,
                timeout=3,
            )
            if res.returncode != 0 or not res.stdout.strip():
                return True
            current_pid = os.getpid()
            for pid_str in res.stdout.splitlines():
                pid_str = pid_str.strip()
                if not pid_str.isdigit():
                    continue
                pid = int(pid_str)
                if pid in (0, 1, current_pid):
                    continue
                try:
                    ps_out = subprocess.check_output(
                        ["ps", "-p", str(pid), "-o", "command="],
                        text=True,
                        stderr=subprocess.DEVNULL,
                        timeout=2,
                    )
                    if any(target in ps_out for target in ("gbf_proxy", "app_main", "gui_main", "GBF_Accelerator")):
                        import signal
                        os.kill(pid, signal.SIGTERM)
                        import time
                        time.sleep(0.1)
                        try:
                            os.kill(pid, signal.SIGKILL)
                        except OSError:
                            pass
                    else:
                        return False
                except Exception:
                    pass
            return True
        except Exception:
            return False

class ConfigManager:
    def __init__(self):
        self.config_path = CONFIG_FILE
        self.config = self.load_config()

    def load_config(self) -> Dict[str, Any]:
        cfg = dict(DEFAULT_CONFIG)
        if self.config_path.is_file():
            try:
                with open(self.config_path, "r", encoding="utf-8") as f:
                    user_cfg = json.load(f)
                    cfg.update(user_cfg)
            except Exception:
                pass
        return cfg

    def save_config(self):
        try:
            with open(self.config_path, "w", encoding="utf-8") as f:
                json.dump(self.config, f, indent=2, ensure_ascii=False)
        except Exception:
            pass

    def get_listen_port(self) -> int:
        val = self.config.get("listen_port") or self.config.get("local_port", 8124)
        try:
            port = int(val)
            if 1 <= port <= 65535:
                return port
        except Exception:
            pass
        return 8124

    def get_effective_listen_host(self) -> str:
        """Return 0.0.0.0 if allow_lan is enabled, otherwise 127.0.0.1 (or user configured listen_host)."""
        if self.config.get("allow_lan", False):
            return "0.0.0.0"
        return self.config.get("listen_host", "127.0.0.1")

    def get_lan_ip(self) -> str:
        """Return the primary local IPv4 address for LAN sharing."""
        return get_lan_ip()

    def get_effective_cache_dir(self, interactive: bool = True) -> Path:
        raw_val = self.config.get("cache_dir", "auto")
        if raw_val != "auto" and raw_val:
            p = Path(raw_val)
            if not p.is_absolute():
                p = (get_base_dir() / p).resolve()
            norm_p = normalize_cache_dir(p)
            if norm_p != p:
                self.config["cache_dir"] = str(norm_p)
                self.save_config()
            norm_p.mkdir(parents=True, exist_ok=True)
            return norm_p

        # Check if ACGPower cache is detected
        acgp_path = auto_detect_acgpower_cache()
        default_local = (get_base_dir() / "cache" / "gbf" / "https").resolve()

        if acgp_path and interactive and sys.stdin.isatty():
            print("\n" + "=" * 65)
            print("   [+] 智能缓存检测：发现电脑中已存在的 ACGPower 缓存！")
            print(f"       检测到路径: {acgp_path}")
            print("   --------------------------------------------------------------")
            print("   [1] 直接复用 ACGPower 缓存 (推荐；无需重新下载，可直接复用已有缓存)")
            print(f"   [2] 在程序同级目录新建独立缓存 ({default_local})")
            print("   [3] 手动输入自定义缓存路径")
            print("=" * 65)
            try:
                choice = input("   请选择 [直接回车默认 1]: ").strip()
            except Exception:
                choice = "1"

            if choice == "2":
                chosen = default_local
            elif choice == "3":
                custom = input("   请输入自定义缓存文件夹路径: ").strip()
                chosen = normalize_cache_dir(Path(custom).resolve()) if custom else default_local
            else:
                chosen = acgp_path

            self.config["cache_dir"] = str(chosen)
            self.save_config()
            chosen.mkdir(parents=True, exist_ok=True)
            return chosen

        # If not interactive or no prompt, use acgp_path if found, else default
        chosen = acgp_path if acgp_path else default_local
        self.config["cache_dir"] = str(chosen)
        self.save_config()
        chosen.mkdir(parents=True, exist_ok=True)
        return chosen

    def get_effective_upstream_proxy(self) -> str:
        raw_val = self.config.get("upstream_proxy", "auto")
        if raw_val != "auto" and raw_val:
            return raw_val
        detected = auto_detect_upstream_proxy()
        return detected

config_manager = ConfigManager()
