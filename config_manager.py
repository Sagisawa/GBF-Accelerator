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
    "upstream_proxy": "auto",  # auto-probe 7897, 7890, 10808, 10809
    "direct_mode": False,       # connect directly while retaining local cache
    "cache_dir": "auto",       # auto-detect ACGPower or use ./cache/gbf/https
    "clean_zombies": True,
    "auto_system_proxy": True, # Automatically mount PAC in Windows Internet Settings
    "auto_start": False,       # Start the GUI with Windows and minimize to tray
    "enable_ram_cache": True,  # In-memory LRU hot cache (fast RAM lookup)
    "ram_cache_max_mb": 256,   # Max RAM allocation for hot cache (in MB)
    "enable_browser_cache": False, # Conservative default: only inject immutable on versioned assets if enabled
    "enable_auto_repair": True, # Auto-detect and clean 0-byte or corrupted cache files
    "verify_upstream_tls": True, # Upstream TLS certificate verification for security
    "shimakaze_mode": False,   # ShimakazeGo optimization mode (relaxed timeout, retry, self-signed CA)
}

KNOWN_ACGPOWER_PATHS = [
    Path(r"D:\acgpower\cache\gbf\https"),
    Path(r"C:\acgpower\cache\gbf\https"),
    Path(r"E:\acgpower\cache\gbf\https"),
    Path(r"F:\acgpower\cache\gbf\https"),
]

PROBE_PROXY_PORTS = [
    (7897, "Clash Verge (Mixed Port)"),
    (7890, "Clash Default (HTTP)"),
    (10808, "v2rayN (HTTP)"),
    (10809, "v2rayN (SOCKS/HTTP)"),
    (8099, "岛风 GO (HTTP)"),
]

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

def auto_detect_acgpower_cache() -> Optional[Path]:
    """Check common paths and relative paths for existing ACGPower cache."""
    # Check parent paths in case the exe is placed in or near acgpower
    base_dir = get_base_dir()
    for p in [base_dir / "cache" / "gbf" / "https", base_dir.parent / "cache" / "gbf" / "https", base_dir.parent.parent / "cache" / "gbf" / "https"]:
        if p.is_dir() and (p / "assets").is_dir():
            return p

    for p in KNOWN_ACGPOWER_PATHS:
        if p.is_dir() and (p / "assets").is_dir():
            return p
    return None

LEGACY_LEAKED_CA_SHA1 = "51e9aa40a64fb8dc63f18f4b1a11b98d1cf8d3ff"

def is_ca_installed() -> bool:
    """Check if the current unique Root CA (from certs/ca.crt) is installed in CurrentUser Root store.
    Strictly verifies by SHA-1 hash of the local certificate to prevent mismatch with legacy/other certs.
    """
    if sys.platform != "win32":
        return True
    ca_crt_path = get_base_dir() / "certs" / "ca.crt"
    if not ca_crt_path.is_file():
        return False
    try:
        from cryptography import x509
        from cryptography.hazmat.primitives import hashes
        cert_data = ca_crt_path.read_bytes()
        cert = x509.load_pem_x509_certificate(cert_data)
        sha1 = cert.fingerprint(hashes.SHA1()).hex().lower()
        res = subprocess.run(
            ["certutil", "-user", "-verifystore", "Root", sha1],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=3,
        )
        return res.returncode == 0
    except Exception:
        return False

def check_legacy_leaked_ca_installed() -> bool:
    """Check whether the old public leaked CA ('GBF Speed CA') is present in CurrentUser Root store."""
    if sys.platform != "win32":
        return False
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

def clean_legacy_leaked_ca() -> bool:
    """Remove the old leaked CA certificate from CurrentUser Root store."""
    if sys.platform != "win32":
        return True
    cleaned = False
    for target in [LEGACY_LEAKED_CA_SHA1, "GBF Speed CA"]:
        try:
            # Only attempt deletion if target actually exists in store
            chk = subprocess.run(
                ["certutil", "-user", "-verifystore", "Root", target],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            if chk.returncode != 0:
                continue
            res = subprocess.run(
                ["certutil", "-delstore", "-user", "Root", target],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
            )
            if res.returncode == 0:
                cleaned = True
        except Exception:
            pass
    return cleaned

def install_ca_certificate(ca_path: Path) -> bool:
    """Install Root CA into CurrentUser Root store."""
    if not ca_path.is_file():
        return False
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

def uninstall_ca_certificate() -> Tuple[bool, str]:
    """Uninstall and remove GBF Root CA from CurrentUser Root store."""
    if sys.platform != "win32":
        return False, "非 Windows 系统无需卸载"
    success = False
    messages = []
    
    # Check ca.crt SHA1
    ca_crt_path = get_base_dir() / "certs" / "ca.crt"
    targets = []
    if ca_crt_path.is_file():
        try:
            from cryptography import x509
            from cryptography.hazmat.primitives import hashes
            cert = x509.load_pem_x509_certificate(ca_crt_path.read_bytes())
            targets.append(cert.fingerprint(hashes.SHA1()).hex().lower())
        except Exception:
            pass
    targets.extend(["GBF Local Accelerator Root CA", "GBF Speed CA", "GBF Local CA", LEGACY_LEAKED_CA_SHA1])

    seen = set()
    for name in targets:
        if name in seen:
            continue
        seen.add(name)
        try:
            # Only attempt deletion if target actually exists in store
            chk = subprocess.run(
                ["certutil", "-user", "-verifystore", "Root", name],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=3,
            )
            if chk.returncode != 0:
                continue
            res = subprocess.run(
                ["certutil", "-delstore", "-user", "Root", name],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=10,
            )
            if res.returncode == 0:
                success = True
                messages.append(f"已清理证书: {name}")
        except Exception:
            pass
    if success:
        return True, "\n".join(messages)
    return False, "未在系统中找到 GBF 根证书"

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
    Guarantees exact port boundary matching and strictly inspects process identity without relying on wmic.
    """
    if sys.platform != "win32":
        return False
    if port in (7890, 7897, 10808, 10809, 80, 443):
        return False
    try:
        output = subprocess.check_output("netstat -aon", shell=True, encoding="gbk", errors="replace", timeout=3)
        # Match exact local address and port boundary: e.g. "  TCP    127.0.0.1:8124    0.0.0.0:0   LISTENING   12345"
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
                # Inspect command line via modern PowerShell CIM (compatible with Win10 and Win11 24H2+)
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

    def get_effective_cache_dir(self, interactive: bool = True) -> Path:
        raw_val = self.config.get("cache_dir", "auto")
        if raw_val != "auto" and raw_val:
            p = Path(raw_val)
            if not p.is_absolute():
                p = (get_base_dir() / p).resolve()
            p.mkdir(parents=True, exist_ok=True)
            return p

        # Check if ACGPower cache is detected
        acgp_path = auto_detect_acgpower_cache()
        default_local = (get_base_dir() / "cache" / "gbf" / "https").resolve()

        if acgp_path and interactive and sys.stdin.isatty():
            print("\n" + "=" * 65)
            print("   [+] 智能缓存检测：发现电脑中已存在的 ACGPower 缓存！")
            print(f"       检测到路径: {acgp_path}")
            print("   --------------------------------------------------------------")
            print("   [1] 直接复用 ACGPower 缓存 (推荐！无需重新下载，立享本地极速响应)")
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
                chosen = Path(custom).resolve() if custom else default_local
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
