# GBF-Accelerator 核心架构与安全治理入口 (GEMINI.md)

> **CRITICAL DIRECTIVE FOR GEMINI / ANTIGRAVITY AGENTS**:
> [AGENTS.md](AGENTS.md) is the **canonical source of truth** for all architectural, safety, and operational standards in this repository.
> You MUST read and strictly adhere to [AGENTS.md](AGENTS.md) BEFORE making or proposing any code changes.

### Mandatory Compliance Summary

1. **P0 级红线**：业务语义绝对透明（动态 API 原样转发，零业务 Header/Body 篡改）、官方探测 `/ob/r` 与 `/rest/error/js` 100% 原样穿透、POST/PUT/DELETE 坚决零重试、零自定义代理头（`X-Proxy-*` 严禁）、静态资源字节防篡改。
2. **P1 级工程调度**：Prefetch 调度平滑（避免突发并发，遇到 API 主动避让）、连接池保守水线（HTTP/2 多路复用，推荐 <= 32）。
3. **P2 级反向克制**：严禁任何试图伪装成浏览器或反检测的代码（禁止随机 Header、禁止随机 UA、禁止伪造 TLS/JA3 指纹）。保持标准透明网络代理定位。
4. **测试诚信法则**：严禁通过修改、跳过、弱化现有断言来让测试通过。
5. **研发操作协议**：必须严格遵循 CHANGE PROTOCOL（现状确认 -> 风险评估 -> 最小修改 -> 全量测试 -> Diff 自检）。

**测试验证命令**：
```powershell
.\.venv\Scripts\python.exe test_proxy.py
.\.venv\Scripts\python.exe test_update_manager.py
```
