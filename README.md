# GBF Accelerator (碧蓝幻想极速本地缓存与加速器)

专为 GBF（碧蓝幻想）打造的现代化本地缓存加速系统，直接复用电脑上现存的 ACGPower 海量缓存（或自动本地创建），并与 **Clash Verge（7897）/ Clash（7890）/ v2rayN** 深度结合，实现 **0ms 闪电进本与秒级切屏**。

---

## 核心特性

1. **0ms 本地 SSD + 内存强缓存**：
   - 自动挂载读取海量立绘、音效与动画，直接注入 `Cache-Control: immutable`，浏览器常驻内存命中。
2. **全自动免插件开箱即用（支持 Windows 系统 PAC 自动托管）**：
   - 内置 WinINet 系统代理托管引擎，软件启动自动挂载 PAC 分流，退出自动还原，**无需安装任何浏览器插件**，任何 Chromium / Edge 浏览器直接打开即玩！
   - 同时兼容 ZeroOmega / SwitchyOmega 插件分流。
3. **现代化桌面 GUI + 任务栏右下角系统托盘**：
   - 最小化或点击右上角关闭直接缩入 Windows 系统托盘后台无感加速；
   - 动态托盘菜单：一键显示、暂停加速、打开缓存、彻底退出。
4. **游戏级特征阻断与报错免疫**：
   - 彻底修复战斗攻击时频繁弹出的 `新しいバージョンがあります、更新します。` 错误弹窗；
   - 本地阻断并秒回 Mobage、SmartBeat、Datadog 等第三方埋点监控，网络瀑布流零阻塞。
5. **智能探测与无缝兼容**：
   - 自动探测 ACGPower 现有缓存目录（支持 D/C/E/F 盘）；
   - 自动嗅探 Clash Verge / Clash / v2rayN 端口；
   - 确定性内置根证书，首次启动自动提示 Windows 信任。

---

## 极简使用指南（只需 2 步）

1. **启动上游代理**：确保你的 Clash Verge / Clash / v2rayN 正在运行并连上日本节点；
2. **双击 `GBF_Accelerator.exe`**：点击【启动加速】即可！
   - 默认开启 **【自动配置 Windows 系统代理】**，此时直接在 Edge / Chrome 浏览器打开 `game.granbluefantasy.jp`，即刻享受 0ms 极速游玩！

