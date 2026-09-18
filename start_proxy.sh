#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "========================================================"
echo "  启动 GBF 加速代理 (GBF Accelerator for macOS)"
echo "========================================================"

if [ -f "$DIR/bin/GBF_Accelerator" ]; then
    "$DIR/bin/GBF_Accelerator" "$@"
else
    echo "[*] Binary not found in bin/, running Go engine directly..."
    cd "$DIR/engine" && go run . "$@"
fi
