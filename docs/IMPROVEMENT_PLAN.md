# GBF-Accelerator 改善与补全实施计划

> 本文档基于对 Go 原生重写版（v1.8.0，`feat/v2.0-master-plan` 分支）的全量代码审查输出。
> 审查结论：`go test ./...` 与 `go vet ./...` 全量通过，无遗留 TODO，P0 安全红线（动态 API 透明转发、零重试、Set-Cookie 多行保留、无本地 Mock、Quarantine 精准语法）均已落实。
> 本计划按「正确性 → 可观测性 → 一致性 → 测试 → 代码质量」分层，供按优先级逐步推进。

---

## 一、正确性问题（建议优先修复）

### 1.1 `sendAssetResponse` HTTP 状态行硬编码错误 ✅ 已完成
- **位置**：`engine/proxy/proxy.go` `sendAssetResponse()`
- **现象**：`fmt.Fprintf(&buf, "HTTP/1.1 %d OK\r\n", status)` 中 reason phrase 写死为 `OK`。当 `status` 为 304/206 等非 200 时，返回 `304 OK` 这类语义不一致的状态行。
- **修复**：改用 `http.StatusText(status)`，并对空串兜底。
- **验证**：`proxy_p0_test.go:TestSendAssetResponse_StatusPhrase` 断言 304 状态行为 `304 Not Modified`。

### 1.2 `updater.IsNewerVersion` 丢弃预发布后缀 ✅ 已完成
- **位置**：`engine/updater/updater.go` `ParseVersion()` / `IsNewerVersion()`
- **现象**：`1.4.1-rc1` 被解析为 `[1,4,1]`，丢弃 `-rc1` 后缀；`IsNewerVersion("1.8.0","1.8.0-rc1")` 错误返回 false（正式版应大于 RC），且 RC 之间无法比较。
- **修复**：`ParseVersion` 剥离 `-`/`+` 后的预发布/构建元数据；新增 `preReleaseTag` + `comparePreRelease`（semver 规则：正式版 > 任意预发布，数字标识符数值比较、数字 < 字母）。
- **验证**：`updater_test.go` 新增 6 例（正式>RC、RC2>RC1、beta、点分标识符等）。

### 1.3 HTTP 响应标准 Date 头补齐与公共序列化助手抽取 ✅ 已完成
- **位置**：`engine/proxy/proxy.go`（`writeHTTPResponse` / `sendAssetResponse` / `sendNotModifiedResponse` / `forwardDynamicResponse` / `handlePlainHTTP` / 错误响应）
- **现象**：直接散落 `fmt.Fprintf` 写原始 HTTP/1.1 响应，缺少 RFC 7231 / RFC 9110 规范要求的 `Date` 头，且动态、静态、明文三条路径重复手写状态行与 Header 拼接。
- **修复**：
  1. 抽取包级公共序列化助手 `writeHTTPResponse`，统一接管状态行、Date 补齐、逐跳与指纹头过滤、多行 Set-Cookie 保留、RFC 7230 Content-Length 计算（1xx/204/304 绝不输出）及 Connection 头写入；
  2. 补齐 RFC 1123 GMT 标准 `Date` 响应头：上游已有则严格透传保留（P0 业务头零干预），上游缺失或本地生成时自动补充；
  3. 保留 HTTP 规范特有的大小写格式（`ETag` / `WWW-Authenticate`）；
  4. 动态 API、静态缓存、304 响应、明文直连服务及网关错误全部接入统一助手，消除重复代码与行为漂移。
- **验证**：`proxy_p0_test.go` 新增 4 项单测（Date 自动生成、上游 Date 透传保留、静态 200 响应 Date、304 响应 Date），既有全部 P0 测试与全量测试套件 100% 通过。

---

## 二、可观测性（遥测数据真实性）✅ 已完成

> 现状（修复前）：`control.go:getTelemetrySummary()` 返回大量**占位/伪造数据**：`reused = totalAPIs`、`newConns = 1`、`reuseRate = 100.0`、`percentiles` 全为 `0.0`、`protocols` 简单按 API/资产归类。
> 修复后：全部为真实观测值。

### 2.1 真实延迟采样 ✅
- **改动**：`telemetry.Stats` 新增定容 512 的延迟环形缓冲（`RecordLatency` / `LatencySnapshot`）；`proxy` 在动态 API（HTTPS 与明文两条路径）与静态回源处记录真实耗时。
- **产出**：`percentiles`（p50/p95/p99/avg/min/max + samples）返回真实值。
- **验证**：`telemetry_test.go`（空/分位/环形上限/并发安全 4 例）。

### 2.2 真实连接复用统计 ✅
- **改动**：`proxy.tracedRequest()` 通过 `httptrace.ClientTrace.GotConn` 注入非侵入钩子，`RecordConnReuse(reused)` 累计真实新建/复用；协议从 `resp.Proto` 经 `RecordProtocol` 统计。不改任何请求/响应字节。
- **产出**：`reused_connections` / `new_connections` / `reuse_rate_percent` 反映真实连接池行为。

### 2.3 协议分布统计 ✅
- **改动**：`protocols` 返回按 `resp.Proto` 真实计数的 `HTTP/1.1` / `HTTP/2`。
- **验证**：`control_test.go:TestGetTelemetrySummaryRealData` 验证 3 复用+1 新建=75%、H2=3/H1=1、延迟分位正确。

> 说明：控制面返回键名由 `reuse_rate` 修正为 `reuse_rate_percent`，与前端 `types.ts`/`TelemetryStrip.tsx` 实际读取的字段对齐（此前前端恒为 `undefined`，回退显示 100）。

---

## 三、构建与发布一致性

