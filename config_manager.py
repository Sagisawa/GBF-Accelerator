import json
import os
import socket
import subprocess
import sys
from pathlib import Path
from typing import Optional, Dict, Any, List

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
    "cache_dir": "auto",       # auto-detect ACGPower or use ./cache/gbf/https
    "clean_zombies": True,
    "auto_system_proxy": True, # Automatically mount PAC in Windows Internet Settings
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
]

def is_port_open(host: str, port: int, timeout: float = 0.3) -> bool:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except (socket.timeout, ConnectionRefusedError, OSError):
        return False

def auto_detect_upstream_proxy() -> str:
    """Probe common local proxy ports and return active proxy URL."""
    for port, name in PROBE_PROXY_PORTS:
        if is_port_open("127.0.0.1", port):
            return f"http://127.0.0.1:{port}"
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

def is_ca_installed() -> bool:
    """Check if GBF Speed CA is installed in CurrentUser Root store."""
    try:
        res = subprocess.run(
            ["certutil", "-user", "-verifystore", "Root", "GBF Speed CA"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=3,
        )
        return res.returncode == 0
    except Exception:
        return False

def install_ca_certificate(ca_path: Path) -> bool:
    """Install Root CA into CurrentUser Root store."""
    if not ca_path.is_file():
        return False
    try:
        res = subprocess.run(
            ["certutil", "-addstore", "-user", "Root", str(ca_path)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=10,
        )
        return res.returncode == 0
    except Exception:
        return False

def kill_process_on_port(port: int):
    """Find and terminate any process listening on the given local port."""
    if sys.platform != "win32":
        return
    # Guard against accidentally killing upstream proxies or standard system ports
    if port in (7890, 7897, 10808, 10809, 80, 443):
        return
    try:
        cmd = f'for /f "tokens=5" %a in (\'netstat -aon ^| findstr ":{port}" ^| findstr "LISTENING"\') do taskkill /F /PID %a'
        subprocess.run(cmd, shell=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3)
    except Exception:
        pass

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
            print("   [1] 直接复用 ACGPower 缓存 (推荐！无需重新下载，立享 0ms)")
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
