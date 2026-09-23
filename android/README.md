# GBF-Accelerator Android 客户端开发指南 (第一阶段)

本项目是 GBF-Accelerator 的 Android 客户端第一阶段最小工程。采用原生 Kotlin 开发，基于官方标准 Android Gradle Plugin (AGP) 构建，集成了针对碧蓝幻想（Granblue Fantasy）网页端深度优化的 WebView 游戏运行环境。

---

## 一、当前版本特性清单

- **官方游戏入口**：默认加载碧蓝幻想官方入口 `https://game.granbluefantasy.jp/`。
- **Cookie 与本地存储持久化**：完整启用 IndexedDB、LocalStorage 与多方 Cookie 保持，游戏鉴权与账号状态在重启后持久保留。
- **横屏与旋转适配**：配置无损横竖屏自适应切换，切换屏幕方向时不会重载游戏、不刷新页面、不断连。
- **屏幕常亮保护**：内置 `FLAG_KEEP_SCREEN_ON`，游戏运行期间屏幕常亮，防止挂机或战斗时黑屏。
- **极简悬浮控制栏**：
  - `<` 后退（优先后退游戏内路由）
  - `>` 前进
  - `↻` 刷新
  - `🏠` 重返 GBF 首页
  - `💡` 屏幕常亮快速开关
  - `🔄` 横屏 / 竖屏一键锁定与切换
  - `☰` 工具栏一键收起/展开，避免在激烈战斗时遮挡技能与按键
- **防误触双击退出**：在首页按系统返回键时提示“再按一次退出游戏”，防止战斗中误触返回退出。
- **轻量透明**：第一阶段暂未接入 Go 核心，不包含 VPN、TUN、系统代理与 Root CA 劫持，纯净安全。

---

## 二、Android Studio 怎么打开

> [!IMPORTANT]
> **关键打开路径**：请务必将 **`android`** 这个子目录作为项目根目录在 Android Studio 中打开，不要打开最外层的仓库根目录。

1. 启动 **Android Studio**。
2. 在欢迎界面点击 **Open**（或在菜单栏选择 **File -> Open...**）。
3. 在文件选择对话框中导航并选中：
   ```text
   D:\gbf_proxy\android-Acclerator\android
   ```
4. 点击 **OK** 打开。
5. Android Studio 会自动开始识别并执行 Gradle 同步（首次同步耗时约 10~30 秒，右下角进度条走完即表示就绪）。

---

## 三、第一次需要下载什么

工程已适配当前最新稳定版配置，大部分依赖在你安装 Android Studio 时已具备：

1. **JDK**：
   - Android Studio 已经内置了 JBR（JetBrains Runtime），无需手动下载配置。
   - 如果使用命令行编译，系统已安装 Java 17（`Temurin-17.0.20`），完全满足构建要求。
2. **Android SDK**：
   - 本工程指定 `compileSdk = 35`（Android 15），`minSdk = 26`（Android 8.0+）。
   - 在首次用 Android Studio 打开或首次运行 Gradle 时，如检测到本地缺少特定组件，Gradle 会自动完成下载与授权安装。
   - 也可手动在 Android Studio 中打开：**Settings (设置) -> Languages & Frameworks -> Android SDK**，在 **SDK Platforms** 勾选 **Android 15.0 ("VanillaIceCream") / API 35** 并点击 **Apply** 下载。
3. **Gradle 与依赖库**：
   - 工程自带 **Gradle 9.7.1 Wrapper**，已配置阿里云与 Google 官方 Maven 镜像源，首次构建会自动拉取所需的 AndroidX 支持库，无需额外手动配置。

---

## 四、怎么连接 Android 真机

推荐使用 USB 数据线连接真机进行调试：

1. **开启手机开发者选项**：
   - 打开手机「设置」->「关于手机」。
   - 连续快速点击「版本号」（或 MIUI/HyperOS 版本号、HarmonyOS 版本号）**7 次**，直到屏幕提示“您已处于开发者模式”。
