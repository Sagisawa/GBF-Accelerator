# GBF-Accelerator 核心架构与安全治理规范 (AGENTS.md)

> 本文件是所有参与此项目代码开发、重构、调优的 AI Agent 和人类开发者的**最高约束规范与唯一事实来源 (Canonical Source of Truth)**。
> 任何代码改动在被提议或提交前，必须逐条对照此规范自查。违反 P0 级原则或测试诚信法则的代码属于严重故障，必须立即无条件回滚。

---

## 0. 项目核心定位与叙事原则

1. **核心使命**：本项目是一个**高性能本地静态资源缓存与透明代理工具**。
2. **优化目标**：通过本地 RAM/磁盘缓存、HTTP/2 多路复用和连接复用，优化静态资源加载体验；通过温和的资源调度，**降低突发并发与瞬时请求压力，减少对上游/CDN造成不必要负载**。
3. **叙事与合规红线**：
   - 严禁对外宣称“100% 保证不封号”、“免除官方处罚”或“规避风控检测”。
   - 严禁将代码优化的目标描述为“抹平特征”或“对抗检测”。所有优化的动机必须从**“工程减负、流量削峰填谷、透明稳定”**出发。
4. **语言风格准则（言简意赅、实事求是、严禁夸张）**：
   - 面向用户与社区的文档、更新日志（Changelog）、Release 说明、代码注释及提示信息，必须遵循**言简意赅、字句克制、实事求是、切实实际**的原则。
   - **严禁营销化与绝对化夸张用词**：严禁出现“彻底解决”、“绝对零阻塞”、“100%保证”、“起飞”、“全网最强”、“秒杀”、“完美”等非工程性宣传用语。
   - **坚持以客观事实与实测数据为准**：必须采用严谨、中立的工程术语与量化测试指标（如“将冷启动扫描上限调整为 1500 项，预热耗时由 ~7.5s 降至 ~1.8s”、“将素材扫描从前台请求路径解耦至后台队列，减少主事件循环阻塞风险”、“引入前台请求计数，优先保障前台素材加载”）。

---

## P0 级：不可逾越的绝对安全红线 (Violations are Critical Bugs)

### 1. 业务语义绝对透明（Business Semantic Transparency）
- **范围**：所有非静态资源请求，包括但不限于 `/rest/`、`/quest/`、`/party/`、`/user/`、`/deck/`、`/gacha/`、`/casino/`、`/mypage/` 等。
- **规则**：
  - 必须通过专用的 `api_client`（HTTP/1.1 Keep-Alive 池）进行端到端透明转发。
  - **业务语义零干预**：除 HTTP 代理层依法/协议上必须处理的逐跳头（hop-by-hop，如 `Transfer-Encoding`, `Connection` 等）外，**严禁修改上游业务状态码、实体正文、Cookie 或端到端业务 Header**。
  - **Content-Encoding 限制**：仅允许在代理底层库已经完成对应解压、且客户端若接收原头将无法正确解压的特定管道中，对 `Content-Encoding` 做必要的技术同步剔除；**且必须同时保证实体解压后的业务内容字节语义严格一致**。严禁将 Header 修改扩大化。
  - **严禁本地 Mock**：`MOCK_PATHS` 必须保持为空元组 `()`。严禁针对游戏接口构造本地虚假 200 响应。
  - **严禁缓存动态 API**：动态接口的任何响应严禁写入本地磁盘或内存缓存。

### 2. 官方心跳与错误上报绝对穿透
- `/ob/r`（官方反作弊与心跳探测）和 `/rest/error/js`（前端错误上报）必须作为标准动态 API 100% 穿透至 Cygames 上游服务器。
- 严禁在本地拦截、丢弃或伪造此类探测包。

