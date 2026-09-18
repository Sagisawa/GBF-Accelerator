#!/usr/bin/env python3
"""
GBF-Accelerator Native Go Single-Binary Packager & Release Builder (M4)

Compiles the native Go proxy engine with embedded Web UI assets and desktop tray
into a standalone, high-performance single-binary release package.
Supports Windows native build and macOS (arm64/amd64) cross-compilation.
"""

import argparse
import os
import platform
import shutil
import subprocess
import sys
import zipfile
from pathlib import Path

BASE_DIR = Path(__file__).parent.resolve()
ENGINE_DIR = BASE_DIR / "engine"
BIN_DIR = BASE_DIR / "bin"
RELEASE_DIR = BASE_DIR / "release"
WEB_DIR = BASE_DIR / "web"
UI_DIST_DIR = ENGINE_DIR / "ui" / "dist"


def get_app_version() -> str:
    """Read APP_VERSION from update_manager.py or fallback to 1.8.0."""
    try:
        from update_manager import APP_VERSION
        return APP_VERSION
    except Exception:
        return "1.8.0"


def kill_running_instances():
    """Terminate running instances to prevent file lock errors during compilation."""
    if sys.platform == "win32":
        for exe in ["GBF_Accelerator.exe", "gbf-proxy.exe", "gbf_proxy.exe"]:
            try:
                subprocess.run(["taskkill", "/F", "/IM", exe], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except Exception:
                pass



def sync_embedded_assets(rebuild_web: bool = False) -> bool:
    """Synchronize web/dist and icon assets into engine/ui/dist for //go:embed."""
    print("[*] Synchronizing embedded web assets into engine/ui/dist...")

    if rebuild_web:
        print("[*] Rebuilding Web UI assets (npm run build)...")
        npm_cmd = "npm.cmd" if sys.platform == "win32" else "npm"
        res = subprocess.run([npm_cmd, "run", "build"], cwd=str(WEB_DIR))
        if res.returncode != 0:
            print("[-] Failed to rebuild Web UI with npm")
            return False

    web_dist = WEB_DIR / "dist"
    if not web_dist.is_dir() or not (web_dist / "index.html").is_file():
        print(f"[-] Web dist directory missing at {web_dist}")
        return False

    UI_DIST_DIR.mkdir(parents=True, exist_ok=True)

    # Clean existing destination assets to prevent stale bundles
    dest_assets = UI_DIST_DIR / "assets"
    if dest_assets.is_dir():
        shutil.rmtree(dest_assets)

    # Copy web/dist contents to engine/ui/dist
    for item in web_dist.iterdir():
        dest = UI_DIST_DIR / item.name
        if item.is_dir():
            shutil.copytree(item, dest, dirs_exist_ok=True)
        else:
            shutil.copy2(item, dest)

    # Copy application icon
    ico_src = BASE_DIR / "gbf_accelerator.ico"
    if ico_src.is_file():
        shutil.copy2(ico_src, ENGINE_DIR / "ui" / "icon.ico")
        shutil.copy2(ico_src, UI_DIST_DIR / "favicon.ico")

    print(f"[+] Embedded assets synchronized ({len(list(UI_DIST_DIR.rglob('*')))} files)")
    return True


def build_windows(gui_mode: bool = True) -> Path:
    """Compile Go native engine for Windows."""
    kill_running_instances()
    BIN_DIR.mkdir(parents=True, exist_ok=True)
    exe_name = "GBF_Accelerator.exe"
    out_path = BIN_DIR / exe_name

    ldflags = ["-s", "-w"]
    if gui_mode:
        ldflags.append("-H=windowsgui")

    cmd = [
        "go", "build",
        "-ldflags", " ".join(ldflags),
        "-o", str(out_path),
        ".",
    ]

    print(f"[*] Compiling Windows single native binary: {' '.join(cmd)}")
    env = os.environ.copy()
    env["CGO_ENABLED"] = "0"
    res = subprocess.run(cmd, cwd=str(ENGINE_DIR), env=env)
    if res.returncode != 0:
        raise RuntimeError(f"Go compilation failed with code {res.returncode}")

    # Also maintain bin/gbf-proxy.exe for test harness compatibility
    shutil.copy2(out_path, BIN_DIR / "gbf-proxy.exe")
    shutil.copy2(out_path, BIN_DIR / "gbf_proxy.exe")

    size_mb = out_path.stat().st_size / (1024 * 1024)
    print(f"[+] Windows binary compiled successfully: {out_path} ({size_mb:.2f} MB)")
    return out_path


def build_darwin(arch: str) -> Path:
    """Cross-compile Go native engine for macOS (arm64 or amd64)."""
    BIN_DIR.mkdir(parents=True, exist_ok=True)
    bin_name = f"GBF_Accelerator_darwin_{arch}"
    out_path = BIN_DIR / bin_name

    cmd = [
        "go", "build",
        "-ldflags", "-s -w",
        "-o", str(out_path),
        ".",
    ]

    print(f"[*] Cross-compiling macOS ({arch}) binary: {' '.join(cmd)}")
    env = os.environ.copy()
    env["GOOS"] = "darwin"
    env["GOARCH"] = arch
    env["CGO_ENABLED"] = "0"

    res = subprocess.run(cmd, cwd=str(ENGINE_DIR), env=env)
    if res.returncode != 0:
        raise RuntimeError(f"macOS {arch} compilation failed with code {res.returncode}")

    size_mb = out_path.stat().st_size / (1024 * 1024)
    print(f"[+] macOS ({arch}) binary compiled successfully: {out_path} ({size_mb:.2f} MB)")
    return out_path


def package_windows_zip(exe_path: Path, version: str) -> Path:
    """Package standalone single-binary release package for Windows."""
    RELEASE_DIR.mkdir(parents=True, exist_ok=True)
    zip_path = RELEASE_DIR / f"GBF_Accelerator_v{version}_GUI.zip"

    readme_path = BASE_DIR / "使用说明.txt"
    if not readme_path.is_file():
        try:
            from app_main import ensure_bundled_files
            ensure_bundled_files()
        except Exception:
            pass

    print(f"[*] Creating distribution package: {zip_path}")
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.write(exe_path, arcname="GBF_Accelerator.exe")
        for aux in ["SwitchyOmega_GBF.bak", "proxy.pac", "使用说明.txt", "LICENSE"]:
            src = BASE_DIR / aux
            if src.is_file():
                zf.write(src, arcname=aux)

    size_mb = zip_path.stat().st_size / (1024 * 1024)
    print(f"[***] RELEASE READY: {zip_path} ({size_mb:.2f} MB)")
    return zip_path


def package_darwin_zip(bin_path: Path, arch: str, version: str) -> Path:
    """Package standalone release package for macOS."""
    RELEASE_DIR.mkdir(parents=True, exist_ok=True)
    zip_path = RELEASE_DIR / f"GBF_Accelerator_v{version}_macOS_{arch}.zip"

    print(f"[*] Creating macOS ({arch}) distribution package: {zip_path}")
    with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.write(bin_path, arcname="GBF_Accelerator")
        for aux in ["SwitchyOmega_GBF.bak", "proxy.pac", "install_ca.sh", "start_proxy.sh", "使用说明.txt", "LICENSE"]:
            src = BASE_DIR / aux
            if src.is_file():
                zf.write(src, arcname=aux)

    size_mb = zip_path.stat().st_size / (1024 * 1024)
    print(f"[***] RELEASE READY: {zip_path} ({size_mb:.2f} MB)")
    return zip_path


def main():
    parser = argparse.ArgumentParser(description="Build standalone release packages for GBF-Accelerator")
    parser.add_argument(
        "--target",
        choices=["windows", "darwin", "all"],
        default="windows",
        help="Target platform to build for (default: windows)",
    )
    parser.add_argument(
        "--gui",
        action="store_true",
        default=True,
        help="Build with GUI subsystem (hide console window on Windows)",
    )
    parser.add_argument(
        "--console",
        action="store_true",
        help="Build in console mode (show console window on Windows)",
    )
    parser.add_argument(
        "--rebuild-web",
        action="store_true",
        help="Rebuild React Web SPA before packaging",
    )
    parser.add_argument(
        "--skip-zip",
        action="store_true",
        help="Skip creating distribution zip archive",
    )
    args = parser.parse_args()

    version = get_app_version()
    print("=" * 65)
    print(f"   GBF-Accelerator Native Go Release Builder (v{version})")
    print("=" * 65)

    if not sync_embedded_assets(rebuild_web=args.rebuild_web):
        sys.exit(1)

    gui_flag = not args.console

    targets = [args.target] if args.target != "all" else ["windows", "darwin"]

    for target in targets:
        if target == "windows":
            exe_path = build_windows(gui_mode=gui_flag)
            if not args.skip_zip:
                package_windows_zip(exe_path, version)

        elif target == "darwin":
            for arch in ["arm64", "amd64"]:
                darwin_bin = build_darwin(arch)
                if not args.skip_zip:
                    package_darwin_zip(darwin_bin, arch, version)

    print("\n[+] All release artifacts built successfully.")


if __name__ == "__main__":
    main()
