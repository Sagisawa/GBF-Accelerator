#!/usr/bin/env python3
"""
GBF-Accelerator macOS Application Packager
Builds a native macOS .app bundle and portable release zip with Universal 2
(Apple Silicon arm64 + Intel x86_64) dual-architecture support.
"""

import argparse
import os
import shutil
import subprocess
import sys
from pathlib import Path

BASE_DIR = Path(__file__).parent.resolve()
DIST_DIR = BASE_DIR / "dist"
BUILD_DIR = BASE_DIR / "build"
RELEASE_DIR = BASE_DIR / "release"

def ensure_icns_icon() -> Path:
    """Ensure gbf_accelerator.icns exists by converting gbf_accelerator.ico if necessary."""
    icns_path = BASE_DIR / "gbf_accelerator.icns"
    ico_path = BASE_DIR / "gbf_accelerator.ico"

    if icns_path.is_file() and icns_path.stat().st_size > 0:
        return icns_path

    if not ico_path.is_file():
        raise FileNotFoundError(f"Icon source {ico_path} not found.")

    print("[*] Generating gbf_accelerator.icns from gbf_accelerator.ico...")
    from PIL import Image

    iconset_dir = BASE_DIR / "gbf_accelerator.iconset"
    iconset_dir.mkdir(parents=True, exist_ok=True)

    try:
        img = Image.open(ico_path).convert("RGBA")
        sizes = [
            (16, "icon_16x16.png"),
            (32, "icon_16x16@2x.png"),
            (32, "icon_32x32.png"),
            (64, "icon_32x32@2x.png"),
            (128, "icon_128x128.png"),
            (256, "icon_128x128@2x.png"),
            (256, "icon_256x256.png"),
            (512, "icon_256x256@2x.png"),
            (512, "icon_512x512.png"),
            (1024, "icon_512x512@2x.png"),
        ]
        for sz, name in sizes:
            resized = img.resize((sz, sz), Image.Resampling.LANCZOS)
            resized.save(iconset_dir / name, "PNG")

        cmd = ["iconutil", "-c", "icns", str(iconset_dir), "-o", str(icns_path)]
        res = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        if res.returncode != 0:
            raise RuntimeError(f"iconutil failed: {res.stderr}")
        print(f"[+] Successfully generated macOS icon: {icns_path}")
    finally:
        shutil.rmtree(iconset_dir, ignore_errors=True)

    return icns_path

def find_pyinstaller() -> Path:
    """Find the preferred PyInstaller executable (prioritizing universal2 .venv_build)."""
    candidates = [
        BASE_DIR / ".venv_build" / "bin" / "pyinstaller",
        BASE_DIR / ".venv" / "bin" / "pyinstaller",
    ]
    for c in candidates:
        if c.is_file() and os.access(c, os.X_OK):
            return c

    system_pyinstaller = shutil.which("pyinstaller")
    if system_pyinstaller:
        return Path(system_pyinstaller)

    raise FileNotFoundError("PyInstaller executable not found in .venv_build, .venv, or system PATH.")

def get_app_version() -> str:
    """Read APP_VERSION from update_manager.py."""
    try:
        from update_manager import APP_VERSION
        return APP_VERSION
    except Exception:
        return "1.7.2"

