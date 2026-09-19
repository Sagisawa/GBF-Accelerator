import os
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

BASE_DIR = Path(__file__).parent.resolve()
DIST_DIR = BASE_DIR / "dist"
BUILD_DIR = BASE_DIR / "build"
RELEASE_DIR = BASE_DIR / "release"

def kill_running_instances():
    if sys.platform == "win32":
        try:
            subprocess.run(["taskkill", "/F", "/IM", "GBF_Accelerator.exe"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        except Exception:
            pass

def build():
    print("=" * 60)
    print("   Starting PyInstaller Compilation for GBF Accelerator...")
    print("=" * 60)

    kill_running_instances()

    cmd = [
        sys.executable,
        "-m",
        "PyInstaller",
        "--noconfirm",
        "--clean",
        "--onefile",
        "--windowed",  # No console black box
        "--name", "GBF_Accelerator",
        "--icon", str(BASE_DIR / "gbf_accelerator.ico"),
        "--paths", str(BASE_DIR),
        "--collect-all", "cryptography",
        "--collect-all", "h2",
        "--hidden-import", "hpack",
        "--collect-all", "pystray",
        "--collect-all", "PIL",
        "--hidden-import", "cert_manager",
        "--hidden-import", "cache_manager",
        "--hidden-import", "config_manager",
        "--hidden-import", "gbf_proxy",
        "--hidden-import", "gui_main",
        "--hidden-import", "app_main",
        "--hidden-import", "system_proxy",
        "--hidden-import", "startup_manager",
        "--hidden-import", "update_manager",
        "--hidden-import", "socksio",
        "--exclude-module", "AppKit",
        "--exclude-module", "objc",
        "--exclude-module", "Foundation",
        "--add-data", f"{BASE_DIR / 'SwitchyOmega_GBF.bak'};.",
        "--add-data", f"{BASE_DIR / 'proxy.pac'};.",
        str(BASE_DIR / "gui_main.py"),
    ]

    print("Running command:", " ".join(cmd))
    res = subprocess.run(cmd, cwd=str(BASE_DIR))
    if res.returncode != 0:
        print("[!] PyInstaller build failed!")
        return False

    exe_path = DIST_DIR / "GBF_Accelerator.exe"
    if not exe_path.is_file():
        print("[!] Cannot find compiled GBF_Accelerator.exe in dist!")
        return False

    print(f"[+] Compiled successfully! Size: {exe_path.stat().st_size / (1024 * 1024):.2f} MB")

    # Prepare Release Package
    RELEASE_DIR.mkdir(parents=True, exist_ok=True)
    try:
        from update_manager import APP_VERSION
        app_ver = APP_VERSION
    except Exception:
        app_ver = "1.8.1"
    zip_path = RELEASE_DIR / f"GBF_Accelerator_v{app_ver}_GUI.zip"
    readme_path = BASE_DIR / "使用说明.txt"
    if not readme_path.is_file():
        from app_main import ensure_bundled_files
        ensure_bundled_files()

    print(f"[+] Packaging into portable distribution: {zip_path}")
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.write(exe_path, arcname="GBF_Accelerator.exe")
        if (BASE_DIR / "SwitchyOmega_GBF.bak").is_file():
            zf.write(BASE_DIR / "SwitchyOmega_GBF.bak", arcname="SwitchyOmega_GBF.bak")
        if (BASE_DIR / "proxy.pac").is_file():
            zf.write(BASE_DIR / "proxy.pac", arcname="proxy.pac")
        if readme_path.is_file():
            zf.write(readme_path, arcname="使用说明.txt")
        if (BASE_DIR / "LICENSE").is_file():
            zf.write(BASE_DIR / "LICENSE", arcname="LICENSE")

    print(f"\n[***] RELEASE READY: {zip_path}")
    print(f"      Zip Package Size: {zip_path.stat().st_size / (1024 * 1024):.2f} MB")
    return True

if __name__ == "__main__":
    build()
