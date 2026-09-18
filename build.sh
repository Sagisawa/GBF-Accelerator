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