def build(target_arch: str = "universal2") -> bool:
    if sys.platform != "darwin":
        print("[!] build_app.py is designed for macOS. On Windows, please run build_exe.py.")
        return False

    print("=" * 65)
    print(f"   Building macOS App for GBF Accelerator (Target: {target_arch})")
    print("=" * 65)

    icns_path = ensure_icns_icon()
    pyinstaller_exe = find_pyinstaller()
    print(f"[*] Using PyInstaller: {pyinstaller_exe}")

    cmd = [
        str(pyinstaller_exe),
        "--noconfirm",
        "--clean",
        "--windowed",
        "--name", "GBF_Accelerator",
        "--icon", str(icns_path),
        "--target-arch", target_arch,
        "--osx-bundle-identifier", "com.sagisawa.gbfaccelerator",
        "--paths", str(BASE_DIR),
        "--collect-all", "cryptography",
        "--collect-all", "h2",
        "--hidden-import", "hpack",
        "--hidden-import", "hyperframe",
        "--collect-all", "pystray",
        "--collect-all", "PIL",
        "--collect-all", "pyobjc_core",
        "--collect-all", "pyobjc_framework_Cocoa",
        "--collect-all", "pyobjc_framework_Quartz",
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
        "--hidden-import", "AppKit",
        "--hidden-import", "objc",
        "--hidden-import", "Foundation",
        "--hidden-import", "Quartz",
        "--add-data", f"{BASE_DIR / 'SwitchyOmega_GBF.bak'}:.",
        "--add-data", f"{BASE_DIR / 'proxy.pac'}:.",
        "--add-data", f"{BASE_DIR / 'install_ca.sh'}:.",
        "--add-data", f"{BASE_DIR / 'start_proxy.sh'}:.",
        str(BASE_DIR / "gui_main.py"),
    ]

    if (BASE_DIR / "使用说明.txt").is_file():
        cmd.extend(["--add-data", f"{BASE_DIR / '使用说明.txt'}:."])
    if (BASE_DIR / "LICENSE").is_file():
        cmd.extend(["--add-data", f"{BASE_DIR / 'LICENSE'}:."])

    print("Running command:", " ".join(cmd[:10]), "... [options truncated]")
    res = subprocess.run(cmd, cwd=str(BASE_DIR))
    if res.returncode != 0:
        print("[!] PyInstaller build failed!")
        return False

    app_path = DIST_DIR / "GBF_Accelerator.app"
    if not app_path.is_dir():
        print(f"[!] Cannot find compiled {app_path} in dist!")
        return False

    binary_path = app_path / "Contents" / "MacOS" / "GBF_Accelerator"
    if binary_path.is_file():
        file_check = subprocess.run(["file", str(binary_path)], capture_output=True, text=True)
        print("\n[+] Binary Architecture Verification:")
        print("   ", file_check.stdout.strip())

    version = get_app_version()
    plist_path = app_path / "Contents" / "Info.plist"
    if plist_path.is_file():
        subprocess.run(["/usr/libexec/PlistBuddy", "-c", f"Set :CFBundleShortVersionString {version}", str(plist_path)], stderr=subprocess.DEVNULL)
        subprocess.run(["/usr/libexec/PlistBuddy", "-c", f"Set :CFBundleVersion {version}", str(plist_path)], stderr=subprocess.DEVNULL)
        subprocess.run(["codesign", "--force", "--deep", "--sign", "-", str(app_path)], check=True)

    # Prepare Release Package
    RELEASE_DIR.mkdir(parents=True, exist_ok=True)
    zip_name = f"GBF_Accelerator_v{version}_macOS_{target_arch}.zip"
    zip_path = RELEASE_DIR / zip_name

    if zip_path.is_file():
        zip_path.unlink()

    # Stage files in temporary release staging directory
    staging_dir = RELEASE_DIR / f"staging_{target_arch}"
    if staging_dir.is_dir():
        shutil.rmtree(staging_dir)
    staging_dir.mkdir(parents=True, exist_ok=True)

    try:
        # Copy GBF_Accelerator.app preserving symlinks and executable bits
        print(f"[*] Staging {app_path.name}...")
        subprocess.run(["cp", "-R", "-p", str(app_path), str(staging_dir / "GBF_Accelerator.app")], check=True)

        aux_files = ["SwitchyOmega_GBF.bak", "proxy.pac", "install_ca.sh", "start_proxy.sh", "使用说明.txt", "LICENSE"]
        for f_name in aux_files:
            src = BASE_DIR / f_name
            if src.is_file():
                shutil.copy2(src, staging_dir / f_name)

        # Ensure executable scripts have +x permission
        for script_name in ("install_ca.sh", "start_proxy.sh"):
            s_path = staging_dir / script_name
            if s_path.is_file():
                s_path.chmod(0o755)

        print(f"[*] Creating distribution archive: {zip_path}...")
        # Use zip to preserve macOS metadata and executable permissions without staging prefix
        cmd = ["/usr/bin/zip", "-r", "-y", str(zip_path)] + [p.name for p in staging_dir.iterdir()]
        subprocess.run(cmd, cwd=str(staging_dir), check=True)
    finally:
        shutil.rmtree(staging_dir, ignore_errors=True)

    size_mb = zip_path.stat().st_size / (1024 * 1024)
    print("=" * 65)
    print(f"[***] RELEASE READY: {zip_path}")
    print(f"      Architecture: {target_arch}")
    print(f"      Package Size: {size_mb:.2f} MB")
    print("=" * 65)
    return True

def main():
    parser = argparse.ArgumentParser(description="Build macOS Application for GBF-Accelerator")
    parser.add_argument(
        "--arch",
        choices=["universal2", "arm64", "x86_64"],
        default="universal2",
        help="Target architecture slice (default: universal2 for Apple Silicon + Intel dual compatibility)",
    )
    args = parser.parse_args()
    success = build(target_arch=args.arch)
    sys.exit(0 if success else 1)

if __name__ == "__main__":
    main()
