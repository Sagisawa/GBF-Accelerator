#!/usr/bin/env bash
# GBF-Accelerator Native Go Release Builder for Unix / macOS
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="${ROOT_DIR}/engine"
BIN_DIR="${ROOT_DIR}/bin"
RELEASE_DIR="${ROOT_DIR}/release"
WEB_DIR="${ROOT_DIR}/web"
UI_DIST_DIR="${ENGINE_DIR}/ui/dist"

APP_VERSION=$(grep 'AppVersion' "${ENGINE_DIR}/config/config.go" | sed -E 's/.*"([^"]+)".*/\1/' || echo "1.8.0")

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

    MAC_BIN="${BIN_DIR}/GBF_Accelerator_darwin_arm64"
    if command -v lipo >/dev/null 2>&1; then
        echo "[*] Combining universal2 binary via lipo..."
        lipo -create -output "${BIN_DIR}/GBF_Accelerator_darwin_universal" "${BIN_DIR}/GBF_Accelerator_darwin_arm64" "${BIN_DIR}/GBF_Accelerator_darwin_amd64"
        MAC_BIN="${BIN_DIR}/GBF_Accelerator_darwin_universal"
    fi

    ZIP_PATH="${RELEASE_DIR}/GBF_Accelerator_v${APP_VERSION}_macOS_universal2.zip"
    rm -f "${ZIP_PATH}"
    echo "[*] Packaging macOS release zip: ${ZIP_PATH}..."
    AUX_FILES=("SwitchyOmega_GBF.bak" "proxy.pac" "使用说明.txt" "LICENSE")
    FILES_TO_PACK=("${MAC_BIN}")
    for aux in "${AUX_FILES[@]}"; do
        if [[ -f "${ROOT_DIR}/${aux}" ]]; then
            FILES_TO_PACK+=("${ROOT_DIR}/${aux}")
        fi
    done
    if command -v zip >/dev/null 2>&1; then
        zip -j "${ZIP_PATH}" "${FILES_TO_PACK[@]}"
        echo "[***] MAC RELEASE READY: ${ZIP_PATH}"
    fi
fi
