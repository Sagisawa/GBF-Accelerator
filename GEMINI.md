# GBF-Accelerator 核心架构与安全底线规范 (GEMINI.md)

> 本文件与 `AGENTS.md` 保持完全一致，由 Antigravity / Gemini CLI 在初始化会话时自动无条件装载。

<!-- INCLUDE: AGENTS.md -->

请参见并严格遵守项目根目录下的 [AGENTS.md](AGENTS.md) 的全部规定：

1. **P0 级绝对安全红线**：
   - 动态 API（`/rest/`, `/quest/`, `/party/`, `/user/` 等）100% 原样透传，严禁修改业务响应，零 Mock（`MOCK_PATHS = ()`），严禁缓存动态接口。
   - `/ob/r` 和 `/rest/error/js` 必须 100% 原样穿透上游。
   - POST/PUT/DELETE 请求严禁任何重试（`max_attempts = 1`）。白名单只读 GET 仅限网络层 Stale 断连时 1 次快速重试。
   - 响应头零指纹污染：严禁注入 `X-Proxy-*` 等自定义标识头。
   - 静态资源字节完整性：禁止代码注入，旧补丁隔离检测必须遵循结构化精准指纹，严禁单凭 `void 0` 粗暴误伤。

2. **P1 级工程调度与平滑**：
   - Prefetch 严禁硬编码 `sleep`，必须采用调度器错峰平滑（15~35ms 抖动），前台有 API 活跃时主动避让。
   - 连接池维持保守水线（HTTP/2 多路复用，连接数 <= 32），禁止盲目拉大。

3. **P2 级反向克制原则**：
   - 严禁任何反检测伪装（禁止随机 Header、禁止随机 UA、禁止伪造 TLS/JA3 指纹）。保持标准代理定位。

4. **验证铁律**：改动后必须运行 `.\.venv\Scripts\python.exe test_proxy.py` 确保 54 项测试 100% 通过。
