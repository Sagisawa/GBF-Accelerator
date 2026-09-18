import React from 'react'
import { RuntimeStatus, TelemetrySummary, PrefetchStatus } from '../../types'
import { useTrafficHistory } from '../../hooks/useTrafficHistory'
import { Database, ArrowDownToLine, ArrowRightLeft, ShieldAlert } from 'lucide-react'

export interface TelemetryStripProps {
  status: RuntimeStatus | null
  telemetry: TelemetrySummary | null
  prefetch: PrefetchStatus | null
}

export const TelemetryStrip: React.FC<TelemetryStripProps> = ({
  status,
  telemetry,
  prefetch,
}) => {
  const req = status?.requests || {
    total_apis: 0,
    total_assets: 0,
    total_hits: 0,
    ram_hits: 0,
    disk_hits: 0,
    cache_misses: 0,
    prefetch_requests: 0,
    prefetch_reused: 0,
    api_retries: 0,
  }

  const totalHits = req.total_hits || 0
  const ramHits = req.ram_hits || 0
  const diskHits = req.disk_hits || 0
  const misses = req.cache_misses || 0
  const totalLookups = totalHits + misses
  const hitRatio = totalLookups > 0 ? ((totalHits / totalLookups) * 100).toFixed(1) : '0.0'

  const ramPct = totalLookups > 0 ? (ramHits / totalLookups) * 100 : 0
  const diskPct = totalLookups > 0 ? (diskHits / totalLookups) * 100 : 0
  const missPct = totalLookups > 0 ? (misses / totalLookups) * 100 : 0

  const p50 = telemetry?.percentiles?.p50_ms ?? 0
  const p95 = telemetry?.percentiles?.p95_ms ?? 0
  const reuseRate = telemetry?.reuse_rate_percent ?? 100

  const isYielding = Boolean(
    (status && status.active_api_count > 0) || prefetch?.is_yielding
  )

  const totalRequests = (req.total_apis || 0) + (req.total_assets || 0)
  const { currentQps, getPolylinePoints } = useTrafficHistory(totalRequests)

  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-3.5 select-none">
      {/* Feature Card 1: Local Cache Hits */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] hover:border-white/[0.14] rounded-2xl p-4 flex flex-col justify-between transition-all duration-200">
        <div>
          <div className="flex items-center justify-between text-xs mb-2">
            <div className="flex items-center gap-2">
              <div className="w-6 h-6 rounded-lg bg-apple-green/15 text-apple-green flex items-center justify-center shrink-0">
                <Database className="w-3.5 h-3.5" />
              </div>
              <span className="font-semibold text-white tracking-tight text-xs">
                本地静态缓存
              </span>
            </div>
            <span className="text-[11px] font-mono text-apple-green font-semibold px-2 py-0.5 rounded-full bg-apple-green/15 tnum">
              {hitRatio}% 命中
            </span>
          </div>

          <div className="text-2xl font-bold font-mono text-white tnum tracking-tight mt-1">
            {totalHits.toLocaleString()}{' '}
            <span className="text-xs font-sans font-normal text-label-secondary">次命中</span>
          </div>
        </div>

        <div className="mt-3.5 space-y-2">
          {/* Segmented Ratio Pill */}
          <div className="w-full h-1.5 rounded-full bg-white/[0.08] overflow-hidden flex">
            <div
              style={{ width: `${ramPct}%` }}
              className="bg-apple-green transition-all duration-300"
              title={`RAM 内存命中: ${ramHits}`}
            />
            <div
              style={{ width: `${diskPct}%` }}
              className="bg-apple-blue transition-all duration-300"
              title={`SSD 磁盘命中: ${diskHits}`}
            />
            <div
              style={{ width: `${missPct}%` }}
              className="bg-white/20 transition-all duration-300"
              title={`远程下载拉取: ${misses}`}
            />
          </div>

          <div className="flex items-center justify-between text-[11px] font-mono text-label-secondary tnum">
            <span>RAM <strong className="text-apple-green font-medium">{ramHits.toLocaleString()}</strong></span>
            <span>SSD <strong className="text-apple-blue font-medium">{diskHits.toLocaleString()}</strong></span>
            <span>拉取 <strong className="text-white font-medium">{misses.toLocaleString()}</strong></span>
          </div>
        </div>
      </div>

      {/* Feature Card 2: Remote Upstream & Connection Reuse */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] hover:border-white/[0.14] rounded-2xl p-4 flex flex-col justify-between transition-all duration-200">
        <div>
          <div className="flex items-center justify-between text-xs mb-2">
            <div className="flex items-center gap-2">
              <div className="w-6 h-6 rounded-lg bg-apple-blue/15 text-apple-blue flex items-center justify-center shrink-0">
                <ArrowDownToLine className="w-3.5 h-3.5" />
              </div>
              <span className="font-semibold text-white tracking-tight text-xs">
                远程下载与连接
              </span>
            </div>
            <span className="text-[11px] font-mono text-apple-blue font-semibold px-2 py-0.5 rounded-full bg-apple-blue/15 tnum">
              {reuseRate}% H2复用
            </span>
          </div>

          <div className="flex items-baseline justify-between mt-1">
            <div className="text-2xl font-bold font-mono text-white tnum tracking-tight">
              {misses.toLocaleString()}{' '}
              <span className="text-xs font-sans font-normal text-label-secondary">次拉取</span>
            </div>

            {/* Apple Style Sparkline */}
            <div className="flex items-center gap-1.5" title="瞬时 QPS 走势">
              <svg className="w-20 h-6 overflow-visible" viewBox="0 0 100 24">
                <polyline
                  fill="none"
                  stroke="#0071E3"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  points={getPolylinePoints(100, 24)}
                />
              </svg>
            </div>
          </div>
        </div>

        <div className="mt-3.5 flex items-center justify-between text-[11px] font-mono text-label-secondary tnum border-t border-white/[0.06] pt-2">
          <span>瞬时吞吐 <strong className="text-white font-medium">{currentQps} QPS</strong></span>
          <span>连接复用 <strong className="text-apple-blue font-medium">{reuseRate}%</strong></span>
        </div>
      </div>

      {/* Feature Card 3: Dynamic API & Pacing Yield */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] hover:border-white/[0.14] rounded-2xl p-4 flex flex-col justify-between transition-all duration-200">
        <div>
          <div className="flex items-center justify-between text-xs mb-2">
            <div className="flex items-center gap-2">
              <div className="w-6 h-6 rounded-lg bg-apple-amber/15 text-apple-amber flex items-center justify-center shrink-0">
                <ArrowRightLeft className="w-3.5 h-3.5" />
              </div>
              <span className="font-semibold text-white tracking-tight text-xs">
                游戏 API 转发
              </span>
            </div>
            {isYielding ? (
              <span className="inline-flex items-center gap-1 text-[10px] px-2 py-0.5 rounded-full font-medium bg-apple-amber/15 text-apple-amber border border-apple-amber/30 animate-pulse">
                <ShieldAlert className="w-3 h-3" /> 避让中
              </span>
            ) : (
              <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-white/[0.08] text-label-secondary">
                前台优先
              </span>
            )}
          </div>

          <div className="text-2xl font-bold font-mono text-white tnum tracking-tight mt-1">
            {req.total_apis.toLocaleString()}{' '}
            <span className="text-xs font-sans font-normal text-label-secondary">次穿透</span>
          </div>
        </div>

        <div className="mt-3.5 flex items-center justify-between text-[11px] font-mono text-label-secondary tnum border-t border-white/[0.06] pt-2">
          <span>
            P50 延迟 <strong className="text-white font-medium">{p50 > 0 ? `${p50}ms` : '--'}</strong>
          </span>
          <span>
            P95 <strong className="text-apple-amber font-medium">{p95 > 0 ? `${p95}ms` : '--'}</strong>
          </span>
          {req.api_retries > 0 && (
            <span>重试 <strong className="text-apple-red font-medium">{req.api_retries}</strong></span>
          )}
        </div>
      </div>
    </div>
  )
}
