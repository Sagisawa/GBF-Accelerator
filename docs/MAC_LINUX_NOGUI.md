# GBF Accelerator — macOS / Linux (nogui) 使用说明

> **macOS 用户须知**：
> macOS 已支持完整的原生图形与无头版本（内嵌 Web 控制台、Menu Bar 顶部状态栏常驻菜单、一键 CA 根证书自动信任、`networksetup` 系统代理自动托管以及跨平台更新检查）。
> macOS 用户推荐直接运行打包好的 `GBF_Accelerator`（或源码模式运行 `./start_proxy.sh` / `cd engine && go run .`）。
> **本文档仅面向**：在 **Linux** 系统，或在 **macOS** 上以纯**命令行无头模式 (--headless)** 运行的用户。
> Windows 用户请参考随程序分发的《使用说明.txt》。

---

## 1. 运行模式与平台能力对比

| 项目 | Windows GUI 版 | macOS 原生版 | nogui 命令行模式 (Linux / macOS 无头) |
|---|---|---|---|
| 启动方式 | 双击 `GBF_Accelerator.exe` | 双击 `GBF_Accelerator` 或 `./start_proxy.sh` | `./GBF_Accelerator --headless` 或 `go run . --headless` |
| 系统代理 | 自动写入注册表 + WinINet 通知 | 自动调用 `networksetup` 配置系统代理与直连绕过列表 | **不自动写入**，需在浏览器手动设置 HTTP/HTTPS 代理 |
| 根证书信任 | 自动调用 `certutil -addstore` | 自动调用 `security add-trusted-cert` 信任 Keychain 根证书 | **不自动写入**，需在系统或浏览器证书库手动信任 |
| 开机自启 | 写入 `HKCU\...\Run` | 写入 LaunchAgents plist | **不提供** |
| 托盘 / 状态栏 | Windows 任务栏通知区托盘图标 | macOS Menu Bar 顶部状态栏常驻图标 | **不提供** |
| 静态资源缓存 | ✓ | ✓ | ✓ |
| 动态 API 透明转发 | ✓ | ✓ | ✓ |
| 连接池 / 预加载调度 | ✓ | ✓ | ✓ |
| 更新检查 | 自动检测更新 | 自动检测更新（跨平台过滤） | 自动检测更新 |

**核心原则与 Windows 版一致**：本工具为网络层本地静态资源缓存与透明代理，不修改任何游戏内数据、协议包或战斗参数；游戏核心 API（抽卡、编队、结算、多人战等）原样转发至上游代理，保留 Cygames 原生响应头；本工具不对账号安全作任何保证，使用第三方网络工具存在违反游戏服务条款的可能，是否使用请自行评估，风险自负。

---

## 2. 环境要求

- **Go 1.21+**（推荐 1.22 / 1.23，源码编译时需要）
- **OpenSSL / libnss3 工具链**（仅 Linux 信任证书时需要 `certutil`，Debian/Ubuntu：`apt install libnss3-tools`）
- **Git**（可选，仅从源码克隆时需要）
- **网络访问**到 GitHub / Akamai CDN
- 一个可用的上游代理（Clash Verge / Clash / v2rayN / 任意 HTTP/SOCKS5），或选择「直连模式」

---

## 3. 安装与启动

### 3.1 准备源码

```bash
git clone https://github.com/Sagisawa/GBF-Accelerator.git
cd GBF-Accelerator
```

### 3.2 首次启动

```bash
cd engine
go run . --headless
```

首次启动时程序会：

1. 在程序同目录生成 `certs/ca.crt`、`certs/*.key`；
2. 在程序同目录生成 `proxy.pac` 与《使用说明.txt》；
3. 自动探测本机 7897 / 7890 / 10808 / 10809 等常见上游代理端口（Clash / v2rayN），也可在 `config.json` 手动指定；
4. 启动本地代理服务并监听 `http://127.0.0.1:8124`（默认端口，可在 `config.json` 中修改），控制面监听 `http://127.0.0.1:8125`。

按 `Ctrl+C` 即可安全退出。

### 3.3 后续启动

直接再次执行 `go run . --headless` 或运行预编译二进制 `./bin/GBF_Accelerator --headless` 即可。已生成的证书与配置会自动复用。

---

## 4. 一次性配置（每个新设备执行一次）

### 4.1 浏览器代理地址

代理模式（HTTP/HTTPS 代理）：

```
http://127.0.0.1:8124
```

在浏览器中配置手动代理：

- **Chrome / Edge**：设置 → 系统 → 打开代理设置 → 「手动设置代理」→ HTTP / HTTPS 代理均填 `127.0.0.1`，端口 `8124`，`不使用代理` 列表留空。
- **Firefox**：设置 → 网络设置 → 「手动配置代理」→ HTTP / HTTPS 代理均填 `127.0.0.1`，端口 `8124`，勾选「也将此代理用于 HTTPS」。
- **Safari**（仅 macOS）：系统设置 → 网络 → 详情 → 代理 → 「手动配置代理」→ Web 代理 / 安全 Web 代理均填 `127.0.0.1:8124`。

> 直连模式（不经过上游代理）可在 `config.json` 中设置 `"direct_mode": true`，浏览器代理地址保持不变；本工具只优化静态资源加载，不替代你的上游网络。

### 4.2 信任根证书

根证书由程序生成于 `certs/ca.crt`。**在 nogui 命令行模式下，程序不会自动写入系统证书库**，你需要按下列方式手动信任一次（macOS 用户推荐直接执行项目根目录下的 `./install_ca.sh` 一键自动导入并信任）：

#### macOS

**方式一（推荐，一键脚本）：**

```bash
./install_ca.sh
```