### 3.1 统一 macOS 发布包命名（分叉缺陷）✅ 已完成
- **现象**：`build.ps1` 产出 `..._macOS_arm64.zip` / `..._macOS_amd64.zip`，`build.sh` 产出 `..._macOS_universal2.zip`；而 `updater.go` 与 AGENTS.md 约定 `macOS_universal2`。
- **修复**：`build.ps1` 重构为 `Build-DarwinArch`（编译）+ `Build-Darwin`（统一打包），恒定产出 `GBF_Accelerator_vX.Y.Z_macOS_universal2.zip`；`lipo` 可用则合并真 Universal 2，否则回退 arm64 保持命名。与 `updater.go` 对 `universal` 的优先匹配逻辑一致。

### 3.2 版本号三处同步 ✅ 已完成
- **现象**：`config.go AppVersion = 1.8.0`、`build.ps1`/`build.sh` 兜底默认 `1.8.0`、`web/package.json version = 1.9.0` 三者不同步。
- **修复**：`build.ps1`/`build.sh` 提取失败即报错退出（去掉静默兜底）。`web/package.json` 为前端内部版本、不嵌入产物、不参与更新匹配，按最小改动原则保持原样。

### 3.3 清理残留证书安装脚本 ⏭️ 经审计后保留（非残留）
- **审计结论**：`install_ca.bat` / `install_ca.sh` **仍被多处引用且为文档明确推荐的手动路径**——`build.ps1` 的 macOS AuxFiles、`README.md`、`docs/MAC_LINUX_NOGUI.md`、前端 `web/.../CaCertModal.tsx`。Go 内嵌安装（`cert/install_*`）虽存在，但这两个脚本服务于 nogui/手动信任场景。
- **动作**：按 P2 克制原则**不删除**，保持现状。

---

## 四、测试补全（防 P0 回归）✅ 核心项已完成

- [x] `proxy` 包针对 P0 红线的针对性单测（新增 `engine/proxy/proxy_p0_test.go`）：
  - `forwardDynamicResponse` 的 `Set-Cookie` 多行不折叠、`X-Proxy-*`/`X-Cache-*`/`X-Acceleration-*` 零指纹过滤、上游自定义头保留、状态行/Content-Length/HEAD 无体、204/304 不带 Content-Length；
  - 写/状态端点（`start.json`/`ability_result`/`gacha`/`/ob/r` 等）一律不可重试（零重试红线）；
  - 官方探测 `/ob/r`、`/rest/error/js` 必须分类为动态而非静态可缓存。
- [x] `telemetry` 并发采样与计数（`telemetry_test.go`：空/分位/环形上限/8 goroutine 并发安全）。
- [x] `control.getTelemetrySummary` 真实值校验（`control_test.go:TestGetTelemetrySummaryRealData`）。
- [ ] （可选后续）`cache.SingleFlight` 并发回源专项压测、完整链路「MISS → 回源 → 落盘 → 二次 HIT」集成测试。

---

## 五、代码质量（P3 处理结果）

| 位置 | 项 | 状态 |
|---|---|---|
| `prefetch.go` | Prefetch 写死桌面 Chrome UA | ✅ 已改为包级常量 `prefetchUserAgent`，去掉硬编码版本号（`Chrome/130.0.0.0` → 版本无关 `Chrome Safari`）。稳定、非轮换，符合 P2 克制原则。 |
| `telemetry.go` | `logWorker` 无退出机制 | ✅ 已加 `Close()`（幂等 `closeOnce` + 关闭 logChan 使 worker 退出）；`Log()` 加 recover 防护避免关闭竞态 panic。 |
| `proxy.go` | client 热更新未关旧连接 | ✅ 经复核：`updateClients` 已对旧 client 调 `CloseIdleConnections()`（此前审查误判），无需改动。 |
| `manager.go:Warmup` | 启动预热同步 Walk | ⏭️ 保留：已在后台 goroutine 执行，不阻塞前台请求路径；缓存量级下阻塞有限，暂不引入分页/限速（避免过度工程）。 |

---

## 六、文档治理（本次已完成）

- [x] AGENTS.md：移除 `update_manager.py`/`build_exe.py`/Python 测试命令等过期引用，更新为 Go 真实文件名与 `go test ./... && go vet ./...`。
- [x] AGENTS.md：补充 v2.0 技术栈现状说明、GUI 烟测对象改为 `engine/desktop/*` 与 `web/`。
- [x] GEMINI.md / `.cursorrules`：测试命令同步更新为 Go 命令。

---

## 建议推进顺序

| 阶段 | 内容 | 状态 |
|---|---|---|
| P0 | 3.1 统一 macOS 包命名 + 3.2 版本号同步 + 1.1 状态行修复 | ✅ 已完成 |
| P1 | 2.1/2.2/2.3 遥测真实性重构 | ✅ 已完成 |
| P2 | 4. 测试补全（P0 红线防回归 + 遥测/控制面真实性） | ✅ 核心项已完成 |
| P3 | 1.2 预发布版本比较 ✅；3.3 残留脚本经审计保留（非残留）⏭️；五、代码质量项（UA 常量 / logWorker 退出 ✅，client 关旧连接经复核已就绪 ⏭️，Warmup 保留 ⏭️） | ✅ 已完成 |
| P3 | 1.3 HTTP 响应 Date 头补齐与公共序列化助手抽取 | ✅ 已完成（不重构入站架构，小步低风险落地） |

> 每一项落地前均须遵循 AGENTS.md 的 CHANGE PROTOCOL：现状确认 → 风险评估 → 最小改动 → `go test ./... && go vet ./...` → `git diff` 自检。
