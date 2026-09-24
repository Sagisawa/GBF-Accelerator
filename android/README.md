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
5. 在模块的推荐作用域列表中，系统会自动勾选 **`SkyLeap` (`com.dena.skyleap`)**；如未自动勾选，请手动搜索并勾选。
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
I GBF-ACC : [GBF-ACC] Framework ver  : 1.9.x (...)
I GBF-ACC : [GBF-ACC] API Version    : 102
I GBF-ACC : [GBF-ACC] Process name   : com.dena.skyleap
I GBF-ACC : ==================================================
I GBF-ACC : [GBF-ACC] Target package loaded: com.dena.skyleap in process: com.dena.skyleap (isFirstPackage=true)
I GBF-ACC : ==================================================
I GBF-ACC : [GBF-ACC] SkyLeap package ready / loaded!
I GBF-ACC : [GBF-ACC] Target Package : com.dena.skyleap
I GBF-ACC : [GBF-ACC] Current Process: com.dena.skyleap
I GBF-ACC : [GBF-ACC] ClassLoader    : dalvik.system.PathClassLoader[...]
I GBF-ACC : [GBF-ACC] Injection verification: SUCCESS (LSPosed -> GBF-ACC -> SkyLeap)
I GBF-ACC : ==================================================
```

看到上述日志即标志着 **第一阶段 PoC 验证完全成功**。
