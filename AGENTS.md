# GBF-Accelerator 核心架构与安全治理规范 (AGENTS.md)

> 本规范为本项目最高约束与唯一事实来源 (Canonical Source of Truth)。
> 改动前必须对照自查; 违背 P0 规则或测试诚信法则属严重故障, 必须无条件立即回滚。

---

## 0. 项目核心定位与叙事原则

1. **定位与架构**:
   - 定位: **高性能本地静态资源缓存与透明代理工具**。
   - 架构: **v2.x Go 原生单静态二进制架构** (零 Python 运行时依赖；`v2.x` 指代 Go 架构代际，具体产品发行版本以 `engine/config/config.go` 的 `AppVersion` 为唯一权威来源)。
   - 核心与支撑 (`engine/`): `proxy` (转发/静态命中/上游路由), `cache` (RAM SLRU + 磁盘持久化 + ResidentPool), `control` (控制面), `telemetry` (指标/日志), `updater` (自更新), `patcher` (Android 环境与 Web 视图兼容补丁), `health` (环境诊断与自愈修复); 适配与辅助层: `cert`, `sysproxy`, `startup`, `desktop`, `firewall` (系统防火墙适配), `process` (前台进程检测), `res` (内嵌模板与资源分发)。
   - 缓存模型规范: RAM 采用 16 分片双段 SLRU (Probationary 试用段 + Protected 保护段)；磁盘缓存采用整文件直接读取与 `.ext` 元数据校验 (`diskMeta`)；RAM Boost 模式使用独立的只读不淘汰常驻池 (`ResidentPool`)。`MaxDiskDirectReadSize` (2 MiB) 仅为磁盘命中项反向加载进入 RAM SLRU 试用段的准入体积上限，不限制磁盘整文件读取或直接网络缓存。
   - 前端: React SPA 内嵌 `engine/ui`。历史 Python 实现已废除, 规范以 Go 源码为准。
2. **目标与协议分流**:
   - 本地缓存 + HTTP/2 多路复用加速静态素材; 温和调度削峰填谷, 降低突发并发与 CDN 负载。
   - HTTP/2 仅限静态 CDN 上游, 禁改协商行为; 动态 API 严格按原协议透明转发, 当前上游基线为 HTTP/1.1 Keep-Alive 池。
3. **合规红线**:
   - 严禁宣称"100%不封号"、"免除官方处罚"或"规避风控检测"; 严禁表述为"抹平特征"或"对抗检测"。
   - 动机严格立足"工程减负、流量削峰填谷、透明稳定"。
4. **文风准则**:
   - 言简意赅、客观严谨; 严禁营销夸张词 ("彻底解决"、"绝对零阻塞"、"100%保证"、"起飞"、"全网最强"、"秒杀"、"完美"); 坚持中立工程术语与量化指标。

---

## P0 级: 不可逾越的绝对安全红线 (Violations are Critical Bugs)

### 1. 业务语义绝对透明 (Business Semantic Transparency)
- **转发范围与协议基线**: 非静态请求 (`/rest/`, `/quest/`, `/party/`, `/user/`, `/deck/`, `/gacha/`, `/casino/`, `/mypage/` 等) 经专用 `api_client` 透明转发。当前动态 API 上游基线为 HTTP/1.1 Keep-Alive；任何未来协议栈调整必须经过专项审计与 benchmark，并且不得改变业务语义、重试规则和透明转发约束。
- **业务零干预**: 除逐跳头 (`Connection`, `Transfer-Encoding` 等), **严禁修改上游状态码、实体正文、Cookie 或业务 Header**。
- **Content-Encoding 限制**: 仅底层解压且客户端无法解码时技术剔除, 保证解压字节语义一致, 严禁扩大篡改。
- **严禁 Mock 与动态缓存**: 严禁在代理管道中对任何非静态 API 构造本地伪造 200 或 Mock 响应；动态响应严禁写入磁盘或 RAM 缓存。