### 3. 双重约束安全重试机制（Dual-Constraint Safe Retry）
- **POST/PUT/DELETE 零重试原则**：任何涉及游戏状态变更的写请求（攻击、使用技能、召唤、购买体力等），最大尝试次数必须严格为 1（`max_attempts = 1`），**绝对禁止自动重试**，彻底杜绝“技能双发 / 状态不一致”。
- **GET 重试双重限制**：
  1. 仅限预先审核过的只读幂等 GET 接口（`RETRYABLE_API_PATHS` 白名单）；
  2. 仅在建立连接前遇到空闲 TCP 断开或传输层 Stale Connection（`ConnectError`, `RemoteProtocolError` 等）时，允许最多 1 次静默快速重连。

### 4. 响应头零指纹污染（Zero Header Pollution）
- 严禁向客户端返回任何自定义代理标识头，包括但不限于 `X-Proxy-Cache`、`X-Cache-Source`、`X-Acceleration-*`。
- 动态接口必须通过 `forward_upstream_response` 原样还原上游头部，严格多行保留每一条 `Set-Cookie`，禁止逗号折叠合并。

### 5. 静态资源防篡改与缓存完整性（Byte-for-Byte Integrity）
- 本地磁盘和内存缓存的静态资源（`.js`, `.css`, `.png`, 音频等）必须是上游 Akamai CDN 的原始字节流。
- 严禁向缓存脚本中注入第三方作弊、挂机或修改 DOM 的 JS 代码。
- 历史补丁清理（Quarantine）机制必须采用“结构化空函数（void 0 / 空函数体）+ 异常处理上下文”的精准语法特征，**严禁对正常代码中合法的 `void 0` 单独进行粗暴匹配误伤**。

---

## P1 级：资源调度与网络负载控制 (Resource & Scheduling Guardrails)

### 1. Prefetch（预加载）调度层平滑与主动避让
- **后台异步**：Prefetch 必须完全在后台 worker 中执行，严禁侵入前台主请求管线。
- **调度平滑（Pacing）**：
  - Prefetch 必须通过调度器（Scheduler / Pacer）实现任务平滑分发，**严禁在任务循环中硬编码机械性的 `sleep(ms)`**；
  - 当前基线推荐目标为任务间 **15~35ms 随机抖动平滑**，削峰填谷，避免切换副本瞬间打出脉冲并发；
  - 队列为空或低负载时保持零延迟，不拖长正常冷启动加载；
  - **可验证演进**：未来对 Pacing 策略或区间的调整，必须附带基准测试（Benchmark）数据，证明不会在多场景下引发突发流量或加重 CDN 负担。
- **动态避让**：当检测到前台有活动中的动态游戏 API（`ACTIVE_API_COUNT > 0`）时，Prefetch 必须主动挂起暂停，绝不挤占前台战斗网络的带宽与系统资源。

### 2. 连接池保守水线（Connection Pool Conservatism）
- 静态素材客户端 `asset_client` 基于 HTTP/2 多路复用，单条 TCP 连接即可并发数十个 Stream。
- **当前推荐基线**：`asset_max_connections <= 32`，`asset_max_keepalive <= 16`，维持在正常客户端网络的保守范围。
- **可验证演进**：连接池上限并非绝对安全常数，而是性能安全平衡点。任何提升连接数上限的提议，**必须提供充分的基准测试报告**，证明其在主流网络环境下不会造成异常 TCP 握手风暴、连接抢占或前台 API 延迟退化。

---

## P1 级：跨平台隔离与 GUI 防回归 (Cross-Platform & GUI Guardrails)

### 1. 平台隔离原则

> **目标平台增加能力，非目标平台保持既有行为。**

* 平台专用功能必须尽量局部化，不得为了适配一个平台而无条件改变其他平台已有逻辑。
* 系统代理、证书、进程管理、权限提升、系统菜单、平台专用命令等 OS 特有能力，应通过平台分支或独立适配器隔离。
* 核心代理、缓存、调度等网络热路径应保持平台无关，避免散落大量 `if sys.platform`。
* 平台专用第三方库不得在其他平台无条件导入；必要时使用条件导入或局部导入。

