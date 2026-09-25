# GBF-ACC SkyLeap Xposed 模块 (第一阶段 PoC)

本项目是 GBF-Accelerator 的最小 Xposed / LSPosed 验证模块，基于 **libxposed Modern API (API 100+)** 与原生 Kotlin 构建，专门用于验证：
**LSPosed → GBF-ACC Xposed 模块 → SkyLeap (com.dena.skyleap)** 的注入链路。

---

## 一、项目结构

本模块保持最小化设计，去除了所有无关的 UI/WebView/网络拦截代码：

```text
android/
├── build.gradle.kts                         # 根构建脚本 (AGP 9.3.2)
├── settings.gradle.kts                      # 仓库源配置 (Google + MavenCentral)
├── gradle.properties                        # JVM 与 AndroidX 配置
├── local.properties                         # SDK 路径指向
├── local.properties.example                 # SDK 路径配置参考
├── gradlew / gradlew.bat                    # 跨平台 Gradle 命令行包装器
├── gradle/wrapper/                          # Gradle 9.7.1 包装器文件
└── app/
    ├── build.gradle.kts                     # 模块依赖 (compileOnly libxposed:api:102.0.0)
    ├── proguard-rules.pro                   # 混淆保持规则
    └── src/main/
        ├── AndroidManifest.xml              # Xposed Modern API 元数据与作用域声明
        ├── java/com/sagisawa/gbfaccelerator/xposed/
        │   └── SkyLeapModule.kt             # 核心模块类 (继承 XposedModule，输出验证日志)
        ├── resources/META-INF/xposed/       # Modern Xposed 标准元数据
        │   ├── java_init.list               # 模块入口类定义
        │   ├── module.prop                  # 模块信息 (minApi: 100, targetApi: 102)
        │   └── scope.list                   # 作用域限定 (com.dena.skyleap)
        └── res/values/
            ├── arrays.xml                   # xposed_scope 数组定义
            └── strings.xml                  # 模块名称与描述文本
```

---

## 二、如何编译

在终端中进入 `android/` 目录执行 Gradle 编译：

```powershell
cd android
.\gradlew.bat assembleDebug
```

编译成功后，终端将输出 `BUILD SUCCESSFUL`。

---

## 三、APK 在哪里

生成的 Debug APK 位于：
* **相对路径**：`android/app/build/outputs/apk/debug/app-debug.apk`
* **绝对路径**：`D:\gbf_proxy\android-Acclerator\android\app\build\outputs\apk\debug\app-debug.apk`
* **文件体积**：约 **860 KB**

---

## 四、手机上如何安装

确保真机已开启「USB 调试」并通过数据线连接到电脑，执行：

```powershell
adb install -r D:\gbf_proxy\android-Acclerator\android\app\build\outputs\apk\debug\app-debug.apk
```

或者将 `app-debug.apk` 发送到手机直接点击安装。

---

## 五、LSPosed 如何启用模块

1. 打开手机上的 **LSPosed Manager** 应用。
2. 进入 **「模块」** 选项卡。
3. 找到并点击刚刚安装的 **`GBF-ACC Xposed`**。
4. 打开顶部 **「启用模块」** 开关。
5. 在模块的推荐作用域列表中，系统会自动勾选 **`SkyLeap` (`com.dena.skyleap`)**；如未自动勾选，请手动勾选官方 SkyLeap。
6. 无需重启手机（LSPosed 支持动态生效），但若 SkyLeap 正在后台运行，请先在手机后台强行停止 SkyLeap（或者执行 `adb shell am force-stop com.dena.skyleap`）。

---

## 六、adb logcat 如何验证

电脑终端执行以下命令监听日志（过滤 `GBF-ACC` 标签）：

```powershell
adb logcat -c ; adb logcat -s GBF-ACC
```

然后在手机上点击图标启动 **SkyLeap**。
终端将清晰打印出以下验证日志：