### 2. 官方探测绝对穿透
- `/ob/r` (反作弊心跳) 与 `/rest/error/js` (前端错误上报) 作为标准动态 API 100% 穿透 Cygames; 严禁本地拦截/丢弃/伪造。

### 3. 多层约束安全重试机制 (Multi-Layer Safe Retry & Replay Guardrails)
- **非幂等写请求应用层坚决零重试**: 写请求 (攻击/技能/召唤/体力等) 应用层 `max_attempts = 1`, **绝对禁止自动重试或重放**, 杜绝"技能双发 / 状态不一致"。
- **Transport 隐式重试与可重放性约束**:
  - 非幂等写请求在传输层严禁被 Go `http.Transport` 底层机制自动重试或重放；
  - 维护网络转发代码时，必须严格关注 Go 标准库 `net/http.Transport` 在连接池复用时因 Stale Connection 对可重放请求自动重发的底层机制；
  - 清除 `upReq.GetBody` 或将 `maxAttempts` 设为 1 均不是充分的绝对零重放保证 (例如空 Body、特定协议头或传输层上下文仍可能触发 Transport 重试逻辑)；
  - 规范要求维护者必须持续核查底层实际重试路径与请求可重放行为，严禁仅凭某一字段赋值或单一变量声明就断言系统在所有边界下绝对不可重放。
- **Failover 上游切换隔离**:
  - 上游代理故障转移 (Failover) 严格仅影响后续新发起请求；**绝对禁止将当前在途失败的动态写请求自动切换并重发至备用上游**。
- **应用层显式 GET 重试的双重限制**:
  1. 仅限经预审的只读幂等接口 (通过代码中的只读白名单判定，如 `isRetryableAPI`);
  2. 仅在遇到连接丢失类网络错误 (如当前代码实际识别的 EOF、Connection Reset、Broken Pipe、Connection Refused 等网络级断开错误) 时允许最多 1 次静默快速重连;
  3. 白名单仅对应用层重试生效，不得暗示所有 GET 重试已受白名单完全约束 (Transport 底层在连接级遇到 Stale 断开时仍有独立重连机制)。

### 4. 响应头零指纹污染 (Zero Header Pollution)
- 严禁向客户端返回自定义代理头 (`X-Proxy-Cache`, `X-Cache-Source`, `X-Acceleration-*`)。
- 动态响应头经反向代理管道原样还原, 严格多行保留每条 `Set-Cookie`, 禁逗号折叠合并。

### 5. 静态资源防篡改与多层校验机制 (Integrity & Validation Guardrails)
- **多层校验职责分离**:
  1. **格式识别与兼容**: 基于文件头 Magic Number 识别真实媒体类型，放行上游合法的跨格式命名资源 (如拓展名为 `.png` 但内容为合规 WebP)，拦截上游错误 HTML 落地；但 Magic Number 仅用于格式识别，不能单独证明资源完整或未被篡改；
  2. **损坏检测**: 校验文件非空、压缩流可解压及无错误标记；
  3. **内容完整性与防篡改**: 磁盘与 RAM 缓存素材 (`.js`, `.css`, 图频等) 必须为 Akamai CDN 原始字节流，严禁本地注入作弊/挂机/DOM 脚本；补丁清理 (Quarantine) 限"结构化空函数 (`void 0` / 空体) + 异常上下文"精准特征，严禁误伤合法 `void 0`。

### 6. Android Patcher 安全边界 (Android Patcher Security Guardrails)
- **业务零篡改与合规底线**: Android 补丁机能力仅限用于代理分流与系统证书兼容 (如 Network Security Config 信任用户 CA)，**严禁修改游戏业务逻辑，严禁注入作弊、挂机或自动化 DOM 脚本**。
- **进程超时可控与资源清理**: 外部命令与工具调用 (如 Java, LSPatch, ADB 等) 必须具备超时控制、任务取消及失败时的临时文件与目录清理机制，严禁无超时的无限阻塞调用。
- **不可信输入与路径隔离**: 控制面板接收的外部参数 (如应用包名、上传文件名) 在进入底层文件系统或设备端命令前，必须进行前置合法性校验与路径清洗 (如防止路径穿越与设备端 Shell 异常拼接)。