**方式二（手动命令，仅当前用户 Safari / Chrome，不需要 sudo）：**

```bash
security add-trusted-cert -r trustRoot \
    -k ~/Library/Keychains/login.keychain-db ./certs/ca.crt
```

**方式三（手动系统级，影响所有浏览器，需要 sudo，会要求输入密码）：**

```bash
sudo security add-trusted-cert -d -r trustRoot \
    -k /Library/Keychains/System.keychain ./certs/ca.crt
```

> Firefox 使用独立证书库。若要让 Firefox 也信任，需要在 Firefox 设置 → 隐私与安全 → 证书 → 查看证书 → 证书颁发机构 → 导入 `certs/ca.crt`，并勾选「信任由此证书颁发机构标识的网站」。

#### Linux

**Debian / Ubuntu（系统级，影响所有 Chromium 应用，需要 sudo）：**

```bash
sudo cp ./certs/ca.crt /usr/local/share/ca-certificates/gbf-accelerator.crt
sudo update-ca-certificates
```

**仅 Firefox / Chrome 当前用户（不需要 sudo）：**

```bash
mkdir -p $HOME/.pki/nssdb
certutil -A -n GBF-Accelerator -t C,C \
    -i ./certs/ca.crt -d sql:$HOME/.pki/nssdb
```

> 其他发行版（Arch / Fedora 等）请使用各自系统提供的 `trust anchor` / `update-ca-trust` 机制，或直接使用 NSS 命令。

---

## 5. 配置文件

配置文件由 `config_manager` 维护，路径：

```
<脚本所在目录>/config.json
```

常用字段（与 Windows 版一致）：

| 字段 | 默认值 | 含义 |
|---|---|---|
| `listen_host` | `127.0.0.1` | 本地监听地址；局域网共享改为 `0.0.0.0` |
| `listen_port` | `8124` | 本地监听端口 |
| `allow_lan` | `false` | 是否允许局域网内其他设备连入 |
| `upstream_proxy` | `auto` | 上游代理；`auto` 自动探测 7897/7890/10808/10809；也可填 `http://127.0.0.1:7897` 等 |
| `direct_mode` | `false` | 直连模式（仍使用本地缓存，但跳过上游代理） |
| `cache_dir` | `auto` | 静态资源缓存目录 |
| `asset_max_connections` | `100` | 静态素材连接池上限（HTTP/2 多路复用，无需调高） |
| `asset_max_keepalive` | `40` | 静态素材 Keep-Alive 上限 |
| `enable_prefetch` | `true` | 后台预加载缺失素材（与 Windows 版行为一致：平滑调度、避让前台） |
| `enable_ram_cache` | `true` | 内存热缓存开关 |

修改后重启 `python3 app_main.py` 生效。

---

## 6. 验证

启动后浏览器访问任意 `*.granbluefantasy.jp` 页面，检查：

1. 浏览器代理设置已指向 `http://127.0.0.1:8124`；
2. 终端中能看到类似 `[proxy] GET http://game.granbluefantasy.jp/... 200 (cache: HIT)` 的访问日志；
3. 静态资源（`.js` / `.css` / 音频 / 立绘）首次走上游代理，**第二次起显示 `cache: HIT`，不再产生上行网络流量**。

若浏览器报 `NET::ERR_CERT_AUTHORITY_INVALID`，说明 §4.2 的根证书未正确信任，重新执行对应平台的命令即可。

---

## 7. nogui 命令行模式不提供的功能（与 GUI 版的差异）

以下功能仅在图形界面版本（Windows GUI / macOS 原生 GUI）中内置，nogui 纯命令行模式下不提供：

- **图形化操作界面**：nogui 模式仅在终端输出运行日志，不弹出 Tkinter 图形窗口。
- **托盘 / 状态栏图标与菜单**：nogui 模式无任务栏托盘或 Menu Bar 状态栏常驻图标。
- **系统代理自动接管**：nogui 模式不修改系统网络设置，需手动在浏览器中配置代理。
- **根证书一键静默信任**：nogui 模式不自动写入系统钥匙串或证书库，需按 §4.2 手动信任。
- **开机自启配置**：nogui 模式不写入自启项，如需后台常驻建议使用 `systemd` 或 `launchd` 服务。
- **GUI 内嵌的更新提示**：nogui 模式不包含图形化更新弹窗。

这些是有意为之的命令行轻量化边界，不构成缺陷。如需图形界面体验，推荐使用各平台对应的 GUI 版本。

---

## 8. 故障排查

| 现象 | 可能原因 |
|---|---|
| 浏览器报 `ERR_CERT_AUTHORITY_INVALID` | §4.2 未执行；或 Firefox 需在「证书颁发机构」中单独导入 |
| 浏览器报 `ERR_PROXY_CONNECTION_FAILED` | 加速器未启动，或端口被占用（修改 `config.json` 的 `listen_port`） |
| 终端 `Address already in use` | 同上端口占用；`kill` 占用进程或换端口 |
| 终端 `ModuleNotFoundError: No module named 'httpx'` | §3.1 的 `pip install -r requirements.txt` 未执行 |
| 终端 `ImportError: libnss3.so` | §2 的 `libnss3-tools` 未安装（仅 Linux 信任证书场景需要） |
| macOS Keychain 命令要求输入密码 | 正常；「系统级」命令需要 sudo |

---

## 9. 项目合规与免责

- 本工具为**网络层本地静态资源缓存与透明代理**，不修改任何游戏内数据、协议包或战斗参数。
- 游戏核心 API（抽卡、编队、结算、多人战等）原样转发至上游代理，保留 Cygames 原生响应头。
- 本工具不对账号安全作任何保证。使用第三方网络工具存在违反游戏服务条款的可能，是否使用请自行评估，风险自负。

