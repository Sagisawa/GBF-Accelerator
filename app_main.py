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
    get_resource_dir,
    config_manager,
    is_ca_installed,
    install_ca_certificate,
    is_port_open,
)
from cert_manager import ensure_ca, CA_CERT_PATH
from cache_manager import cache_manager
import gbf_proxy

USAGE_TEXT = """=================================================================
   GBF 加速器（GBF Accelerator）使用说明
=================================================================

【只需 2 步】：

第 1 步：配置浏览器分流（推荐使用 ZeroOmega 或 SwitchyOmega 插件）
-----------------------------------------------------------------
1. 打开 Chrome / Edge / 任意 Chromium 浏览器，安装 ZeroOmega 扩展。
2. 打开插件设置 -> 【导入/导出】 -> 点击【从备份文件恢复】。
3. 选择本程序同级目录下的【SwitchyOmega_GBF.bak】文件导入。
4. 在浏览器右上角插件图标处，切换选定为【GBF_AutoSwitch】。

（备用方式：如果不想装插件，可在软件界面直接勾选【自动配置 Windows 系统 PAC 代理】）

第 2 步：启动本加速器与上游代理
-----------------------------------------------------------------
1. 确保你的 Clash Verge / Clash / v2rayN 已开启并连接到可用节点。
2. 双击运行 GBF_Accelerator.exe。
3. 浏览器打开 game.granbluefantasy.jp 即可开始游戏。

上游代理也支持岛风 GO，默认地址为 `http://127.0.0.1:8099`。修改地址后点击“确认”，会立即断开旧连接并切换到新代理；“自动探测”也会检测该端口。若同时检测到多个代理，会弹出列表供选择。

如果不想经过上游代理，可在 GUI 的“上游网络代理”下勾选“直连模式”。该模式立即切换到本机网络，同时继续使用本地缓存；取消勾选即可立即恢复上游代理。

如需随 Windows 自动启动，可在 GUI 中勾选“开机自启”；启动后程序会自动缩小到系统托盘。该选项默认关闭。

如需共享给同一 Wi-Fi 下的手机/iPad/备用机加速：
可在界面勾选【允许局域网连接 (Allow LAN)】（该选项默认关闭），按弹出的指南为手机配置 Wi-Fi 代理并安装根证书即可。

=================================================================
【实际效果与限制】：
- 本工具只缓存静态资源（立绘、音频、脚本、图片等）。这类资源首次加载仍需经过网络下载，加速效果体现在之后的重复加载：第二次起由本地磁盘或内存直接响应，不再走跨海网络。
- 游戏动态请求（抽卡、编队、战斗结算、多人战等）不做任何缓存或修改，原样转发给上游代理。这部分延迟完全取决于上游代理节点质量，本工具无法改善。
- 内存缓存上限、资源预加载等性能选项可在界面“性能与系统资源选项”中调整。

【技术原理与免责声明】：
- 本工具为网络层本地静态资源缓存与透明代理，不修改任何游戏内数据、协议包或战斗参数。
- 游戏核心 API（抽卡、编队、结算、多人战等）原样转发至上游代理，保留 Cygames 原生响应头。
- 本工具不对账号安全作任何保证。使用第三方网络工具存在违反游戏服务条款的可能，是否使用请自行评估，风险自负。
"""

