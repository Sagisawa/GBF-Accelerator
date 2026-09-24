#!/usr/bin/env bash
# GBF-Accelerator Native Go Release Builder for Unix / macOS
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="${ROOT_DIR}/engine"
BIN_DIR="${ROOT_DIR}/bin"
RELEASE_DIR="${ROOT_DIR}/release"
WEB_DIR="${ROOT_DIR}/web"
UI_DIST_DIR="${ENGINE_DIR}/ui/dist"

# AppVersion in engine/config/config.go is the single source of truth.
# Fail hard if the version cannot be extracted, to avoid shipping a mislabeled package.
APP_VERSION=$(grep 'AppVersion' "${ENGINE_DIR}/config/config.go" | sed -E 's/.*"([^"]+)".*/\1/' || true)
if [[ -z "${APP_VERSION}" ]]; then
    echo "[-] Failed to extract AppVersion from ${ENGINE_DIR}/config/config.go" >&2
    echo '    Ensure it contains: const AppVersion = "X.Y.Z"' >&2
    exit 1
fi

echo "================================================================="
echo "   GBF-Accelerator Native Go Release Builder (v${APP_VERSION})"
echo "================================================================="

mkdir -p "${BIN_DIR}" "${RELEASE_DIR}" "${UI_DIST_DIR}"

if [[ "${1:-}" == "--rebuild-web" ]]; then
    echo "[*] Rebuilding Web UI assets (npm run build)..."
    (cd "${WEB_DIR}" && npm run build)
fi

echo "[*] Synchronizing embedded web assets into engine/ui/dist..."
rm -rf "${UI_DIST_DIR}/assets"
cp -r "${WEB_DIR}/dist/"* "${UI_DIST_DIR}/"
if [[ -f "${ROOT_DIR}/gbf_accelerator.ico" ]]; then
    cp "${ROOT_DIR}/gbf_accelerator.ico" "${ENGINE_DIR}/ui/icon.ico"
    cp "${ROOT_DIR}/gbf_accelerator.ico" "${UI_DIST_DIR}/favicon.ico"
fi

echo "[*] Compiling native binary..."
(cd "${ENGINE_DIR}" && CGO_ENABLED=0 go build -ldflags "-s -w" -o "${BIN_DIR}/GBF_Accelerator" .)
echo "[+] Compilation complete: ${BIN_DIR}/GBF_Accelerator"