---

## P0 级: v2.0 架构核心不变量 (Network Plane & Transaction Invariants)

### 1. 双平面拓扑与 AllowLAN 边界
- **8124 Data Plane**:
  - `AllowLAN=false` 绑 `127.0.0.1`; `AllowLAN=true` 绑 `0.0.0.0`。
  - 负责代理流量, 向 LAN 提供 `/ca.crt`, `/proxy.pac` 与移动端引导页。
- **8125 Control Plane**:
  - **永远仅绑 `127.0.0.1` (Strictly Loopback)**。
  - 管理接口 (示例包括但不限于 `/api/config/*`, `/api/cache/*`, `/api/cert/*`, `/api/sysproxy/*`, `/api/startup/*`, `/api/update/*`, `/api/health/*`, `/api/firewall/*`, `/api/android/*` 等管理端点) 仅限 Loopback。
  - 严禁因 `AllowLAN=true` 改绑非 Loopback; 严禁改 Origin 或白名单向 LAN 暴露 8125。
- **AllowLAN 语义**:
  - 仅控制 8124 是否接受非 Loopback 连接; 绝不开放 Control Plane 与后台。
  - `AllowLAN` 本身不得自动修改系统防火墙；Windows 防火墙若需适配，仅允许通过独立、用户明确触发的防火墙操作配置。
  - 防火墙适配必须严格限定 `Private + LocalSubnet + TCP + 当前代理端口 + Inbound + Allow`，不得开放 Public 或 Any。

### 2. Listener 生命周期与热重载
- **重绑顺序**:
  - 同端口变更: 串行 `Candidate -> Close old -> Listen new -> (成功 switch gen / 失败 rollback old)`, 禁未释放重绑同 TCP 地址。
  - 不同端口: `Listen new -> 成功 -> retire old` 缩短中断。
- **Generation 保护 Accept Loop**:
  - 每个 `serveLoop` 绑定唯一 `generation`; `gen != currentGeneration` 退役立即退出, 禁继续 accept。
  - 关闭旧 gen listener 属正常生命周期, 禁记高等级异常。
- **Anti-Spin 退避机制**:
  - 当前 gen 在 `Accept()` 遇临时错误受控退避 (默认 ~20ms), 禁无延时 busy spin; 修改策略须有测试/Benchmark 依据。
- **保护 Established 连接**:
  - `net.Listener.Close()` 仅影响未来 `Accept()`; 已建立 `net.Conn` (Established 连接) 严禁因 reload 或 gen 切换中断, 须由原 handler 跑完生命周期。
  - 新增热重载必须包含 existing-connection 回归测试。
- **Reload API 恢复性**:
  - 覆盖新绑失败、旧回滚失败、并发 Stop/Reload; 新绑失败旧 listener 尽可能恢复, 禁新旧双活。
  - 回滚失败记明确 high-level 错误, 禁静默返回 success。

### 3. 配置事务模型 (Configuration Transaction)
- **Candidate 数据隔离**:
  - 遵循 `Current Config -> Candidate copy -> Patch Candidate`。
  - Candidate 为纯内存快照, 禁直接修改生效配置、Listener、系统代理、自启动或缓存状态。
- **网络配置强事务顺序**:
  - 涉及 `allow_lan`, `listen_port`, `control_port` 严格遵循:
    `Candidate -> Config Commit / Save -> Network Rebind -> Post-Commit Runtime Sync`。
  - 先持久化配置再修改 listener；任一 listener 重绑失败必须回滚配置与已重绑 listener；严禁磁盘与运行时分裂 (`config.json = new, runtime = old`)。