def get_pac_content(port: int = 8124, host: str = "127.0.0.1", *args, **kwargs) -> str:
    """Generate PAC script content pointing to the specified host and port."""
    return f"""function FindProxyForURL(url, host) {{
    if (
        shExpMatch(host, "*.granbluefantasy.jp") ||
        shExpMatch(host, "granbluefantasy.jp") ||
        shExpMatch(host, "*.granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.com") ||
        shExpMatch(host, "granbluefantasy.akamaized.net") ||
        shExpMatch(host, "*.granbluefantasy.akamaized.net") ||
        shExpMatch(host, "gbf.akamaized.net") ||
        shExpMatch(host, "*.gbf.akamaized.net") ||
        shExpMatch(host, "prd-game-a-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a1-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a2-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a3-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a4-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a5-granbluefantasy.akamaized.net") ||
        shExpMatch(host, "prd-game-a-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a1-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a2-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a3-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a4-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "prd-game-a5-granbluefantasy-steam.akamaized.net") ||
        shExpMatch(host, "*.game.mbga.jp") ||
        shExpMatch(host, "gbf.game.mbga.jp") ||
        shExpMatch(host, "*.sp.pf.mbga.jp") ||
        shExpMatch(host, "*.pf.mbga.jp") ||
        shExpMatch(host, "*.sp.mbga.jp") ||
        shExpMatch(host, "sp.mbga.jp") ||
        shExpMatch(host, "*.mbga.jp") ||
        shExpMatch(host, "mbga.jp") ||
        shExpMatch(host, "*.connect.mobage.jp") ||
        shExpMatch(host, "connect.mobage.jp") ||
        shExpMatch(host, "*.game.mobage.jp") ||
        shExpMatch(host, "gbf.game.mobage.jp") ||
        shExpMatch(host, "*.mobage.jp") ||
        shExpMatch(host, "mobage.jp")
    ) {{
        return "PROXY {host}:{port}; DIRECT";
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
        source_readme = get_resource_dir() / "使用说明.txt"
        if source_readme.is_file():
            try:
                import shutil
                shutil.copy2(source_readme, readme_file)
            except Exception:
                pass
        else:
            try:
                with open(readme_file, "w", encoding="utf-8") as f:
                    f.write(USAGE_TEXT.format(pac_path=str(pac_file).replace("\\", "/")))
            except Exception:
                pass

    # 3. SwitchyOmega_GBF.bak (if source exists in source tree or bundled)
    bak_dest = base_dir / "SwitchyOmega_GBF.bak"
    if not bak_dest.is_file():
        source_bak = get_resource_dir() / "SwitchyOmega_GBF.bak"
        if not source_bak.is_file():
            source_bak = Path(__file__).parent / "SwitchyOmega_GBF.bak"
        if source_bak.is_file():
            try:
                import shutil
                shutil.copy2(source_bak, bak_dest)
            except Exception:
                pass

def _print_unix_setup_instructions():
    """Print one-time CA-trust + browser-proxy setup instructions for macOS / Linux."""
    ca_path = get_base_dir() / "certs" / "ca.crt"
    ca_path_str = str(ca_path)
    listen_port = config_manager.get_listen_port()
    proxy_url = f"http://127.0.0.1:{listen_port}"

    print()
    print("=" * 65)
    print("   macOS / Linux (nogui) 一次性配置指引")
    print("=" * 65)
    print()
    print(f"[1] 浏览器 HTTP/HTTPS 代理设置为：")
    print(f"      {proxy_url}")
    print("    (Chrome / Edge: 设置 → 系统 → 打开代理设置 → 手动配置代理；")
    print("     Firefox: 设置 → 网络设置 → 手动配置代理 → HTTP/HTTPS 填同一地址)")
    print()
    print("[2] 信任根证书（一次性，二选一）：")
    print()
    print("    macOS (系统级，影响所有浏览器):")
    print(f"      sudo security add-trusted-cert -d -r trustRoot \\")
    print(f"          -k /Library/Keychains/System.keychain {ca_path_str}")
    print()
    print("    macOS (仅当前用户 Safari / Chrome, 不需要 sudo):")
    print(f"      security add-trusted-cert -r trustRoot \\")
    print(f"          -k ~/Library/Keychains/login.keychain-db {ca_path_str}")
    print()
    print("    Linux (Debian / Ubuntu 系统级):")
    print(f"      sudo cp {ca_path_str} /usr/local/share/ca-certificates/gbf-accelerator.crt")
    print("      sudo update-ca-certificates")
    print()
    print("    Linux (仅 Firefox / Chrome 当前用户, 不需要 sudo):")
    print(f"      certutil -A -n GBF-Accelerator -t C,C \\")
    print(f"          -i {ca_path_str} -d sql:$HOME/.pki/nssdb")
    print()
    print("=" * 65)
    print()

def check_ca_setup():
    """Verify and prompt to install Root CA. Windows: real install; unix: print instructions."""
    ensure_ca()
    if sys.platform == "win32":
        if is_ca_installed():
            print("   [+] 根证书状态: [已信任] (HTTPS 缓存已就绪)")
            return

        print("\n" + "!" * 65)
        print("   [!] 检测到本机尚未安装加速根证书！")
        print("       本加速器需信任根证书才能解密并缓存 Akamai 静态资源。")
        print("       正在自动调用系统证书管理器为你安装...")
        print("       >>> 稍后弹出的 Windows 安全警告窗口中，请点击【是 (Y)】<<<")
        print("!" * 65 + "\n")

        success = install_ca_certificate(CA_CERT_PATH)
        if is_ca_installed():
            print("   [+] 根证书安装成功并已受信！\n")
        else:
            print(f"   [!] 证书未自动安装，你也可以双击运行 certs/ca.crt 手动安装到【受信任的根证书颁发机构】。\n")
        return

    # macOS / Linux: cert is generated on disk; user must trust it manually.
    print("   [*] 根证书已生成到 certs/ca.crt（macOS / Linux 需手动信任，见下方启动说明）。")

def main():
    base_dir = get_base_dir()
    print("=" * 65)
    print("      GBF Accelerator - 碧蓝幻想本地缓存加速代理")
    print("      基于上游代理与本地静态资源缓存的加速工具")
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
    if sys.platform != "win32":
        _print_unix_setup_instructions()
    print("   --------------------------------------------------------------")
    try:
        asyncio.run(gbf_proxy.main())
    except KeyboardInterrupt:
        print("\n[*] 加速器已安全退出。")

if __name__ == "__main__":
    main()
