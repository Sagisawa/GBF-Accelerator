#!/usr/bin/env bash
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$DIR"

echo "========================================================"
echo "  启动 GBF 加速代理 (GBF Accelerator for macOS)"
echo "========================================================"

if [ -f "$DIR/.venv/bin/python" ]; then
    PYTHON_CMD="$DIR/.venv/bin/python"
else
    PYTHON_CMD="python3"
fi

if [ "$1" == "--cli" ]; then
    "$PYTHON_CMD" app_main.py "${@:2}"
else
    "$PYTHON_CMD" gui_main.py "$@"
fi