- **Commit() 与 Update() 语义分离**:
  - `Commit(candidate)` 为强事务主路径 API; `Update(fn)` 仅历史兼容, 禁在网络事务主路径使用。
- **commitMu 锁作用域与禁止回调重入**:
  - 保证并发 Commit 串行, 禁交错 Save 与回滚覆盖。
  - **关键约束**: 严禁持有 `commitMu` 时执行可能重入配置系统的外部回调 (callback)。
  - 标准模式: `lock -> update memory -> save -> snapshot callbacks -> unlock -> invoke callbacks` (Callback 默认不得持有内部锁)。
- **Post-Commit 运行时同步**:
  - 系统代理、自启动、缓存配置 (`base_dir` / `ram_cache_limit_mb`) 与注册表属 Post-Commit best-effort sync。
  - 提交成功后执行, 失败不破坏已成网络事务亦不伪装成功; 日志明确区分 Commit success 与 Runtime Sync failure。

---

## P1 级: 资源调度与网络负载控制 (Resource & Scheduling Guardrails)

### 1. Prefetch 调度层平滑与主动避让
- **后台异步与调度平滑**:
  - Prefetch 完全在后台 worker 异步执行, 严禁侵入前台主请求管线。
  - 调度器 (Scheduler / Pacer) 分发任务, 基线采用 **15~35ms 随机抖动平滑**削峰填谷, 严禁循环硬编码机械 `sleep(ms)`。
  - 低负载或空队列零延迟; 调整策略须附 Benchmark 证明不加重 CDN 负担。
- **动态避让**:
  - 当存在活动中的前台动态 API 或前台静态资源请求时，Prefetch 主动暂停，避免与前台请求竞争 CPU、磁盘 I/O 和上游连接资源。

### 2. 连接池保守水线 (Connection Pool Conservatism)
- 静态客户端 `asset_client` 基于 HTTP/2 多路复用, 连接池基线保持 `asset_max_connections <= 32`, `asset_max_keepalive <= 16` 保守水线。
- 调高上限须提供充分基准测试报告, 证明不造成 TCP 握手风暴、连接抢占或前台 API 延迟退化。

---

## P1 级: 跨平台隔离与 GUI 防回归 (Cross-Platform & GUI Guardrails)

### 1. 平台隔离原则
- **核心原则**: "目标平台增加能力, 非目标平台保持既有行为"。
- 架构遵循 `平台无关核心 -> 平台适配层 (Windows / macOS / Linux)`, 严禁为修复单一平台重构所有平台。
- 系统代理、证书、进程、权限、托盘等 OS 能力由适配器隔离; 核心网络热路径保持平台无关; 平台专有库条件导入。

### 2. GUI 防回归与视觉烟测
- **禁止全局一刀切**: 严禁向公共布局注入全局滚动容器、强制固定尺寸等过度约束。
- 保持现有平台 GUI 的窗口尺寸、最小/最大尺寸、控件布局、滚动行为与交互语义。
- **跨平台审查三要素**:
  1. **污染性**: 新增代码是否在非目标平台执行;
  2. **回归性**: 是否改变非目标平台已有窗口/系统行为;
  3. **覆盖性**: 自动化测试是否真正覆盖受影响行为。
- **真实视觉烟测**:
  - 涉及 `engine/desktop/*` (托盘/独立窗口/控制台隐藏/浏览器发现) 或 `web/` 前端布局与滚动容器等 GUI 修改时, 须在真实环境验证:
    程序正常启动、控件完整可见、无文字或按钮截断、无非预期滚动条或异常空白、非目标平台未受影响。

---

## P1 级: 迁移、测试与发布真实性规范 (Verification & Release Standards)

