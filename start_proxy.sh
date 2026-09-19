#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "========================================================"
echo "  启动 GBF 加速代理 (GBF Accelerator for macOS)"
echo "========================================================"

if [ -f "$DIR/GBF_Accelerator_darwin_universal" ]; then
    "$DIR/GBF_Accelerator_darwin_universal" "$@"
elif [ -f "$DIR/GBF_Accelerator" ]; then
    "$DIR/GBF_Accelerator" "$@"
elif [ -f "$DIR/GBF_Accelerator_darwin_arm64" ]; then
    "$DIR/GBF_Accelerator_darwin_arm64" "$@"
elif [ -f "$DIR/GBF_Accelerator_darwin_amd64" ]; then
    "$DIR/GBF_Accelerator_darwin_amd64" "$@"
elif [ -f "$DIR/bin/GBF_Accelerator" ]; then
    "$DIR/bin/GBF_Accelerator" "$@"
elif [ -f "$DIR/engine/main.go" ]; then
    echo "[*] Binary not found, running Go engine directly..."
    cd "$DIR/engine" && go run . "$@"
else
    echo "[-] GBF_Accelerator binary not found in $DIR" >&2
    exit 1
fi