```text
--------- beginning of main
I GBF-ACC : ==================================================
I GBF-ACC : [GBF-ACC] Xposed Module initialized successfully!
I GBF-ACC : [GBF-ACC] Framework name : LSPosed
I GBF-ACC : [GBF-ACC] Framework ver  : 1.9.2 (7280)
I GBF-ACC : [GBF-ACC] API Version    : 100
I GBF-ACC : [GBF-ACC] Process name   : com.dena.skyleap
I GBF-ACC : ==================================================
I GBF-ACC : ==================================================
I GBF-ACC : [GBF-ACC] SkyLeap package loaded!
I GBF-ACC : [GBF-ACC] Target Package : com.dena.skyleap
I GBF-ACC : [GBF-ACC] Current Process: com.dena.skyleap
I GBF-ACC : [GBF-ACC] ClassLoader    : dalvik.system.PathClassLoader[...]
I GBF-ACC : [GBF-ACC] First Package  : true
I GBF-ACC : [GBF-ACC] Injection verification: SUCCESS (LSPosed -> GBF-ACC -> SkyLeap)
I GBF-ACC : ==================================================
```

看到上述日志即标志着 **第一阶段 PoC 验证完全成功**。

---

## 七、第六阶段：Android 宿主管理 App 与前台生命周期

在第六阶段中，本 APK 同时承担 **Android 宿主管理 App (Host App)** 与 **LSPosed 模块** 双重职责：
1. **宿主生命周期管理**：
   - 通过 `CoreService`（Foreground Service，带常驻前台通知）提高后台存活优先级，并对 Go Core 异常退出提供受控恢复。
   - 通过 `CoreManager` 以子进程方式启动、停止、监控 Go Core 进程。
   - 包含崩溃自动重启保护（1 分钟内最多尝试 3 次，防死循环）。
   - 当应用退出或点击【停止代理】时，正常退出和异常终止后监听端口由系统释放；当前真机测试中未发现重启绑定冲突。
2. **二进制打包与执行机制**：
   - 将 Go Core 作为 APK 原生库资产随 ABI 打包（`libgbfcore.so` 放置于 `jniLibs/arm64-v8a/`），并通过 `nativeLibraryDir` 的实际路径启动独立进程；该方式已在当前 Android 16 ARM64 真机完成验证。
   - 遵循 Android 10+（API 29+）的 `W^X` SELinux 安全策略。
3. **数据隔离与干净卸载**：
   - 数据目录通过 `-base-dir` 定位到 `Context.getFilesDir()`。
   - 运行时配置 `config.json`、证书 `certs/ca.crt`、静态缓存 `files/cache/gbf/https` 均存放在应用私有目录，卸载时被 Android 系统完全清理，零残留。
4. **控制台 UI 与业务观察**：
   - 显示 Go Core 运行状态、PID、运行时长、端口状态。
   - 实时轮询 127.0.0.1:8125 显示 RAM 缓存占用、RAM/磁盘命中数、API 请求数、HTTP/2 连接复用率。
   - 提供快捷启动 SkyLeap 按钮与实时控制台日志滚动查看器。
   - 本次实测的 GBF GET/POST 请求保持正常，业务请求完成，未观察到 Body、Cookie 或关键 Header 丢失。
   - 当前实现不主动修改 GBF 动态 API 业务语义，不包含自动操作、请求重放或业务数据修改逻辑；账号/服务条款风险无法由技术测试证明为零。
   - 当前 `dns_android.go` 作为阶段性 PoC 实现，Android 最终版本仍需评估系统 DNS / ConnectivityManager / DnsResolver 路径，不将当前公共 DNS fallback 视为最终定案。
5. **ProxyController 分流机制与职责划分**：
   - Xposed 模块（`SkyLeapModule`）通过 AndroidX WebKit `ProxyConfig.Builder` 启用 `setReverseBypassEnabled(true)` 反向分流机制。
   - 仅将 GBF 目标流量（`prd-game-a-gbf.akamaized.net`、`gbf.game.mbga.jp`、`*.granbluefantasy.jp`）导向本地 `127.0.0.1:8124`，其余 SkyLeap 流量在 WebView 层直接走 DIRECT 直连，不进入 8124。
   - Go Core 接收到 8124 流量后，依据 Host 与 Path 对静态素材与动态 API 进行内部精准分流：静态资源走本地 RAM/Disk 缓存与 HTTP/2 多路复用，动态 API 走透明转发池（保留原始 Cookie 与 Header，写请求坚决零重试）。

---