### 1. Control Port 迁移与平滑切换
- 修改 `control_port` 仅限 Loopback 迁移 (`127.0.0.1:old -> 127.0.0.1:new`), 禁改 host。
- 优先绑新端口, 旧 listener 停收新连接; 旧 HTTP Server 执行 graceful shutdown 留出在途时间, 严禁直接 `Server.Close()` 强杀请求。
- 重载成功返回新 `control_url`; 前端断开旧 SSE/轮询并导航至新 URL, 禁持续请求失效旧端口。
- 测试须覆盖端口可用、冲突、在途平滑完成、前端切换、回滚及并发 Stop/Reload 场景。

### 2. 网络边界测试规范
- Listener、LAN 或 Control Plane 修改须严格验证:
  1. 本机 Data Plane 访问;
  2. 本机 Control Plane 访问;
  3. 真实非 Loopback 物理网卡访问;
  4. `AllowLAN=false` 拒绝 LAN 连接;
  5. `AllowLAN=true` 接受 LAN 连接;
  6. 8125 端口始终保持 Loopback-only。
- **报告真实性**: 严格区分 `Local LAN Interface Socket E2E` (本机物理网卡调用) 与 `Cross-Device Wi-Fi E2E` (跨物理设备), 禁混淆。

### 3. 竞态测试规范 (Race Verification)
- 任何修改 `listener`, `listenerGen`, `serveLoop`, `ReloadListener`, `Stop`, `commitMu`, `Candidate`, `Commit`, rollback 状态的提交, 必须运行:
  `go test -race -count=1 ./...`。
- 若当前环境无法运行 `-race` (如 Windows 缺少 CGO/gcc), 报告必须注明 **`Race detector 未执行`**, 严禁虚报通过。

### 4. macOS 构建真实性与历史演进
- **.app 发布结构**: 遵循 `GBF_Accelerator.app/Contents/{Info.plist, MacOS/GBF_Accelerator, Resources/gbf_accelerator.icns}`。
- **Universal 2 验证**:
  - 严格区分单架构交叉编译 (`darwin/amd64`, `darwin/arm64`) 与 `lipo` fat binary 合成;
  - 须在真实 Mac/runner 实机启动验证方可声明 `macOS runtime smoke-tested`; 未测 Gatekeeper/签名/公证须标注 `Not verified in current environment`。
- **发布证据三态分级**:
  - 审查报告严格区分: `Implemented` (源码已实现)、`Automated Verified` (测试通过)、`Environment Verified` (实机验证); 严禁偷换概念。
- **演进与文档分离**:
  - Go 替代 Python 属架构替换, 无需复刻旧技术栈;
  - 当前 README / CHANGELOG / docs 描述真实 Go 实现;
  - 历史 `docs/releases/vX.Y.Z.md` 原样保留当时记录, 严禁篡改历史 Release Note。
- **分支隔离规范**:
  - 重大开发在明确 feature/release 分支进行; 要求不触碰 main 时严禁操作 main;
  - 提交前后严格检查分支、状态与 diff; 禁 force push 改写共享分支历史。

---

## P2 级: 反向克制原则 (Anti-Overengineering)

**严禁添加任何试图伪装代理特征的反检测逻辑或过度工程**:
- ❌ 严禁随机打乱 Header 顺序或随机注入假 Header;
- ❌ 严禁随机轮换 User-Agent;
- ❌ 严禁尝试模拟浏览器指纹、伪造 TLS / JA3 特征;
- ❌ 严禁在协议层模拟"人类操作间隔/随机延迟"。
**理念**: 代理定位是标准传输中间件, 伪装代码在协议层破绽更大。**克制、干净、少做多余事是本项目最高追求。**

---

## 测试诚信法则 (Test Integrity Guardrails)

**严禁通过修改、删除、跳过或削弱现有测试断言来使测试通过!**
- 自动化测试失败必须定位并修复生产代码缺陷, 严禁降低测试门槛;
- 严禁将精确匹配断言 (如 `assert resp.status_code == 200`) 泛化为宽泛断言 (如 `in (200, 500, 502)`) 掩盖真实故障;
- 除非经技术论证测试与现行正式规范冲突且获维护者明确批准, 严禁修改任何测试断言。

