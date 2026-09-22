# Tarou 可选集成（开发中）

GBF-Accelerator 计划将 Chrome-Extension-Tarou 作为可选浏览器侧集成，而不是将其源码直接并入核心代理。

当前分支只提供集成壳和本地 Bridge 合同：
- 默认关闭，不影响现有用户。
- 不捆绑、不复制 Tarou 源码。
- 8124 数据面不依赖 Tarou；集成仅通过 8125 本地控制面通信。
- 只有 /api/integrations/tarou/* 接口允许 chrome-extension://... Origin。
- protocol_version 当前为 1，30 秒无 heartbeat 即视为断开。
- 正式发布前需要获得原作者对代码复用、适配和再发布范围的明确许可，并保留原项目署名与入口。

## API

GET /api/integrations/tarou/status
POST /api/integrations/tarou/enable
POST /api/integrations/tarou/disable
POST /api/integrations/tarou/heartbeat

heartbeat 示例：

```json
{
  "protocol_version": 1,
  "extension_version": "x.y.z",
  "capabilities": ["page-bridge", "config"]
}
```

返回的 connected 只表示最近 30 秒内收到合法 heartbeat，不代表检测到了浏览器扩展是否安装。

## 维护边界

Tarou 继续作为独立项目维护。Accelerator 只维护 Bridge 协议兼容性、本地控制面接口和 UI 状态展示。