### 2. GUI 防回归

* **严禁“一刀切”修改公共 GUI 布局。** 为解决某个平台的小屏、DPI 或输入问题，不得直接给所有平台增加 `Canvas + Scrollbar`、固定窗口尺寸或其他平台专用布局约束。
* 修改 GUI 前必须确认原有平台的几何布局机制，特别是 `winfo_reqheight()`、`winfo_reqwidth()`、`pack`、`grid` 以及窗口最小/最大尺寸。
* 除非明确修改 UI 设计，否则必须保持既有平台的窗口尺寸、控件可见性、布局层级及交互行为。
* 平台专用事件绑定、菜单协议和输入行为必须经过平台隔离，不得默认假设不同操作系统行为完全一致。
* 路径统一使用 `pathlib.Path`，平台专用命令和权限机制必须放在对应平台实现中。

### 3. 跨平台修改审查

每次涉及跨平台代码或 GUI 的修改，AI 必须检查：

1. **污染性**：新增代码是否可能在非目标平台执行？
2. **回归性**：是否改变了非目标平台已有的窗口、布局、启动、文件或系统行为？
3. **覆盖性**：自动化测试是否真正覆盖了受影响的行为？

不得因为“全量自动化测试通过”就直接判定 GUI 没有回归。

### 4. GUI 视觉烟测

涉及以下内容时：

* `gui_*.py`
* 窗口尺寸或布局
* `Canvas` / `Scrollbar`
* 字体、DPI、自适应
* 平台专用菜单或事件

除自动化测试外，必须考虑在实际运行环境中进行 GUI 烟测，至少确认：

* 程序正常启动；
* 主要控件完整可见；
* 无文字或按钮截断；
* 无非预期滚动条；
* 无明显异常空白；
* 非目标平台原有界面行为未被破坏。

### 5. 跨平台改动原则

优先采用：

```text
平台无关核心逻辑
        ↓
平台适配层
   ┌────┴────┐
Windows     macOS
```

而不是在核心业务和公共 GUI 中不断增加平台判断。

**不要为了修复一个平台，而重构所有平台。**

---

## P2 级：反向克制原则（禁止画蛇添足与过度工程）

**严禁添加任何“试图伪装代理特征”的反检测逻辑**：
- ❌ 严禁随机修改 Header 顺序或随机注入假 Header；
- ❌ 严禁随机轮换 User-Agent；
- ❌ 严禁尝试模拟浏览器指纹、伪造 TLS / JA3 特征；
- ❌ 严禁模拟所谓的“人类操作间隔/随机延迟”。

**理念**：
代理的立身之本是“标准的网络传输中间件”。任何试图伪装成“人类或原生浏览器”的反检测代码，在协议层都会产生更畸形、更脆弱的破绽，带来无限的维护噩梦。**克制、干净、少做多余事，是本项目的最高追求。**

---

## 测试诚信法则（Test Integrity Guardrails）

**严禁通过修改、删除、跳过或削弱现有测试断言来使测试通过！**
- 遇到自动化测试失败时，必须深入排查生产代码的实现缺陷，而不是降低测试门槛。
- 严禁把精确匹配断言（如 `assert resp.status_code == 200`）泛化为宽泛断言（如 `assert resp.status_code in (200, 500, 502)`）来掩盖真实故障。
- 除非该测试用例已被技术论证与当前正式设计规范相冲突、且获得项目维护者明确批准，否则任何测试断言的修改均属于严重违规。

---

## AI 研发操作协议 (CHANGE PROTOCOL)

所有 AI Agent 在接到修改需求时，必须严格按照以下五步流水线执行，禁止跳步：

1. **现状确认 (Audit First)**：
   - 修改前必须通过工具阅读并定位真实源码，确认现有实现（如调度逻辑、连接池设置、`ACTIVE_API_COUNT` 行为等）；
   - 严禁基于“我认为项目是这样写的”盲目假设直接动手。
