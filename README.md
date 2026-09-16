# GBF Accelerator

碧蓝幻想（Granblue Fantasy）本地静态资源缓存与加速代理工具。

[![Release](https://img.shields.io/github/v/release/Sagisawa/GBF-Accelerator?color=blue&logo=github)](https://github.com/Sagisawa/GBF-Accelerator/releases)
[![Python](https://img.shields.io/badge/Python-3.10%2B-blue.svg)](https://www.python.org/)
[![Platform](https://img.shields.io/badge/Platform-Windows-lightgrey.svg)]()
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

通过将游戏静态资源（立绘、音频、战斗动画、脚本）本地化缓存至 SSD / 内存中，减少静态资源的跨海重复下载，降低重复加载延迟与流量消耗；同时透明联动 Clash / v2rayN 等上游代理，核心游戏 API（抽卡、编队、结算、多人战等）原样转发、不做修改。

> 📥 **下载开箱即用版**：前往 [Releases 页面](https://github.com/Sagisawa/GBF-Accelerator/releases) 下载最新绿色便携包 `GBF_Accelerator_v1.7.1_GUI.zip`，解压即用，无需配置 Python 环境。各版本改动参见 [CHANGELOG.md](CHANGELOG.md)。

---

## 主要功能

- **动态 API / 静态素材双通道物理隔离 (Dual-Client)**：
  - **动态 API 通道 (`api_client`)**：针对 `game.granbluefantasy.jp` 专设独立 HTTP/1.1 连接池，避免协议降级开销与队头排队。
  - **静态素材通道 (`asset_client`)**：针对 Akamai CDN 启用 HTTP/2 多路复用并发通道。
  - **连接池互不影响**：后台 Prefetch 批量并发预加载素材时，动态 API 拥有专属长连接通道，减少与静态流量的连接排队冲突。
- **白名单只读接口失效重试 (Safe Stale-Retry)**：
  - **写请求不重试**：普攻、技能、召唤等所有 POST 请求以及未知路径不进行重试，避免重复触发。
  - **只读白名单断连重试**：仅对 `/rest/multiraid/start.json`、`/rest/raid/start.json` 等已识别的只读接口，在未收到响应且底层空闲长连接断开（`ConnectError` / `RemoteProtocolError` / `ReadError`）时，自动执行最多 1 次重新建连重发。
- **GUI 实时日志查看**：主界面底栏提供「实时日志」窗口，显示请求耗时、状态及连接是否复用（`reused` / `new`），支持关键词检索与按分类筛选。
- **API 延迟与连接复用统计**：通过底层传输流统计连接复用情况，并记录耗时分布（P50 / P95 / P99），支持在配置中开启或关闭。
- **静态资源本地加速与内存热点缓存 (RAM Cache)**：首次拉取的静态资源原子落盘至本地；高频静态资源直接载入内存（默认上限 256MB），读取不经磁盘；启动时自动预热高频小文件。
- **资源预加载 (Prefetch)**：自动解析场景 JS/JSON 及 CreateJS 动画引用的素材路径，使用协议合规的最小标头在后台低并发预热落盘，让首次进入新副本/活动时的大部分素材提前就位。
- **素材 Magic Bytes 二进制校验与一键体检自愈**：极速校验 PNG / JPEG / WebP / GIF / MP3 / WOFF 等二进制文件头（纳秒级），杜绝 0 字节损坏文件及 502/503 伪装 HTML 落盘；GUI 提供“一键体检缓存”按钮支持一键扫描与坏件自愈。
- **CDN 静态资源 HTTP/2 多路复用**：针对 Akamai CDN 静态资源与后台预加载启用 HTTP/2，复用连接并发拉取资源，减少逐条 TCP+TLS 跨海握手。
- **上游延迟测试**：一键测量经当前上游代理到游戏服务器的真实往返延迟，辅助选择最优 Clash 节点。
- **支持复用现有缓存**：支持自定义缓存存储路径，可自动检测并无缝复用已有缓存目录（如 ACGPower 等工具的历史缓存）。
- **系统 PAC 代理支持**：
  - 支持一键开启 Windows 系统 PAC 自动配置，开启后无需在浏览器安装任何插件即可生效。
  - 内置 PAC 服务（默认 `http://127.0.0.1:8124/proxy.pac`），精准收敛分流规则，也支持配合 SwitchyOmega / ZeroOmega 等扩展使用。
- **图形界面与系统托盘**：Windows 原生控件风格美化与 DPI 自适应清晰渲染；支持自定义监听端口（默认 8124）及上游代理，支持最小化至系统托盘后台静默运行。
- **局域网共享与移动端支持**：可在 GUI 中勾选“允许局域网连接”，支持同一 Wi-Fi 下的 iPhone / iPad / Android 设备接入（内置私网 ACL 防护与 PAC 分流指引）。
- **开机自启**：可在 GUI 中选择随 Windows 启动，启动后自动隐藏到系统托盘；默认关闭。
- **直连模式**：在 GUI 中勾选后，动态请求和长连接改用本机网络直连，同时继续使用本地静态缓存；取消勾选即可立即恢复上游代理模式。
- **透明代理与原始内容准则 (v1.7.1)**：动态请求与官方心跳直达游戏服务器，静态脚本保持原始内容交付；移除发往浏览器的内部调试标头，减少代理干预，运行更轻量稳定。

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
  "enable_browser_cache": true,
  "enable_auto_repair": true,
  "enable_prefetch": true,
  "enable_ram_warmup": true,
  "verify_upstream_tls": true,
  "shimakaze_mode": false,
  "api_max_connections": 16,
  "api_max_keepalive": 4,
  "api_keepalive_expiry": 20.0,
  "asset_max_connections": 100,
  "asset_max_keepalive": 40,
  "asset_keepalive_expiry": 60.0,
  "enable_api_telemetry": true
}
```

- `upstream_proxy`: 上游代理地址。
- `direct_mode`: 直连模式开关；开启后不使用上游代理，但本地缓存仍然生效。
- `shimakaze_mode`: 岛风GO 兼容优化模式（默认关闭；开启后放宽上游超时至 25s/12s、适配岛风GO自签证书、自动对 GET/HEAD 请求进行断线与网关超时自愈重试）。
- `listen_port`: 本地加速服务监听端口（默认 8124，支持在界面中自定义）。
- `cache_dir`: 静态资源缓存落盘路径。
- `auto_system_proxy`: 启动加速时是否自动挂载 Windows 系统 PAC 代理。
- `auto_start`: 是否随 Windows 启动并自动缩小到系统托盘（默认关闭）。
- `enable_ram_cache`: 启用内存热点缓存（默认开启，占用约 256MB 内存，高频资源读取不经磁盘）。
- `ram_cache_max_mb`: 内存热点缓存上限，单位 MB（默认 256，范围 16–8192，可在图形界面中直接设置并立即生效）。
- `enable_browser_cache`: 启用浏览器强缓存与渲染留存（默认开启；仅对带版本哈希/时间戳的不可变资源注入 `immutable`）。
- `enable_auto_repair`: 自动检测并清除损坏/0字节缓存文件并重新拉取（默认开启）。
- `enable_prefetch`: 资源预加载开关（默认开启）；解析场景 JS/JSON 引用的素材路径，在后台低并发预热下载。
- `enable_ram_warmup`: 启动预热开关（默认开启）；启动时把缓存目录中的高频小文件预载入 RAM Cache，消除首读磁盘延迟。
- `verify_upstream_tls`: 请求上游时是否校验上游 TLS 证书（默认开启，提升网络传输安全性）。
- `api_max_connections`: 动态 API 专用连接池最大允许总连接数（默认 16）。
- `api_max_keepalive`: 动态 API 连接池最多保留的空闲保活连接数（默认 4，可试验档位 1/2/4/8）。
- `api_keepalive_expiry`: 动态 API 空闲连接保活时长，单位秒（默认 20.0）。
- `asset_max_connections`: 静态素材与预加载专用连接池最大连接数（默认 100）。
- `asset_max_keepalive`: 静态素材连接池最多保留的空闲保活连接数（默认 40）。
- `asset_keepalive_expiry`: 静态素材空闲连接保活时长，单位秒（默认 60.0）。
- `enable_api_telemetry`: 是否启用低开销 API 尾延迟与连接复用遥测（默认开启）。

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
- **精准分流规则**：PAC 脚本及 SwitchyOmega 规则严格限制为 GBF 主站域名、标准版 CDN、以及 Steam 版专用 CDN（`prd-game-a*-granbluefantasy-steam.akamaized.net`），不通配公共 `*.akamaized.net`，其他使用 Akamai CDN 的应用流量不受影响。
- **缓存原子落盘与完整性校验**：静态资源下载采用临时文件（`.tmp`）及原子替换（`os.replace` + `fsync`），防止写入意外中断产生半截残损文件；同时校验非 HTML 静态资源的内容完整性与 MIME，避免将上游错误 HTML 页面误存为持久缓存。
- **动态 API 原样透明转发与心跳直达**：所有动态接口（抽卡、编队、结算、`/ob/r` 在线心跳、`/socket/` 多人战等）均原样透传，不篡改业务正文、不伪造任何 Mock 响应，保留 Cygames 原生 CORS 响应头。
- **纯净响应与零私有头泄露**：向浏览器交付的所有静态素材与动态 API 响应中，绝不注入任何 `X-Proxy-*` 私有调试头，确保网页端探针零泄露。
- **免责声明**：本软件为开源网络辅助与本地静态资源缓存工具，不篡改任何游戏数据、前端脚本或内存。请遵守 Cygames 最终用户许可协议，使用风险自负。
