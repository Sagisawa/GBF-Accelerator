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
   GBF 加速器（GBF Speed Accelerator）使用说明
=================================================================

【只需 2 步开箱即玩】：

第 1 步：配置浏览器分流（推荐使用 ZeroOmega 或 SwitchyOmega 插件）
-----------------------------------------------------------------
1. 打开 Chrome / Edge / 任意 Chromium 浏览器，安装 ZeroOmega 扩展。
2. 打开插件设置 -> 【导入/导出】 -> 点击【从备份文件恢复】。
3. 选择本程序同级目录下的【SwitchyOmega_GBF.bak】文件导入。
4. 在浏览器右上角插件图标处，切换选定为【GBF_AutoSwitch】。

（备用方式：如果不想装插件，可在软件界面直接勾选【自动配置 Windows 系统 PAC 代理】）

第 2 步：启动本加速器与科学上网代理
-----------------------------------------------------------------
1. 确保你的 Clash Verge / Clash / v2rayN 已开启并连上日本节点。
2. 双击运行 GBF_Accelerator.exe。
3. 浏览器打开 game.granbluefantasy.jp 即可畅玩！

=================================================================
【技术原理与免责声明】：
- 本工具为网络层本地静态资源缓存与透明代理，不修改任何游戏内数据、协议包或战斗参数。
- 游戏静态资源（立绘、音频、脚本）自动保存在本地，重复加载由本地毫秒级直接响应，减少跨海 CDN 延迟。
- 游戏核心 API（抽卡、编队、结算、多人战等）原样透明转发至上游代理，保持 Cygames 原生 CORS 与头部一致。
- 免责声明：本软件为第三方开源网络优化工具，用户请遵守 Cygames 游戏使用条款，使用风险自负。
"""

def get_pac_content(port: int = 8124) -> str:
    """Generate PAC script content pointing to the specified local port."""
    return f"""function FindProxyForURL(url, host) {{
    if (
        shExpMatch(host, "*.granbluefantasy.jp") ||
        shExpMatch(host, "granbluefantasy.jp") ||
        shExpMatch(host, "*.granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.com") ||
        shExpMatch(host, "*granbluefantasy.akamaized.net") ||
        shExpMatch(host, "*gbf.akamaized.net") ||
        shExpMatch(host, "*.mbga.jp") ||
        shExpMatch(host, "mbga.jp") ||
        shExpMatch(host, "rcv.a-i-ad.com") ||
        shExpMatch(host, "*.smbeat.jp") ||
        shExpMatch(host, "*.smrtbeat.com") ||
        shExpMatch(host, "*datadoghq-browser-agent*") ||
        shExpMatch(host, "*google-analytics.com") ||
        shExpMatch(host, "*googletagmanager.com")
    ) {{
        return "PROXY 127.0.0.1:{port}; DIRECT";
    }}
    return "DIRECT";
}}
"""

PAC_CONTENT = get_pac_content(8124)

def update_pac_file(port: int = 8124):
    """Write or update proxy.pac in base directory with the specified port."""
    base_dir = get_base_dir()
    pac_file = base_dir / "proxy.pac"
    try:
        with open(pac_file, "w", encoding="utf-8") as f:
            f.write(get_pac_content(port))
    except Exception:
        pass

def ensure_bundled_files():
    """Ensure auxiliary helper files exist in the base directory."""
    base_dir = get_base_dir()
    port = config_manager.get_listen_port()

    # 1. proxy.pac
    update_pac_file(port)
    pac_file = base_dir / "proxy.pac"

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
    print("       本加速器需信任根证书以解密并实现 Akamai 静态资源本地极速响应。")
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
    print("      GBF Accelerator - 碧蓝幻想本地缓存加速代理")
    print("      基于 Clash + SSD 本地缓存的高速进本方案")
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