---

## AI 研发操作协议 (CHANGE PROTOCOL)

所有修改必须严格按五步流水线执行, 禁止跳步:

1. **现状确认 (Audit First)**: 修改前定位并通读真实源码确认现有实现, 严禁盲目假设。
2. **风险评估 (Risk Assessment)**: 对照本规范核查 P0/P1/P2 范围及业务透明、反伪装红线。
3. **最小改动 (Minimal Code Change)**: 仅修改解决问题必需的最小代码块, 禁随意格式化无关代码、删除重要注释或重写无关模块。
4. **自动化全量测试 (Automated Test Suite)**:
   - 必须在 `engine/` 目录运行并通过全量 Go 测试 + 静态检查 (100% Pass, 零告警):
     ```powershell
     cd engine
     go test -count=1 ./...
     go vet ./...
     ```
   - 涉及并发、Listener、配置事务时: `go test -race -count=1 ./...`;
   - 涉及 Web 控制台时: 必须依次完成 `(cd web && npm test)` 前端单测、前端构建并将产物同步至 `engine/ui/dist` 再编译 Go 程序；完整发行构建与 Universal 2 合成仅在有发布需求时执行，日常开发无需执行发行包制作流程；
   - 涉及跨平台逻辑时: 验证 `windows/amd64`, `linux/amd64`, `darwin/amd64`, `darwin/arm64` 编译;
   - **报告真实性**: 环境受限无法执行项 (如 Windows 缺少 CGO 运行 `-race`) 须注明 `Race detector 未执行`, 严禁虚报; 旧 Python 测试已移除, 严禁引用。
5. **Diff 自检核对 (Self-Review via Diff)**: 运行 `git diff` 逐行审查所有变动行, 确认未引入非预期副作用与违规代码。

---

## Git 提交与版本发布治理规范 (Commit & Release Standards)

严格遵循双轨语言与格式规范:

### 1. Git Commit 必须 100% 使用英文 (Strict English Conventional Commits)
- **绝对禁令**: Git Commit 的 Subject 和 Body **严禁包含任何中文字符**。
- **命名规范**: 遵循 Conventional Commits (`feat(...)`, `fix(...)`, `perf(...)`, `refactor(...)`, `docs(...)`, `release: vX.Y.Z - <short English summary>`):
  - ✅ 正确: `feat(control): harden transaction rollback and limit commit lock scope`
  - ❌ 错误: `feat(control): 加强事务回滚并限制提交锁作用域`

### 2. GitHub Release 必须统一中文模板 (Chinese Release Presentation)
- **Release 标题**: 严格固定为 `vx.x.x - “主要功能/修复”` 中文格式 (例: `v2.0.0 - Go原生重构、双平面拓扑与Web控制台`)。
- **Release 说明**: 保存在 `docs/releases/vX.Y.Z.md`, 使用客观严谨中文 Markdown。
- **发布包命名**: Windows 为 `GBF_Accelerator_vX.Y.Z_GUI.zip`; macOS 为 `GBF_Accelerator_vX.Y.Z_macOS_universal2.zip` (须与 `updater.go` 资产匹配一致)。

### 3. 版本发布检查清单 (Release Checklist)
发布前逐项核对以下 5 项, 严禁将 Release 标题混淆复制给 Commit:
1. `engine/config/config.go` 中的 `AppVersion = "X.Y.Z"` (唯一权威版本号来源);
2. 构建脚本产出的 zip 命名符合 Windows/macOS 规范, 并与 `updater.go` 资产匹配规则一致;
3. `CHANGELOG.md` 与 `README.md` 包含对应版本的更新说明;
4. **Git Commit 信息必须为 100% 纯英文**;
5. **GitHub Release 标题必须为中文 `vX.Y.Z - 主要功能/修复`**。
