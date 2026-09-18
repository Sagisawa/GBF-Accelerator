import React from 'react'
import { RuntimeStatus, TelemetrySummary, PrefetchStatus } from '../types'
import { 
  Activity, 
  Database, 
  Layers, 
  Wifi, 
  ShieldCheck, 
  Clock, 
  PauseCircle, 
  PlayCircle,
  AlertTriangle
} from 'lucide-react'
import {
  ResponsiveContainer,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  Cell,
  PieChart,
  Pie,
} from 'recharts'

interface DashboardProps {
  status: RuntimeStatus | null
  telemetry: TelemetrySummary | null
  prefetch: PrefetchStatus | null
  onToggleProxy: (start: boolean) => void
  loading: boolean
}

export const Dashboard: React.FC<DashboardProps> = ({
  status,
  telemetry,
  prefetch,
  onToggleProxy,
  loading,
}) => {
  if (!status) {
    return (
      <div className="flex items-center justify-center h-64 text-slate-400">
        正在连接控制服务 (127.0.0.1:8125)...
      </div>
    )
  }

  const p50 = telemetry?.percentiles?.p50_ms ?? 0
  const p95 = telemetry?.percentiles?.p95_ms ?? 0
  const p99 = telemetry?.percentiles?.p99_ms ?? 0

  const latencyData = [
    { name: 'P50 (常规中位数)', value: p50, color: '#38bdf8' },
    { name: 'P95 (尖峰95%)', value: p95, color: '#fbbf24' },
    { name: 'P99 (极端99%)', value: p99, color: '#f87171' },
  ]

  const totalHits = status.requests.total_hits || 0
  const misses = status.requests.cache_misses || 0
  const totalLookups = totalHits + misses
  const hitRatio = totalLookups > 0 ? ((totalHits / totalLookups) * 100).toFixed(1) : '0.0'

  const cachePieData = [
    { name: 'RAM 内存命中', value: status.requests.ram_hits || 0, color: '#22c55e' },
    { name: 'SSD 磁盘命中', value: status.requests.disk_hits || 0, color: '#38bdf8' },
    { name: '未命中 (上游拉取)', value: misses, color: '#64748b' },
  ].filter(d => d.value > 0)

  const isYielding = (status.active_api_count > 0) || (prefetch?.is_yielding ?? false)

  return (
    <div className="space-y-6">
      {/* 1. Header Status Card */}
      <div className="bg-[#131c2e] border border-slate-800 rounded-2xl p-6 shadow-xl relative overflow-hidden">
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div className="flex items-center gap-4">
            <div className={`w-4 h-4 rounded-full ${status.proxy_running ? 'bg-emerald-400 shadow-[0_0_12px_rgba(52,211,153,0.8)]' : 'bg-rose-500'}`} />
            <div>
              <div className="flex items-center gap-3">
                <h2 className="text-xl font-bold text-white tracking-wide">
                  GBF 加速代理核心
                </h2>
                <span className={`text-xs px-2.5 py-0.5 rounded-full font-medium ${status.proxy_running ? 'bg-emerald-950/80 text-emerald-400 border border-emerald-800' : 'bg-rose-950 text-rose-400 border border-rose-800'}`}>
                  {status.proxy_running ? '运行中 (Running)' : '已停止 (Stopped)'}
                </span>
                <span className="text-xs bg-slate-800 text-slate-400 px-2 py-0.5 rounded-md font-mono">
                  v{status.version}
                </span>
              </div>
              <p className="text-xs text-slate-400 mt-1 flex items-center gap-4">
                <span>本地监听: <strong className="text-slate-200 font-mono">http://{status.listen_host}:{status.listen_port}</strong></span>
                <span>上游: <strong className="text-slate-200 font-mono">{status.direct_mode ? '直连模式 (Direct)' : status.upstream_proxy}</strong></span>
                <span>运行时长: <strong className="text-slate-200 font-mono">{Math.floor(status.uptime_seconds / 60)} 分钟</strong></span>
              </p>
            </div>
          </div>

          <div className="flex items-center gap-3">
            <button
              onClick={() => onToggleProxy(!status.proxy_running)}
              disabled={loading}
              className={`flex items-center gap-2 px-5 py-2.5 rounded-xl font-medium text-sm transition shadow-lg ${
                status.proxy_running
                  ? 'bg-rose-600/90 hover:bg-rose-500 text-white shadow-rose-900/30'
                  : 'bg-emerald-600/90 hover:bg-emerald-500 text-white shadow-emerald-900/30'
              }`}
            >
              {status.proxy_running ? (
                <>
                  <PauseCircle className="w-4 h-4" /> 停止代理
                </>
              ) : (
                <>
                  <PlayCircle className="w-4 h-4" /> 启动代理
                </>
              )}
            </button>
          </div>
        </div>

        {status.last_error && (
          <div className="mt-4 p-3 bg-rose-950/50 border border-rose-800 rounded-xl text-xs text-rose-300 flex items-center gap-2">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            <span>异常提示: {status.last_error}</span>
          </div>
        )}
      </div>

      {/* 2. Top Metrics Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        {/* P50 Latency */}
        <div className="bg-[#131c2e] border border-slate-800/80 rounded-xl p-5 hover:border-slate-700 transition">
          <div className="flex items-center justify-between text-slate-400 mb-2">
            <span className="text-xs font-medium uppercase tracking-wider">动态 API 延迟 (P50)</span>
            <Clock className="w-4 h-4 text-sky-400" />
          </div>
          <div className="text-2xl font-bold font-mono text-white">
            {p50 > 0 ? `${p50} ms` : '--'}
          </div>
          <div className="text-xs text-slate-400 mt-2 flex items-center justify-between">
            <span>P95: <strong className="text-slate-300 font-mono">{p95}ms</strong></span>
            <span>P99: <strong className="text-slate-300 font-mono">{p99}ms</strong></span>
          </div>
        </div>

        {/* Connection Reuse */}
        <div className="bg-[#131c2e] border border-slate-800/80 rounded-xl p-5 hover:border-slate-700 transition">
          <div className="flex items-center justify-between text-slate-400 mb-2">
            <span className="text-xs font-medium uppercase tracking-wider">连接复用率 (Reuse)</span>
            <Wifi className="w-4 h-4 text-emerald-400" />
          </div>
          <div className="text-2xl font-bold font-mono text-emerald-400">
            {telemetry?.reuse_rate_percent !== undefined ? `${telemetry.reuse_rate_percent}%` : '--'}
          </div>
          <div className="text-xs text-slate-400 mt-2 flex items-center justify-between">
            <span>已复用: <strong className="text-slate-300 font-mono">{telemetry?.reused_connections ?? 0} 次</strong></span>
            <span>新建: <strong className="text-slate-300 font-mono">{telemetry?.new_connections ?? 0} 次</strong></span>
          </div>
        </div>

        {/* Cache Hit Ratio */}
        <div className="bg-[#131c2e] border border-slate-800/80 rounded-xl p-5 hover:border-slate-700 transition">
          <div className="flex items-center justify-between text-slate-400 mb-2">
            <span className="text-xs font-medium uppercase tracking-wider">静态缓存命中率</span>
            <Database className="w-4 h-4 text-amber-400" />
          </div>
          <div className="text-2xl font-bold font-mono text-amber-300">
            {hitRatio}%
          </div>
          <div className="text-xs text-slate-400 mt-2 flex items-center justify-between">
            <span>RAM: <strong className="text-slate-300 font-mono">{status.requests.ram_hits}</strong></span>
            <span>SSD: <strong className="text-slate-300 font-mono">{status.requests.disk_hits}</strong></span>
            <span>Miss: <strong className="text-slate-300 font-mono">{status.requests.cache_misses}</strong></span>
          </div>
        </div>

        {/* Prefetch Status */}
        <div className="bg-[#131c2e] border border-slate-800/80 rounded-xl p-5 hover:border-slate-700 transition">
          <div className="flex items-center justify-between text-slate-400 mb-2">
            <span className="text-xs font-medium uppercase tracking-wider">预加载调度器 (Pacer)</span>
            <Layers className="w-4 h-4 text-indigo-400" />
          </div>
          <div className="flex items-center gap-2">
            <div className={`w-2.5 h-2.5 rounded-full ${isYielding ? 'bg-amber-400 animate-pulse' : 'bg-emerald-400'}`} />
            <div className="text-lg font-bold font-mono text-white">
              {isYielding ? '主动避让中 (Yielding)' : '就绪 (Active)'}
            </div>
          </div>
          <div className="text-xs text-slate-400 mt-2 flex items-center justify-between">
            <span>队列深度: <strong className="text-slate-300 font-mono">{prefetch?.queue_size ?? 0}</strong></span>
            <span>已预加载: <strong className="text-slate-300 font-mono">{status.requests.prefetch_reused}</strong></span>
          </div>
        </div>
      </div>

      {/* 3. Charts & Breakdown */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        {/* Latency Bar Chart */}
        <div className="lg:col-span-2 bg-[#131c2e] border border-slate-800/80 rounded-2xl p-6">
          <div className="flex items-center justify-between mb-4">
            <div>
              <h3 className="text-sm font-semibold text-white tracking-wide">动态 API 耗时分位数 (ms)</h3>
              <p className="text-xs text-slate-400 mt-0.5">严格测量游戏核心请求（如战斗结算、施放技能）的端到端真实耗时</p>
            </div>
            <Activity className="w-4 h-4 text-sky-400" />
          </div>

          <div className="h-64">
            {p50 > 0 ? (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={latencyData} layout="vertical" margin={{ top: 10, right: 30, left: 60, bottom: 5 }}>
                  <XAxis type="number" stroke="#64748b" tickFormatter={(v) => `${v}ms`} />
                  <YAxis type="category" dataKey="name" stroke="#94a3b8" tick={{ fontSize: 12 }} />
                  <Tooltip 
                    contentStyle={{ backgroundColor: '#0f172a', borderColor: '#334155', borderRadius: '8px', color: '#fff' }}
                    formatter={(val: any) => [`${val} ms`, '耗时']}
                  />
                  <Bar dataKey="value" radius={[0, 6, 6, 0]}>
                    {latencyData.map((entry, index) => (
                      <Cell key={`cell-${index}`} fill={entry.color} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            ) : (
              <div className="flex flex-col items-center justify-center h-full text-slate-500 text-sm">
                <Activity className="w-8 h-8 mb-2 opacity-30" />
                <span>暂无动态 API 请求样本 (进入副本或发起操作即可采集)</span>
              </div>
            )}
          </div>
        </div>

        {/* Cache Breakdown Pie Chart */}
        <div className="bg-[#131c2e] border border-slate-800/80 rounded-2xl p-6">
          <div className="flex items-center justify-between mb-4">
            <div>
              <h3 className="text-sm font-semibold text-white tracking-wide">请求响应来源分布</h3>
              <p className="text-xs text-slate-400 mt-0.5">RAM 热存 / SSD 磁盘 / 上游网络拉取</p>
            </div>
            <Database className="w-4 h-4 text-emerald-400" />
          </div>

          <div className="h-48 flex items-center justify-center">
            {cachePieData.length > 0 ? (
              <ResponsiveContainer width="100%" height="100%">
                <PieChart>
                  <Pie
                    data={cachePieData}
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={75}
                    paddingAngle={4}
                    dataKey="value"
                  >
                    {cachePieData.map((entry, index) => (
                      <Cell key={`cell-${index}`} fill={entry.color} />
                    ))}
                  </Pie>
                  <Tooltip 
                    contentStyle={{ backgroundColor: '#0f172a', borderColor: '#334155', borderRadius: '8px', color: '#fff' }}
                    formatter={(val: any, name: any) => [`${val} 次`, name]}
                  />
                </PieChart>
              </ResponsiveContainer>
            ) : (
              <div className="text-slate-500 text-xs">暂无静态资源访问记录</div>
            )}
          </div>

          <div className="space-y-2 mt-2 pt-2 border-t border-slate-800 text-xs">
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-full bg-emerald-500" /> RAM 内存命中</span>
              <strong className="font-mono text-slate-200">{status.requests.ram_hits} 次</strong>
            </div>
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-full bg-sky-400" /> SSD 磁盘命中</span>
              <strong className="font-mono text-slate-200">{status.requests.disk_hits} 次</strong>
            </div>
            <div className="flex items-center justify-between">
              <span className="flex items-center gap-1.5"><span className="w-2.5 h-2.5 rounded-full bg-slate-500" /> 未命中拉取</span>
              <strong className="font-mono text-slate-200">{misses} 次</strong>
            </div>
          </div>
        </div>
      </div>

      {/* 4. Safety Guardrails Indicator (P0 Compliance Bar) */}
      <div className="bg-[#101726] border border-slate-800/60 rounded-xl p-4 flex flex-wrap items-center justify-between gap-4 text-xs text-slate-400">
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-emerald-400 shrink-0" />
          <span>架构安全防护治理状态:</span>
          <span className="text-emerald-400 font-medium">业务语义透明 (P0)</span>
          <span className="text-slate-600">|</span>
          <span className="text-emerald-400 font-medium">心跳绝对穿透 (P0)</span>
          <span className="text-slate-600">|</span>
          <span className="text-emerald-400 font-medium">写请求零重试 (P0)</span>
        </div>
        <div className="text-slate-500 font-mono">
          GBF-Accelerator Architecture Engine v2.0
        </div>
      </div>
    </div>
  )
}
