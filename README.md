# GBF Accelerator

碧蓝幻想（Granblue Fantasy）本地静态资源缓存与代理转发工具。

通过在本地磁盘缓存游戏的静态资源（立绘、音频、战斗动画等），并结合已有上游代理分流转发，减少重复静态资源请求的网络延迟与流量消耗。

---

## 主要功能

- **静态资源本地缓存**：首次加载的静态资源通过上游代理拉取并缓存至本地磁盘，后续请求直接由本地服务高速响应。
- **支持复用现有缓存**：支持自定义缓存存储路径，可直接检测并复用已有缓存目录（如 ACGP 等工具的历史缓存）。
- **系统 PAC 代理支持**：
  - 支持一键开启 Windows 系统 PAC 自动配置，开启后无需在浏览器安装任何插件即可生效。
  - 内置 PAC 服务（`http://127.0.0.1:8080/proxy.pac`），也支持配合 SwitchyOmega / ZeroOmega 等浏览器扩展使用。
- **图形界面与系统托盘**：提供直观的配置界面，支持最小化至任务栏系统托盘在后台运行。
- **冗余请求拦截**：自动拦截游戏附带的第三方埋点与统计上报请求，减少无效网络连接。

---

## 使用方法

1. **准备上游代理**：确保你的代理客户端（如 Clash Verge、Clash、v2rayN 等）正常运行并连接至可用节点。
2. **运行程序**：启动 `GBF_Accelerator`（或通过 Python 运行 `gui_main.py`）。
3. **确认配置**：
   - 检查【上游代理端口】是否与本机代理客户端一致（Clash Verge 常见为 `7897`，Clash 常见为 `7890`，v2rayN 常见为 `10809`）。
   - 确认【缓存保存目录】（默认存放在程序同级 `cache` 目录，也可指定已有缓存目录）。
4. **启动加速**：
   - **方式一（推荐，系统 PAC）**：勾选“自动配置系统 PAC 代理”，点击“启动加速”。启动后直接在浏览器中打开游戏页面即可（停止加速或退出软件时会自动恢复系统网络设置）。
   - **方式二（浏览器扩展）**：若使用 SwitchyOmega 等扩展，可在扩展中添加 PAC 规则并指向 `http://127.0.0.1:8080/proxy.pac`。

---

## 配置文件说明

程序首次运行后会在当前目录下生成 `config.json`，也可通过图形界面直接修改：

```json
{
  "upstream_proxy": "http://127.0.0.1:7897",
  "local_port": 8080,
  "cache_dir": "D:\\gbf_cache",
  "auto_system_proxy": true
}
```

- `upstream_proxy`: 上游代理地址。
- `local_port`: 本地加速服务监听端口（默认 8080）。
- `cache_dir`: 静态资源缓存落盘路径。
- `auto_system_proxy`: 启动加速时是否自动挂载 Windows 系统 PAC 代理。

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

## 注意事项

- 本工具仅提供网络转发与静态资源本地缓存，不包含任何游戏数据篡改或自动化操作功能。
- 首次运行时如提示导入本地根证书，系用于代理转发与缓存 GBF 静态资源所必需，确认信任即可。
