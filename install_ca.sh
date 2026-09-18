#!/usr/bin/env bash
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
CA_PATH="$DIR/certs/ca.crt"

echo "========================================================"
echo "  GBF Accelerator - 安装根证书到 macOS 钥匙串"
echo "========================================================"
echo ""

if [ ! -f "$CA_PATH" ]; then
    echo "[*] 未找到本地 ca.crt，正在自动生成本机专属唯一根证书..."
    if [ -f "$DIR/bin/GBF_Accelerator" ]; then
        "$DIR/bin/GBF_Accelerator" --headless &
        BG_PID=$!
        sleep 2
        kill $BG_PID 2>/dev/null || true
    else
        (cd "$DIR/engine" && go run . --headless &)
        BG_PID=$!
        sleep 2
        kill $BG_PID 2>/dev/null || true
    fi
fi

echo "[*] 正在将根证书导入当前用户登录钥匙串 (login.keychain)..."
KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"
if [ ! -f "$KEYCHAIN" ]; then
    KEYCHAIN="$HOME/Library/Keychains/login.keychain"
fi

if security add-trusted-cert -r trustRoot -k "$KEYCHAIN" "$CA_PATH" 2>/dev/null; then
    echo ""
    echo "[+] 根证书已成功导入登录钥匙串并设置为受信任！"
    echo "========================================================"
    echo "【重要提示】"
    echo "如果 Google Chrome / Safari 浏览器此时正在运行，"
    echo "请务必完全退出浏览器（在浏览器中按快捷键 Cmd + Q），"
    echo "然后重新启动浏览器访问游戏，即可正常识别证书！"
    echo "========================================================"
else
    echo ""
    echo "[!] 尝试使用管理员权限安装至系统钥匙串 (可能需要输入您的 Mac 开机密码)..."
    if sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain "$CA_PATH"; then
        echo ""
        echo "[+] 根证书已成功安装至系统级钥匙串！"
        echo "========================================================"
        echo "【重要提示】"
        echo "请完全退出 Chrome（快捷键 Cmd + Q）后再重新打开即可！"
        echo "========================================================"
    else
        echo ""
        echo "[!] 您也可以双击打开证书文件，在【钥匙串访问】中手动信任："
        open "$CA_PATH"
        echo "    1. 在弹出的【钥匙串访问】中找到【GBF Local Accelerator Root CA】；"
        echo "    2. 双击打开，展开【信任】，将【使用此证书时】设置为【始终信任】；"
        echo "    3. 关闭设置窗口并输入 Mac 密码保存；"
        echo "    4. 按 Cmd + Q 彻底退出 Chrome 后重新打开。"
        echo "========================================================"
    fi
fi
