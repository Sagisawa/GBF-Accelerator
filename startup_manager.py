"""Cross-platform per-user startup registration for GBF Accelerator (Windows & macOS)."""

import os
import plistlib
import subprocess
import sys
from pathlib import Path
from typing import Tuple, List

APP_NAME = "GBF_Accelerator"
RUN_KEY = r"Software\Microsoft\Windows\CurrentVersion\Run"
MAC_LABEL = "com.sagisawa.gbf_accelerator"
MAC_PLIST_PATH = Path.home() / "Library" / "LaunchAgents" / f"{MAC_LABEL}.plist"


def _startup_command_windows() -> str:
    """Build a quoted command that starts this same GUI minimized to tray on Windows."""
    if getattr(sys, "frozen", False):
        executable = Path(sys.executable).resolve()
        return f'"{executable}" --minimized'

    python_exe = Path(sys.executable).resolve()
    gui_script = (Path(__file__).resolve().parent / "gui_main.py").resolve()
    return f'"{python_exe}" "{gui_script}" --minimized'


def _startup_args_mac() -> List[str]:
    """Build argument list for LaunchAgent plist on macOS."""
    if getattr(sys, "frozen", False):
        executable = Path(sys.executable).resolve()
        for p in [executable] + list(executable.parents):
            if p.suffix == ".app":
                return ["/usr/bin/open", "-a", str(p), "--args", "--minimized"]
        return [str(executable), "--minimized"]

    python_exe = Path(sys.executable).resolve()
    gui_script = (Path(__file__).resolve().parent / "gui_main.py").resolve()
    return [str(python_exe), str(gui_script), "--minimized"]


def is_supported() -> bool:
    return sys.platform in ("win32", "darwin")


def is_startup_enabled() -> bool:
    if not is_supported():
        return False

    if sys.platform == "win32":
        try:
            import winreg

            with winreg.OpenKey(winreg.HKEY_CURRENT_USER, RUN_KEY, 0, winreg.KEY_READ) as key:
                value, _ = winreg.QueryValueEx(key, APP_NAME)
            return bool(value)
        except (FileNotFoundError, OSError):
            return False
    elif sys.platform == "darwin":
        return MAC_PLIST_PATH.is_file()

    return False


def set_startup_enabled(enabled: bool) -> Tuple[bool, str]:
    """Enable/disable per-user startup without requiring administrator rights."""
    if not is_supported():
        return False, "开机自启仅支持 Windows 和 macOS。"

    if sys.platform == "win32":
        try:
            import winreg

            with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, RUN_KEY, 0, winreg.KEY_SET_VALUE) as key:
                if enabled:
                    winreg.SetValueEx(key, APP_NAME, 0, winreg.REG_SZ, _startup_command_windows())
                else:
                    try:
                        winreg.DeleteValue(key, APP_NAME)
                    except FileNotFoundError:
                        pass
            return True, ""
        except Exception as exc:
            return False, str(exc)

    elif sys.platform == "darwin":
        try:
            launch_agents_dir = MAC_PLIST_PATH.parent
            launch_agents_dir.mkdir(parents=True, exist_ok=True)

            if enabled:
                plist_data = {
                    "Label": MAC_LABEL,
                    "ProgramArguments": _startup_args_mac(),
                    "RunAtLoad": True,
                    "KeepAlive": False,
                    "StandardOutPath": f"/tmp/{APP_NAME}.stdout.log",
                    "StandardErrorPath": f"/tmp/{APP_NAME}.stderr.log",
                }
                with open(MAC_PLIST_PATH, "wb") as f:
                    plistlib.dump(plist_data, f)

                try:
                    subprocess.run(["launchctl", "unload", str(MAC_PLIST_PATH)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                except Exception:
                    pass
            else:
                try:
                    subprocess.run(["launchctl", "unload", str(MAC_PLIST_PATH)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                except Exception:
                    pass
                if MAC_PLIST_PATH.is_file():
                    MAC_PLIST_PATH.unlink(missing_ok=True)

            return True, ""
        except Exception as exc:
            return False, str(exc)

    return False, "不支持的系统平台"