2. **影响面与风险评估 (Risk & Scope Assessment)**：
   - 对照本文档确认该改动涉及 P0、P1 还是 P2 范围；
   - 检查方案是否触碰了“业务语义透明”或“禁止伪装”红线。
3. **最小化代码改动 (Minimal Code Change)**：
   - 仅修改解决问题所必需的最小代码块；
   - 严禁随意格式化无关代码、删除重要注释或重写无关模块。
4. **自动化全量测试 (Automated Test Suite)**：
   - 改动后必须运行并通过全套回归测试：
     ```powershell
     .\.venv\Scripts\python.exe test_proxy.py
     .\.venv\Scripts\python.exe test_update_manager.py
     ```
   - 验证 73 项代理测试与 10 项更新测试全部通过（100% Pass）。
5. **Diff 自检核对 (Self-Review via Diff)**：
   - 运行 `git diff`，逐行审查所有变动行，确认未引入非预期的副作用和违反规范的代码。

---

## Git 提交与版本发布治理规范 (Commit & Release Standards)

为了保证仓库历史记录整洁规范，并与 GitHub Release 展示格式严格区分，必须无条件遵循以下**双轨语言与格式规范**：

### 1. Git Commit 必须 100% 使用英文 (Strict English Conventional Commits)
- **绝对禁令**：Git Commit 的 Subject 和 Body **严禁包含任何中文字符**。
- **命名规范**：遵循标准 Conventional Commits 格式：
  - `feat(...)`: 新增功能
  - `fix(...)`: 缺陷修复
  - `perf(...)`: 性能优化
  - `refactor(...)`: 代码重构
  - `docs(...)`: 文档更新
  - `release: vX.Y.Z - <short English summary>`: 版本发布提交
- **范例对比**：
  - ✅ 正确：`release: v1.7.2 - prioritize foreground assets, decouple prefetch discovery, and optimize startup warmup`
  - ❌ 错误：`release: v1.7.2 - 前台素材优先调度、预加载解耦防卡顿与启动优化`

### 2. GitHub Release 必须统一中文模板 (Chinese Release Presentation)
- **Release 标题**：严格固定为 `vx.x.x - “主要功能/修复”` 中文格式：
  - 范例：`v1.7.2 - 前台素材优先调度、预加载解耦防卡顿与启动优化`
- **Release 说明**：保存在 `docs/releases/vX.Y.Z.md`，使用规范客观的中文 Markdown。
- **发布压缩包**：
  - Windows 客户端：严格命名为 `GBF_Accelerator_vX.Y.Z_GUI.zip`；
  - macOS 客户端：命名为 `GBF_Accelerator_vX.Y.Z_macOS_universal2.zip`（Universal 2 双架构包，或包含 `mac`/`darwin`/`osx` 标识的命名如 `GBF_Accelerator_vX.Y.Z_mac_GUI.zip`，以配合 `update_manager.py` 跨平台匹配与过滤逻辑）。

### 3. 版本发布检查清单 (Release Checklist)
每次发布版本前，必须严格核对以下 5 项，严禁将 Release 标题混淆复制给 Commit：
1. `update_manager.py` 中的 `APP_VERSION = "X.Y.Z"`；
2. `build_exe.py` 中的 `zip_path` 指向 `GBF_Accelerator_vX.Y.Z_GUI.zip`（macOS 独立发布包命名规范为 `GBF_Accelerator_vX.Y.Z_macOS_universal2.zip`，或包含 `mac`/`darwin`/`osx` 关键词以配合 `update_manager.py` 跨平台匹配）；
3. `CHANGELOG.md` 与 `README.md` 包含对应版本的更新说明；
4. **Git Commit 信息必须为纯英文**；
5. **GitHub Release 标题必须为 `vX.Y.Z - 主要功能/修复`（中文）**。
