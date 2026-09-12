"""Windows per-user startup registration for GBF Accelerator."""

import sys
from pathlib import Path
from typing import Tuple

APP_NAME = "GBF_Accelerator"
RUN_KEY = r"Software\Microsoft\Windows\CurrentVersion\Run"


def _startup_command() -> str:
    """Build a quoted command that starts this same GUI minimized to tray."""
    if getattr(sys, "frozen", False):
        executable = Path(sys.executable).resolve()
        return f'"{executable}" --minimized'

    python_exe = Path(sys.executable).resolve()
    gui_script = (Path(__file__).resolve().parent / "gui_main.py").resolve()
    return f'"{python_exe}" "{gui_script}" --minimized'


def is_supported() -> bool:
    return sys.platform == "win32"


def is_startup_enabled() -> bool:
    if not is_supported():
        return False
    try:
        import winreg

        with winreg.OpenKey(winreg.HKEY_CURRENT_USER, RUN_KEY, 0, winreg.KEY_READ) as key:
            value, _ = winreg.QueryValueEx(key, APP_NAME)
        return bool(value)
    except (FileNotFoundError, OSError):
        return False


def set_startup_enabled(enabled: bool) -> Tuple[bool, str]:
    """Enable/disable per-user startup without requiring administrator rights."""
    if not is_supported():
        return False, "开机自启仅支持 Windows。"

    try:
        import winreg

        with winreg.CreateKeyEx(winreg.HKEY_CURRENT_USER, RUN_KEY, 0, winreg.KEY_SET_VALUE) as key:
            if enabled:
                winreg.SetValueEx(key, APP_NAME, 0, winreg.REG_SZ, _startup_command())
            else:
                try:
                    winreg.DeleteValue(key, APP_NAME)
                except FileNotFoundError:
                    pass
        return True, ""
    except Exception as exc:
        return False, str(exc)
