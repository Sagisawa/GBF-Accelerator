# GBF Speed Proxy (碧蓝幻想极速本地缓存代理)

专为 GBF 打造的高性能本地缓存与分流代理，利用你电脑上的 **Clash Verge（7897 端口）+ 机场日本节点**，并**直接挂接 `d:\acgpower\cache\gbf` 现有的海量缓存**，实现与 ACGPower 相同（甚至更纯净）的 0ms 闪电载入。

---

## 核心特性

1. **0ms 本地 SSD 命中**：直接读取你电脑上已有的大量立绘、音频、骨骼动画与静态脚本，无网速瓶颈。
2. **新素材边玩边存**：遇到游戏新活动素材时，自动通过 Clash 机场节点下载并存入本地，第二次打开直接秒开。
3. **多人战进房加速**：内存级缓存房间 Socket URI（60 秒缓存），进本抢怪更快。
4. **无用报错拦截**：自动就地 Mock `/rest/error/js`、`/ob/r`、`/user/nickname.woff` 与 CORS `OPTIONS` 预检，消灭多余网络往返。
5. **精准定向分流**：只有 GBF 流量走加速代理，电脑看视频、刷网页完全不受任何干扰。

---

## 30 秒开玩配置步骤

### 第 1 步：安装本地根证书（只需一次）
由于 GBF 全站强制 HTTPS，要解密静态素材并从本地硬盘快速读取，需要信任本地根证书：
* 双击运行本目录下的 `install_ca.bat`；
* 弹出的 Windows 安全警告提示中，点击 **【是 (Y)】** 即可。

### 第 2 步：启动加速代理
* 确保你的 **Clash Verge** 正在运行（默认端口 7897，且选择了可用的日本节点）；
* 双击运行本目录下的 `start_proxy.bat`；
* 窗口提示 `[READY] 代理服务已成功启动！` 即表示就绪。

### 第 3 步：浏览器接入（二选一）

#### 选项 A：使用 SwitchyOmega / ZeroOmega 插件（最推荐，速度最纯净）
1. 在 Chrome / Edge 商店安装 **ZeroOmega** 或 **SwitchyOmega** 插件；
2. 打开插件设置 -> 【导入/导出】 -> 点击【从备份文件恢复】；
3. 选择本目录下的 `SwitchyOmega_GBF.bak`；
4. 在浏览器右上角插件图标中，选择 **【GBF_AutoSwitch】**；
5. 打开 `game.granbluefantasy.jp` 开始极速游玩！在代理控制台可看到绿色的 `[0ms CACHE HIT]`。

#### 选项 B：使用 Windows 系统 PAC 脚本（无需装插件）
1. 打开 Windows 设置 -> 【网络和 Internet】 -> 【代理】；
2. 在“使用安装程序脚本”（PAC）中，开启开关；
3. 填入脚本地址：`file://d:/acgpower/gbf_speed_proxy/proxy.pac`，保存即可。
