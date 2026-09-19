# 评估文档 1.3：动态 API 手写 HTTP 响应构造是否回归标准库

> 本文档为「改善计划 1.3」的专项评估，**只分析、不改代码**。
> 结论先行：**建议维持现状（手写构造），不迁移到标准 `http.ResponseWriter`**。理由与验证方案如下。

---

## 1. 现状（What）

代理的入站侧采用**手工连接管理**，而非 `http.Server`：

```
listener.Accept()
  → handleConnection(conn net.Conn)          // proxy.go:298
      for { req := http.ReadRequest(bufio.NewReader(conn)) }
        ├─ CONNECT     → handleConnect       // 建隧道 / MITM
        └─ 其余        → handlePlainHTTP      // 直接写 conn
              ├─ 动态 API → forwardDynamicResponse  // proxy.go:1168 手写响应
              └─ 静态     → sendAssetResponse / sendNotModifiedResponse
```

响应序列化**全部手写** `HTTP/1.1 ...\r\n` 文本，涉及：
- `forwardDynamicResponse`（动态 API 转发，P0 最敏感路径）
- `sendAssetResponse` / `sendNotModifiedResponse`（静态缓存命中）
- `handlePlainHTTP` 内的 `ca.crt` / `proxy.pac` / 移动引导页
- `handleConnect` 的 `200 Connection Established` / `403` / `502`

## 2. 动机（Why change?）

手写协议文本的固有风险：

1. **易漏 `Date` 头**——HTTP/1.1 服务器响应应携带 `Date`，当前手写路径未加。
2. **边界靠人肉维护**——HEAD 不带 body、204/304 不带 `Content-Length`、keep-alive/close 衔接，全靠手写 `if`。
3. **曾出真实 bug**——本次 P0 修复的 `304 OK` 状态行硬编码，正是手写拼接导致的典型错误。
4. **三处重复**——动态/静态/明文路径各写一套拼接，行为易漂移。

## 3. 约束（P0 红线，迁移必须逐条满足）

| 红线 | 手写现状 | 标准库 `http.ResponseWriter` 的行为 | 冲突? |
|---|---|---|---|
| **Set-Cookie 多行不折叠** | 逐条 `Set-Cookie: v` 写多行 | `Header.Add` 也写多行（`Header.Write` 不折叠 Set-Cookie），行为一致 | ⚠️ 需验证 |
| **零指纹头**（不返回 `X-Proxy-*` 等） | 显式跳过 | 标准库不会主动加，但**会自动加 `Date`**（视为服务端正常头，非指纹） | ⚠️ `Date` 是否可接受需确认 |
| **逐跳头剔除** | 显式 `isHopByHop` 过滤 | 标准库不自动剔除，需自己先清理 `resp.Header` | ⚠️ 仍需手工过滤 |
| **Content-Encoding 同步剔除** | 已解压则剔除对应头 | 标准库不管，需自己控制 | ⚠️ 仍需手工 |
| **状态码/正文/端到端业务头零修改** | 透传 `resp.StatusCode` 与 header | `WriteHeader` 透传状态码；但 `http.Server` 可能改写 `Content-Length`/`Transfer-Encoding`（chunked） | 🔴 **关键风险** |
| **HEAD / 204 / 304 无 body** | 手写控制 | 标准库自动处理（更稳） | ✅ 标准库更稳 |

## 4. 关键技术分析

### 4.1 最大障碍：入站侧不是 `http.Server`，而是手工连接循环

要改用标准 `http.ResponseWriter`，**不能只换 `forwardDynamicResponse`**——必须先有一个 `http.ResponseWriter` 实例，而它只在 `http.Server.ServeHTTP` 的处理链里存在。这意味着要重构的是：

- 把 `handleConnection`/`handlePlainHTTP` 的手工 `for { ReadRequest }` 循环，替换为 `http.Server.Serve(listener)` + `Handler`；
- CONNECT 方法在 `http.Server` 下也能收到（`Method == "CONNECT"`），但 `http.Server` 对 CONNECT 的 `RequestURI`/`Host` 处理与手工 `ReadRequest` 略有差异，需重新适配隧道逻辑；
- MITM 拦截后的「内层 HTTPS 请求」目前是直接复用同一套写字节逻辑，迁移后需为内层也套一个 `http.Server` 或 `http.ResponseWriter`。

