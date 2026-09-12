import asyncio
import os
import sys
import time
from pathlib import Path

# Ensure safe UTF-8 output on Windows consoles
if sys.platform == "win32":
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
        sys.stderr.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass

from config_manager import (
    get_base_dir,
    config_manager,
    is_ca_installed,
    install_ca_certificate,
    is_port_open,
)
from cert_manager import ensure_ca, CA_CERT_PATH
from cache_manager import cache_manager
import gbf_proxy

USAGE_TEXT = """=================================================================
   GBF 极速加速器（GBF Speed Accelerator）使用说明
=================================================================

【只需 2 步开箱即玩】：

第 1 步：配置浏览器分流（推荐使用 ZeroOmega 或 SwitchyOmega 插件）
-----------------------------------------------------------------
1. 打开 Chrome / Edge / 任意 Chromium 浏览器，安装 ZeroOmega 扩展。
2. 打开插件设置 -> 【导入/导出】 -> 点击【从备份文件恢复】。
3. 选择本程序同级目录下的【SwitchyOmega_GBF.bak】文件导入。
4. 在浏览器右上角插件图标处，切换选定为【GBF_AutoSwitch】。

（备用方式：如果不想装插件，可在 Windows 系统代理设置中，将 PAC 脚本
地址填入：file:///{pac_path} ）

第 2 步：启动本加速器与 Clash
-----------------------------------------------------------------
1. 确保你的 Clash Verge / Clash / v2rayN 已开启并连上日本节点。
2. 双击运行 GBF_Accelerator.exe。
3. 浏览器打开 game.granbluefantasy.jp 即可享受 0ms 极速游戏！

=================================================================
【原理与安全说明】：
- 本工具为纯网络层本地静态缓存与连接复用，绝不修改任何游戏数据或伤害。
- 静态资源立绘/音频均缓存在本地，越玩越快，永不封号。
"""

PAC_CONTENT = """function FindProxyForURL(url, host) {
    if (
        shExpMatch(host, "*.granbluefantasy.jp") ||
        shExpMatch(host, "granbluefantasy.jp") ||
        shExpMatch(host, "*.granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.com") ||
        shExpMatch(host, "*.akamaized.net") ||
        shExpMatch(host, "akamaized.net") ||
        shExpMatch(host, "*.mbga.jp") ||
        shExpMatch(host, "mbga.jp") ||
        shExpMatch(host, "rcv.a-i-ad.com") ||
        shExpMatch(host, "*.smbeat.jp") ||
        shExpMatch(host, "*.smrtbeat.com") ||
        shExpMatch(host, "*datadoghq-browser-agent*") ||
        shExpMatch(host, "*google-analytics.com") ||
        shExpMatch(host, "*googletagmanager.com")
    ) {
        return "PROXY 127.0.0.1:8124; DIRECT";
    }
    return "DIRECT";
}
"""

def ensure_bundled_files():
    """Ensure auxiliary helper files exist in the base directory."""
    base_dir = get_base_dir()

    # 1. proxy.pac
    pac_file = base_dir / "proxy.pac"
    if not pac_file.is_file():
        try:
            with open(pac_file, "w", encoding="utf-8") as f:
                f.write(PAC_CONTENT)
        except Exception:
            pass

    # 2. 使用说明.txt
    readme_file = base_dir / "使用说明.txt"
    if not readme_file.is_file():
        try:
            with open(readme_file, "w", encoding="utf-8") as f:
                f.write(USAGE_TEXT.format(pac_path=str(pac_file).replace("\\", "/")))
        except Exception:
            pass

    # 3. SwitchyOmega_GBF.bak (if source exists in source tree or bundled)
    bak_dest = base_dir / "SwitchyOmega_GBF.bak"
    if not bak_dest.is_file():
        source_bak = Path(__file__).parent / "SwitchyOmega_GBF.bak"
        if source_bak.is_file():
            try:
                import shutil
                shutil.copy2(source_bak, bak_dest)
            except Exception:
                pass

def check_ca_setup():
    """Verify and prompt to install Root CA if missing."""
    ensure_ca()
    if is_ca_installed():
        print("   [+] 根证书状态: [已信任] (HTTPS 缓存已就绪)")
        return

    print("\n" + "!" * 65)
    print("   [!] 检测到本机尚未安装加速根证书！")
    print("       本加速器需信任根证书以解密并实现 Akamai 静态资源 0ms SSD 读取。")
    print("       正在自动调用系统证书管理器为你安装...")
    print("       >>> 稍后弹出的 Windows 安全警告窗口中，请点击【是 (Y)】<<<")
    print("!" * 65 + "\n")

    success = install_ca_certificate(CA_CERT_PATH)
    if is_ca_installed():
        print("   [+] 根证书安装成功并已受信！\n")
    else:
        print(f"   [!] 证书未自动安装，你也可以双击运行 certs/ca.crt 手动安装到【受信任的根证书颁发机构】。\n")

def main():
    base_dir = get_base_dir()
    print("=" * 65)
    print("      GBF Speed Accelerator v1.0 - 碧蓝幻想极速加速器")
    print("      基于 Clash + SSD 本地缓存的 0ms 闪电进本方案")
    print("=" * 65)

    # Export helper docs/configs
    ensure_bundled_files()

    # Step 1: CA Certificate Check
    check_ca_setup()

    # Step 2: Cache Directory Selection (interactive if first time)
    cache_dir = config_manager.get_effective_cache_dir(interactive=True)
    cache_manager.set_cache_base(cache_dir)
    print(f"   [+] 静态缓存目录: {cache_dir}")

    # Step 3: Upstream Proxy Check
    upstream = config_manager.get_effective_upstream_proxy()
    gbf_proxy.UPSTREAM_PROXY = upstream
    print(f"   [+] 上游代理服务: {upstream}")

    # Step 4: Run Proxy Server
    print("   --------------------------------------------------------------")
    try:
        asyncio.run(gbf_proxy.main())
    except KeyboardInterrupt:
        print("\n[*] 加速器已安全退出。")

if __name__ == "__main__":
    main()
