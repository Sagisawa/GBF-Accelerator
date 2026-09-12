# GBF Accelerator

碧蓝幻想（Granblue Fantasy）本地静态资源缓存与加速代理工具。

[![Release](https://img.shields.io/github/v/release/Sagisawa/GBF-Accelerator?color=blue&logo=github)](https://github.com/Sagisawa/GBF-Accelerator/releases)
[![Python](https://img.shields.io/badge/Python-3.10%2B-blue.svg)](https://www.python.org/)
[![Platform](https://img.shields.io/badge/Platform-Windows-lightgrey.svg)]()
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

通过将游戏静态资源（立绘、音频、战斗动画、脚本）本地化缓存至 SSD / 内存中，大幅减少跨海 CDN 网络延迟与流量消耗；同时透明联动 Clash / v2rayN 等上游代理，保证核心游戏 API（抽卡、编队、结算、多人战等）零篡改、原样稳定传输。

> 📥 **下载开箱即用版**：前往 [Releases 页面](https://github.com/Sagisawa/GBF-Accelerator/releases) 下载最新绿色便携包 `GBF_Accelerator_v1.3.0_GUI.zip`，解压即用，无需配置 Python 环境。

---

## 主要功能

- **静态资源本地加速**：首次加载的静态资源通过上游代理拉取并原子落盘缓存至本地，后续请求直接由本地极速响应。
- **内存热点缓存 (RAM Cache)**：高频静态资源直接载入内存（默认上限 256MB），0 磁盘 I/O 毫秒级极速直出。
- **支持复用现有缓存**：支持自定义缓存存储路径，可自动检测并无缝复用已有缓存目录（如 ACGPower 等工具的历史缓存）。
- **系统 PAC 代理支持**：
  - 支持一键开启 Windows 系统 PAC 自动配置，开启后无需在浏览器安装任何插件即可生效。
  - 内置 PAC 服务（默认 `http://127.0.0.1:8124/proxy.pac`），精准收敛分流规则，也支持配合 SwitchyOmega / ZeroOmega 等扩展使用。
- **图形界面与系统托盘**：Windows 原生控件风格美化与 DPI 自适应清晰渲染；支持自定义监听端口（默认 8124）及上游代理，支持最小化至系统托盘后台静默运行。
- **开机自启**：可在 GUI 中选择随 Windows 启动，启动后自动隐藏到系统托盘；默认关闭。
- **直连模式**：在 GUI 中勾选后，动态请求和长连接改用本机网络直连，同时继续使用本地静态缓存；取消勾选即可立即恢复上游代理模式。
- **缓存原子落盘与自愈防护**：采用临时文件（`.tmp`）原子替换（`os.replace` + `fsync`），防止写入异常产生损坏残卷；自动识别上游 HTML 错误页并拒绝落盘；自动识别并清理 0 字节损坏文件。
- **冗余请求拦截**：自动拦截游戏附带的第三方埋点与统计上报请求，减少无效网络连接。

---

## 使用方法

1. **准备上游代理**：确保你的代理客户端（如 Clash Verge、Clash、v2rayN 等）正常运行并连接至可用节点。
2. **运行程序**：启动 `GBF_Accelerator`（或通过 Python 运行 `gui_main.py`）。
3. **确认配置**：
   - 检查【上游代理端口】是否与本机代理客户端一致（Clash Verge 常见为 `7897`，Clash 常见为 `7890`，v2rayN 常见为 `10809`，岛风 GO 默认为 `8099`）。点击“自动探测”时，如果同时发现多个代理，会弹出列表供选择。
   - 检查【本地监听端口】（默认 `8124`，可按需在界面中自定义）。
   - 确认【缓存保存目录】（默认存放在程序同级 `cache` 目录，也可指定已有缓存目录）。
4. **启动加速**：
   - **方式一（推荐，系统 PAC）**：勾选“自动配置系统 PAC 代理”，点击“启动加速”。启动后直接在浏览器中打开游戏页面即可（停止加速或退出软件时会自动恢复系统网络设置）。
   - **方式二（浏览器扩展）**：若使用 SwitchyOmega 等扩展，可在扩展中添加 PAC 规则并指向 `http://127.0.0.1:8124/proxy.pac`（若修改了端口请对应调整）。

---

## 配置文件说明

程序首次运行后会在当前目录下生成 `config.json`，也可通过图形界面直接修改：

```json
{
  "upstream_proxy": "http://127.0.0.1:7897",
  "direct_mode": false,
  "listen_port": 8124,
  "cache_dir": "D:\\gbf_cache",
  "auto_system_proxy": true,
  "auto_start": false,
  "enable_ram_cache": true,
  "enable_browser_cache": false,
  "enable_auto_repair": true,
  "verify_upstream_tls": true
}
```

- `upstream_proxy`: 上游代理地址。
- `direct_mode`: 直连模式开关；开启后不使用上游代理，但本地缓存仍然生效。
- `listen_port`: 本地加速服务监听端口（默认 8124，支持在界面中自定义）。
- `cache_dir`: 静态资源缓存落盘路径。
- `auto_system_proxy`: 启动加速时是否自动挂载 Windows 系统 PAC 代理。
- `auto_start`: 是否随 Windows 启动并自动缩小到系统托盘（默认关闭）。
- `enable_ram_cache`: 启用内存热点缓存（默认开启，占用约 256MB 内存，高频资源 0 磁盘 I/O 极速直出）。
- `enable_browser_cache`: 启用浏览器强缓存与渲染留存（默认关闭；开启时仅对带版本哈希/时间戳的不可变资源如 `/assets/<timestamp>/...` 注入 `immutable`，普通未版本化资源不注入，避免更新时产生陈旧缓存）。
- `enable_auto_repair`: 自动检测并清除损坏/0字节缓存文件并重新拉取（默认开启）。
- `verify_upstream_tls`: 请求上游时是否校验上游 TLS 证书（默认开启，提升网络传输安全性）。

---

## 开发与构建

### 环境要求
- Python 3.10+
- Windows 10 / 11

### 从源码运行

```bash
git clone https://github.com/Sagisawa/GBF-Accelerator.git
cd GBF-Accelerator

# 创建并激活虚拟环境
python -m venv .venv
.venv\Scripts\activate

# 安装依赖
pip install -r requirements.txt

# 运行图形界面
python gui_main.py
```

### 打包为独立可执行文件

项目使用 PyInstaller 进行打包：

```bash
python build_exe.py
```

打包完成后，可执行文件位于 `dist/GBF_Accelerator.exe`。

---

## 安全与技术说明

- **根证书透明管理**：根证书仅在首次运行时由本机独立生成私钥与证书，私钥严格保存在本地 `certs/` 目录不外泄。主界面实时展示证书的 SHA-256 指纹，并提供“一键注销/卸载根证书”功能，方便随时清理受信任根证书。
- **精准分流规则**：PAC 脚本及 SwitchyOmega 规则严格限制为 GBF 主站域名、标准版 CDN、以及 Steam 版专用 CDN（`prd-game-a*-granbluefantasy-steam.akamaized.net`），绝不通配公共 `*.akamaized.net`，确保其他使用 Akamai CDN 的国际应用流量不受任何干扰。
- **缓存原子落盘与完整性校验**：静态资源下载采用临时文件（`.tmp`）及原子替换（`os.replace` + `fsync`），防止写入意外中断产生半截残损文件；同时严格校验非 HTML 静态资源的内容完整性与 MIME，杜绝将上游错误 HTML 页面误作为资源持久缓存。
- **动态 API 原样透明转发**：所有动态接口（抽卡、编队、结算、`/socket/` 多人战等）均保持 100% 原样透明转发，严格保留 Cygames 原生 CORS 响应头，绝不干预、注入或缓存动态数据，杜绝多人战不同步与状态不一致风险。
- **免责声明**：本软件为开源网络辅助与本地静态资源缓存工具，不篡改任何游戏数据或内存。请遵守 Cygames 最终用户许可协议，使用风险自负。
