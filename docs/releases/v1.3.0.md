# GBF Accelerator v1.3.0

## 新增功能

- 支持 Steam 版 GBF 专用 CDN 分流、HTTPS 证书匹配和静态资源缓存。
- 新增 Windows 开机自启选项，启动后自动缩小到系统托盘。
- 自动探测支持岛风 GO 的 HTTP `127.0.0.1:8099`。
- 检测到多个上游代理时弹出列表供用户选择。
- 新增“确认”按钮，可立即断开旧连接并切换到填写的上游代理。
- EXE、窗口标题栏和系统托盘统一使用 GBF 加速器图标。

## 验证

- Python 语法和 GUI 导入检查通过。
- PyInstaller Windows EXE 构建通过。
- PAC、上游代理探测和 Steam CDN 域名规则已验证。