2. **开启 USB 调试**：
   - 返回手机「设置」->「系统与更新」或「更多设置」-> 进入「开发者选项」。
   - 打开 **「USB 调试」** 开关。
   - *(部分品牌手机，如小米/红米，还需开启「USB 安装」和「USB 调试(安全设置)」)*。
3. **连接电脑与授权**：
   - 使用 USB 数据线连接手机与电脑，手机端连接模式选择“仅充电”或“传输文件”。
   - 此时手机屏幕会弹出弹窗：**「允许 USB 调试吗？」**，勾选“始终允许来自此计算机的调试”，点击**确定**。
4. **验证连接状态**：
   - 在电脑终端中执行以下命令（可在 Android Studio 底部的 Terminal 窗口中运行）：
     ```powershell
     adb devices
     ```
   - 若输出类似 `xxxxxxxx device`，即表示真机已成功连接。

---

## 五、怎么运行

1. 确认真机已连接（或已启动 Android 模拟器）。
2. 在 Android Studio 顶部工具栏正上方的设备选择下拉菜单中，选中你的手机设备名称（例如 `Xiaomi 23116PN5BC` 或其他机型）。
3. 选中左侧的运行配置为 **`app`**。
4. 点击右侧的 **绿色三角形运行按钮 (Run 'app')**（或者按键盘快捷键 `Shift + F10`）。
5. Android Studio 会自动编译代码、安装 APK 到手机并直接拉起 GBF 客户端。

---

## 六、怎么生成 APK

### 方式一：命令行一键构建（推荐，极速无需开 IDE）
在 Windows PowerShell 终端中执行：
```powershell
cd D:\gbf_proxy\android-Acclerator\android
.\gradlew.bat assembleDebug
```
看到终端输出 `BUILD SUCCESSFUL` 即表示生成成功。

### 方式二：在 Android Studio 图形界面生成
1. 点击顶部菜单栏：**Build** -> **Build Bundle(s) / APK(s)** -> **Build APK(s)**。
2. 等待底部状态栏提示构建完成。
3. 构建完成后，IDE 右下角会弹出通知气泡：`APK(s) generated successfully for 1 module`，点击其中的 **locate** 链接即可直接在文件资源管理器中打开 APK 所在目录。

---

## 七、APK 在哪里

生成的 Debug APK 位于工程的输出目录下：

- **相对路径**：
  ```text
  android/app/build/outputs/apk/debug/app-debug.apk
  ```
- **绝对路径**：
  ```text
  D:\gbf_proxy\android-Acclerator\android\app\build\outputs\apk\debug\app-debug.apk
  ```
- **文件体积**：约 **5.4 MB**。
- **安装方式**：可直接将 `app-debug.apk` 发送到手机（通过微信、QQ、网盘或 USB 传输）并在手机上点击安装，或通过命令行直接安装：
  ```powershell
  adb install -r D:\gbf_proxy\android-Acclerator\android\app\build\outputs\apk\debug\app-debug.apk
  ```

---

## 八、工程版本与核心配置速查

| 项目 | 版本 / 配置 | 说明 |
| :--- | :--- | :--- |
| **Android Studio** | 2026.1 (Ladybug / Meerkat) | 推荐开发工具 |
| **开发语言** | Kotlin 2.4.10 | 原生支持 |
| **Android Gradle Plugin (AGP)** | 9.3.2 | 原生内建 Kotlin 编译支持 |
| **Gradle** | 9.7.1 | 自带独立 Gradle Wrapper |
| **Java JDK** | JDK 17 (Temurin-17.0.20) | 官方 LTS 编译套件 |
| **Compile SDK** | 35 (Android 15) | 编译目标框架 |
| **Target SDK** | 35 (Android 15) | 目标运行时系统 |
| **Min SDK** | 26 (Android 8.0) | 最低支持机型 |