**这是一次入站架构重构，不是局部替换。** 触碰面远超 `forwardDynamicResponse` 一个函数。

### 4.2 `Content-Length` vs `Transfer-Encoding: chunked` 的字节级风险

P0 要求动态 API **业务语义零修改**。当前手写路径在读取上游完整 body 后，**总是写死 `Content-Length: len(body)`**，从不产生 chunked。

标准库 `http.ResponseWriter` 在以下情况会自动切到 chunked：
- 调用 `Write` 前未设置 `Content-Length` 且 body 非空；
- 流式写（`Flush`）。

虽然可以「先读全 body 再一次性 `WriteHeader + Write`」避免 chunked，但这正是当前手写路径在做的事——**迁移后要么仍手动管理 Content-Length（没省多少事），要么承担 chunked 透传给客户端的字节差异**（客户端若不支持 chunked 会出错）。

### 4.3 真正的缺陷其实很小

盘点下来，手写路径当前**已确认的真实缺陷**只有一个：**缺 `Date` 头**。
而这个缺陷**不需要架构重构就能修**——在 `forwardDynamicResponse`/`sendAssetResponse` 加一行 `Date: <RFC1123 GMT>` 即可（标准库 `http.TimeFormat`）。

其余「边界靠人肉」「重复代码」属于可维护性问题，且本次 P2 已用测试锁定（`proxy_p0_test.go` 覆盖了状态行/HEAD/204/Set-Cookie/指纹过滤），回归风险已被测试兜住。

## 5. 成本 / 收益 / 风险评估

| 维度 | 评估 |
|---|---|
| **改动面** | 大——需重构入站 `http.Server` + CONNECT/隧道/MITM 适配，触及 proxy 全部热路径 |
| **P0 风险** | 高——`Content-Length`/chunked、`Date`、header 顺序/大小写、Set-Cookie 任一回归都属严重故障 |
| **性能风险** | 中——`http.Server` 的默认超时/缓冲与本代理的手工连接管理语义不同，需基准 |
| **收益** | 低-中——主要是「代码更干净」，功能缺陷仅 `Date` 一项 |
| **测试保障** | 现有 P0 测试可复用为回归基线，但需补 chunked/Date/连接复用的对比测试 |

**结论：成本与风险显著高于收益。** 当前手写路径功能正确、有测试兜底、且紧贴 P0 字节级约束；迁移到标准库为了「干净」却要重写最敏感的业务转发层，不符合 AGENTS.md 的 **P2 反向克制原则**（克制、少做多余事）与**最小改动原则**。

## 6. 建议（替代方案，远小于重构）

**不做架构迁移。** 改为两个低风险的小修补，即可消除手写路径唯一确认的功能缺陷：

1. **补 `Date` 头**（P0 兼容的服务端标准头，非指纹）：在 `forwardDynamicResponse` 与 `sendAssetResponse` 的 header 块加 `fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format(http.TimeFormat))`。
2. **（可选）抽公共序列化助手**：把三条路径共用的「状态行 + 头过滤 + Set-Cookie 多行 + Content-Length + Connection」收敛成一个内部函数，消除重复、降低未来维护漂移风险。纯属内部重构，不改对外字节行为，可由现有 P0 测试验证不回归。

> 这两项都不触碰入站架构，改动量小、可用现有测试直接回归，符合最小改动与克制原则。
> 是否实施由维护者决定；本评估文档仅给出分析，未改动任何生产代码。

## 7. 若未来仍要迁移的验证门槛（供参考）

若日后确有必要迁移标准库，必须先满足：
- [ ] 用真实抓包对比迁移前后动态 API 响应**逐字节一致**（含 Set-Cookie 行数、header 大小写、`Content-Length` 非 chunked）；
- [ ] CONNECT/隧道/MITM 内层请求在 `http.Server` 下行为等价；
- [ ] 基准测试证明吞吐/延迟无退化；
- [ ] 现有 `proxy_p0_test.go` 全部通过 + 新增 chunked/Date/连接复用对比用例。

未满足以上全部条件前，不建议启动迁移。