## 八、Android 正式产品骨架与模块化分层 (Productization Skeleton)

在正式产品化演进中，本项目由单纯的 Xposed 验证宿主升级为标准的 **GBF-Accelerator Android** 独立产品架构：

```text
com.sagisawa.gbfaccelerator/
├── browser/                          # 通用浏览器适配层
│   ├── BrowserAdapter.kt             # 统一适配器接口规范与生命周期回调
│   ├── BrowserAdapterRegistry.kt     # 线程安全适配器注册表 (支持多浏览器注册与查找)
│   ├── GbfRoutingRules.kt            # 唯一事实来源 (SSOT) GBF 6 项反向代理匹配规则
│   ├── ProxyConfigurator.kt          # 状态机 ProxyController 注入器 (严格 Fail-Closed 保证)
│   ├── WebViewBrowserAdapter.kt      # 基于 Android 标准系统 WebView 的通用抽象基类 (独立 Chromium 需单独实现)
│   └── skyleap/
│       └── SkyLeapAdapter.kt         # 官方 SkyLeap (com.dena.skyleap) 专用适配实现
├── core/                             # 本地 Go Core 生命周期与进程看门狗
│   ├── CoreControlReceiver.kt        # 跨进程广播接收器 (ACTION_START / ACTION_STOP)
│   ├── CoreManager.kt                # 进程监控、状态机、指标轮询、崩溃自动恢复 (Watchdog)
│   └── CoreService.kt                # 前台服务 (Foreground Service) 保持 Core 存活与常驻通知
├── patch/                            # 浏览器打包、导入与注入预留流水线接口
│   ├── BrowserSource.kt              # 浏览器来源建模 (已安装包 InstalledPackage / 本地 APK 文件 LocalApk)
│   ├── PatchEngine.kt                # APK 补丁引擎契约 (进度通知、选项配置、结果模型)
│   ├── StubPatchEngine.kt            # 当前阶段待命引擎实现 (客观报告未就绪，杜绝伪造成功)
│   ├── PatchedBrowserInstaller.kt    # 系统 PackageInstaller 安装触发器
│   └── BrowserLauncher.kt            # 目标浏览器安装状态探测与拉起调度器
├── ui/                               # 原生控制台 UI
│   └── MainActivity.kt               # 状态展示 (Core运行/停止, 8124/8125, 代理正常/不可用), 浏览器选择与拉起
└── xposed/                           # Xposed / LSPosed 胶水层
    └── SkyLeapModule.kt              # libxposed Modern API 102 入口，轻量委托至 BrowserAdapterRegistry
```

### 核心产品定位与未来演进
1. **定位**: `GBF-Accelerator Android` 专注于管理本地 Go Core 守护进程与浏览器加速代理桥接，并非特定浏览器的替代品。
2. **浏览器扩展机制**:
   - `WebViewBrowserAdapter` 仅适用于实际基于 Android 系统 WebView (`android.webkit.WebView`) 的浏览器（如 SkyLeap）。
   - 独立 Chromium 架构浏览器（如 Chrome、Kiwi 等）因自建 Cronet 原生网络栈，不使用系统 WebView，不可直接继承复用 `WebViewBrowserAdapter`，后续应根据其实际网络栈单独实现专属 `BrowserAdapter`。
   - 所有受支持的浏览器均实现 `BrowserAdapter` 统一接口并通过 `BrowserAdapterRegistry` 注册。
   - 首页下拉菜单自动识别已注册的浏览器列表，动态检查安装版本与状态。
3. **打包补丁流水线 (Packaging Pipeline) 架构预留**:
   - 规划链路：`用户导入官方浏览器 APK (BrowserSource)` → `PatchEngine 本地重打包` → `PatchedBrowserInstaller 触发系统安装` → `BrowserLauncher 启动`。
   - 当前阶段预留完整抽象接口与数据模型，支持现有预补丁（Pre-patched）模式，避免过度重度构建。
4. **Fail-Closed 故障闭环保护**:
   - 当 Go Core 关停时，8124 端口释放，WebView 代理规则依然锁定该本地端口，GBF 游戏流量自动 Fail-Closed 绝不直连官方泄漏；非 GBF 流量（如第三方 OAuth）走 DIRECT 始终畅通。