# If running on macOS or requested, build macOS universal / releases
if [[ "$(uname -s)" == "Darwin" ]] || [[ "${1:-}" == "--release" ]] || [[ "${2:-}" == "--release" ]]; then
    echo "[*] Compiling macOS arm64 and amd64 binaries..."
    (cd "${ENGINE_DIR}" && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o "${BIN_DIR}/GBF_Accelerator_darwin_arm64" .)
    (cd "${ENGINE_DIR}" && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w" -o "${BIN_DIR}/GBF_Accelerator_darwin_amd64" .)

    if ! command -v lipo >/dev/null 2>&1; then
        echo "[-] lipo command not found; cannot package universal2 release on this host. Official universal2 release archives must be built on macOS." >&2
        exit 1
    fi
    echo "[*] Combining universal2 binary via lipo..."
    lipo -create -output "${BIN_DIR}/GBF_Accelerator_darwin_universal" "${BIN_DIR}/GBF_Accelerator_darwin_arm64" "${BIN_DIR}/GBF_Accelerator_darwin_amd64"
    MAC_BIN="${BIN_DIR}/GBF_Accelerator_darwin_universal"

    if ! command -v zip >/dev/null 2>&1; then
        echo "[-] zip command not found; cannot package macOS release" >&2
        exit 1
    fi

    ZIP_PATH="${RELEASE_DIR}/GBF_Accelerator_v${APP_VERSION}_macOS_universal2.zip"
    rm -f "${ZIP_PATH}"
    echo "[*] Constructing macOS .app bundle and packaging: ${ZIP_PATH}..."

    STAGING_DIR="${RELEASE_DIR}/staging_macos"
    rm -rf "${STAGING_DIR}"
    mkdir -p "${STAGING_DIR}/GBF_Accelerator.app/Contents/MacOS"
    mkdir -p "${STAGING_DIR}/GBF_Accelerator.app/Contents/Resources"

    cp "${MAC_BIN}" "${STAGING_DIR}/GBF_Accelerator.app/Contents/MacOS/GBF_Accelerator"
    chmod +x "${STAGING_DIR}/GBF_Accelerator.app/Contents/MacOS/GBF_Accelerator"

    if [[ -f "${ROOT_DIR}/gbf_accelerator.icns" ]]; then
        cp "${ROOT_DIR}/gbf_accelerator.icns" "${STAGING_DIR}/GBF_Accelerator.app/Contents/Resources/gbf_accelerator.icns"
    fi

    cat > "${STAGING_DIR}/GBF_Accelerator.app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleDevelopmentRegion</key>
    <string>zh_CN</string>
    <key>CFBundleExecutable</key>
    <string>GBF_Accelerator</string>
    <key>CFBundleIconFile</key>
    <string>gbf_accelerator.icns</string>
    <key>CFBundleIdentifier</key>
    <string>com.sagisawa.gbf-accelerator</string>
    <key>CFBundleInfoDictionaryVersion</key>
    <string>6.0</string>
    <key>CFBundleName</key>
    <string>GBF Accelerator</string>
    <key>CFBundlePackageType</key>
    <string>APPL</string>
    <key>CFBundleShortVersionString</key>
    <string>${APP_VERSION}</string>
    <key>CFBundleVersion</key>
    <string>${APP_VERSION}</string>
    <key>LSMinimumSystemVersion</key>
    <string>10.15</string>
    <key>NSHighResolutionCapable</key>
    <true/>
    <key>LSUIElement</key>
    <true/>
</dict>
</plist>
EOF

    AUX_FILES=("SwitchyOmega_GBF.bak" "proxy.pac" "install_ca.sh" "start_proxy.sh" "使用说明.txt" "LICENSE")
    for aux in "${AUX_FILES[@]}"; do
        if [[ -f "${ROOT_DIR}/${aux}" ]]; then
            cp "${ROOT_DIR}/${aux}" "${STAGING_DIR}/"
        elif [[ -f "${ENGINE_DIR}/res/${aux}" ]]; then
            cp "${ENGINE_DIR}/res/${aux}" "${STAGING_DIR}/"
        fi
    done

    if [[ -f "${STAGING_DIR}/start_proxy.sh" ]]; then
        chmod +x "${STAGING_DIR}/start_proxy.sh"
    fi
    if [[ -f "${STAGING_DIR}/install_ca.sh" ]]; then
        chmod +x "${STAGING_DIR}/install_ca.sh"
    fi

    # Stage companion Android tools (LSPatch, Xposed Module, Licenses)
    mkdir -p "${STAGING_DIR}/tools/android"
    if [[ -f "${ROOT_DIR}/build/lspatch/lspatch.jar" ]]; then
        cp "${ROOT_DIR}/build/lspatch/lspatch.jar" "${STAGING_DIR}/tools/android/"
    fi
    if [[ -f "${ROOT_DIR}/android/xposed/build/outputs/apk/release/xposed-release.apk" ]]; then
        cp "${ROOT_DIR}/android/xposed/build/outputs/apk/release/xposed-release.apk" "${STAGING_DIR}/tools/android/"
    fi
    if [[ -f "${ROOT_DIR}/tools/gbf-acc-patcher/THIRD_PARTY_LICENSES.md" ]]; then
        cp "${ROOT_DIR}/tools/gbf-acc-patcher/THIRD_PARTY_LICENSES.md" "${STAGING_DIR}/tools/android/"
    fi

    (cd "${STAGING_DIR}" && zip -r -y "${ZIP_PATH}" .)
    rm -rf "${STAGING_DIR}"
    echo "[***] MAC RELEASE READY: ${ZIP_PATH}"
fi
