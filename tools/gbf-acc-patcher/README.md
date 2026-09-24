# GBF-Accelerator PC Patch Tool (CLI)

`gbf-acc-patcher` 是 GBF-Accelerator 的 PC 端辅助补丁工具，用于将当前项目的 `SkyLeapModule` (Modern libxposed 模块) 安全注入用户自行提供的官方 SkyLeap 浏览器安装包中。

---

## 核心定位与设计原则

1. **零内置版权 APK**：本工具不提供、不下载、不内置任何 DeNA 官方 SkyLeap 安装包，用户需自行通过合法途径（如 Google Play、备份提取）获取。
2. **复用成熟 LSPatch 链路**：不重复开发底层 APK 重打包与 DEX 注入，直接调用经过真机验证的 LSPatch Portable 模式。
3. **输入无损与只读**：严禁修改原始输入文件，所有解包与注入均在独立的临时工作目录沙盒中完成，异常时自动清理。
4. **签名安全透明**：明确提示用户补丁版本采用本地密钥重签名，与官方签名不兼容，手机端安装前必须先备份数据并卸载原版。

---

## 环境要求

1. **Java 21+ 运行环境**：
   * LSPatch 核心使用 Java 21 编译（Classfile 65.0）。
   * 工具会自动检测并优先使用：
     1. Android Studio 内置 JBR (`C:\Program Files\Android\Android Studio\jbr\bin\java.exe`，通常为 OpenJDK 21/25)；
     2. 环境变量 `JAVA_HOME`；
     3. 系统 `PATH` 中的 Java（版本需 >= 21）。
   * 亦可通过 `--java <path>` 手动指定。
2. **LSPatch 核心与模块**：
   * 仓库已在 `build/lspatch/lspatch.jar` 预置经真机验证的 LSPatch 版本。
   * 模块 APK 由 `android/app` 项目构建产出（`android/app/build/outputs/apk/debug/app-debug.apk`）。

---

## 构建与测试

### 1. 运行单元测试
```powershell
cd tools/gbf-acc-patcher
go test -v ./patcher
```

### 2. 构建可执行文件
```powershell
cd tools/gbf-acc-patcher
go build -o gbf-acc-patcher.exe .
```

---

## 使用说明

```text
Usage: gbf-acc-patcher.exe [options] <input.apk | input.apks | directory>

Options:
  -o, --output string   输出目录 (默认: "./output")
  --java string         指定 Java 21+ 路径 (可选，默认自动检测)
  --lspatch string      指定 lspatch.jar 路径 (可选，默认自动检测)
  --module string       指定 SkyLeapModule APK 路径 (可选，默认自动检测)
  -v, --verbose         启用详细日志输出
  --keep-temp           保留临时工作目录 (用于调试排查)
```

### 常见场景

#### 场景 1：处理 Google Play 下载的 Split APK Bundle (`.apks` / `.xapk`)
```powershell
.\gbf-acc-patcher.exe skyleap.apks -o ./output/
```

#### 场景 2：处理已解压的 Split APK 文件夹
```powershell
.\gbf-acc-patcher.exe ./skyleap_splits/ -o ./output/
```

#### 场景 3：处理单个独立 APK
```powershell
.\gbf-acc-patcher.exe skyleap.apk -o ./output/
```

---

## 输出与安装

针对 Split APK，工具会自动输出两类格式：
1. **`output/<name>-splits/`**：包含所有注入并统一重签名后的分卷 APK。
   * 通过 ADB 一键安装：
     ```powershell
     adb install-multiple output/<name>-splits/*.apk
     ```
2. **`output/<name>-patched.apks`**：打包聚合的分卷压缩包。
   * 发送到手机后，使用 **SAI (Split APKs Installer)** 或 **Shizuku** 安装器一键安装。

安装完成后，在手机上打开 **GBF-Accelerator Android** 启动 Go Core，点击【启动 SkyLeap】即可自动享受静态资源本地缓存加速。
