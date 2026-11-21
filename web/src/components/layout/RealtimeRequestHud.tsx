import React, { useState, useEffect, useId } from 'react'
import { Zap, Download, ArrowLeftRight } from 'lucide-react'
import { RuntimeStatus } from '../../types'
import { useI18n } from '../../i18n'
import { useTrafficHistory } from '../../hooks/useTrafficHistory'

let lastKnownMetrics = {
  totalHits: 0,
  ramHits: 0,
  downloads: 0,
  apis: 0,
}

export const RealtimeRequestHud: React.FC<{
  status: RuntimeStatus | null
  running: boolean
  compact?: boolean
}> = ({ status, running, compact = false }) => {
  const { t } = useI18n()
  const gradId = useId()

  const [counts, setCounts] = useState(() => ({
    totalHits: running ? (lastKnownMetrics.totalHits || (status?.requests?.total_hits ?? 0)) : 0,
    ramHits: running ? (lastKnownMetrics.ramHits || (status?.requests?.ram_hits ?? 0)) : 0,
    downloads: running ? (lastKnownMetrics.downloads || (status?.requests?.cache_misses ?? 0)) : 0,
    apis: running ? (lastKnownMetrics.apis || (status?.requests?.total_apis ?? 0)) : 0,
  }))

  useEffect(() => {
    if (!running) return
    const next = {
      totalHits: status?.requests?.total_hits ?? 0,
      ramHits: status?.requests?.ram_hits ?? 0,
      downloads: status?.requests?.cache_misses ?? 0,
      apis: status?.requests?.total_apis ?? 0,
    }
    lastKnownMetrics = next
    setCounts(next)
  }, [
    running,
    status?.requests?.total_hits,
    status?.requests?.ram_hits,
    status?.requests?.cache_misses,
    status?.requests?.total_apis,
  ])

  useEffect(() => {
    const onMetrics = (event: Event) => {
      const data = (event as CustomEvent).detail
      const requests = data?.requests
      if (!requests || !running) return
      setCounts((prev) => {
        const next = {
          totalHits: typeof requests.total_hits === 'number' ? requests.total_hits : prev.totalHits,
          ramHits: typeof requests.ram_hits === 'number' ? requests.ram_hits : prev.ramHits,
          downloads: typeof requests.cache_misses === 'number' ? requests.cache_misses : prev.downloads,
          apis: typeof requests.total_apis === 'number' ? requests.total_apis : prev.apis,
        }
        lastKnownMetrics = next
        return next
      })
    }

    window.addEventListener('gbf-metrics', onMetrics)
    return () => window.removeEventListener('gbf-metrics', onMetrics)
  }, [running])

  const totalRequests = counts.totalHits + counts.downloads + counts.apis
  const { currentQps, getPolylinePoints } = useTrafficHistory(running ? totalRequests : 0, 28)

  if (compact) {
    return (
      <div className="flex items-center gap-1.5 sm:gap-2 flex-nowrap">
        <span
          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full bg-sys-greenBg text-sys-green border border-emerald-500/20 text-xs font-mono font-bold tnum whitespace-nowrap shrink-0"
          title={`${t('hud.totalHits')}: ${counts.totalHits.toLocaleString()}${counts.ramHits > 0 ? ` (${t('hud.ramHits', { count: counts.ramHits.toLocaleString() })})` : ''}`}
        >
          <Zap className="w-3.5 h-3.5 text-emerald-500 shrink-0" />
          <span className="hidden md:inline font-sans font-medium text-label-secondary text-[11px] whitespace-nowrap">
            {t('hud.totalHitsShort') || '命中'}:
          </span>
          <span>{counts.totalHits.toLocaleString()}</span>
          {counts.ramHits > 0 && (
            <span className="hidden lg:inline text-[10px] font-sans opacity-80 whitespace-nowrap">
              (RAM {counts.ramHits.toLocaleString()})
            </span>
          )}
        </span>

        <span
          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full bg-sys-blueBg text-sys-blue border border-sky-500/20 text-xs font-mono font-bold tnum whitespace-nowrap shrink-0"
          title={`${t('hud.remoteDownloads')}: ${counts.downloads.toLocaleString()}`}
        >
          <Download className="w-3.5 h-3.5 text-sky-500 shrink-0" />
          <span className="hidden md:inline font-sans font-medium text-label-secondary text-[11px] whitespace-nowrap">
            {t('hud.remoteDownloadsShort') || '下载'}:
          </span>
          <span>{counts.downloads.toLocaleString()}</span>
        </span>

        <span
          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full bg-surface-active text-label-primary border border-hairline text-xs font-mono font-bold tnum whitespace-nowrap shrink-0"
          title={`${t('hud.apiForwarding')}: ${counts.apis.toLocaleString()}`}
        >
          <ArrowLeftRight className="w-3.5 h-3.5 text-label-tertiary shrink-0" />
          <span className="hidden md:inline font-sans font-medium text-label-secondary text-[11px] whitespace-nowrap">
            {t('hud.apiForwardingShort') || '转发'}:
          </span>
          <span>{counts.apis.toLocaleString()}</span>
        </span>
      </div>
    )
  }

  const sparkPoints = getPolylinePoints(84, 24)
  const sparkAreaPoints = `0,24 ${sparkPoints} 84,24`

  return (
    <div className="grid grid-cols-3 gap-2.5 sm:gap-3 pt-0.5">
      {/* 1. Local Cache Hits */}
      <div className="inset-panel rounded-xl px-3 sm:px-4 py-2.5 sm:py-3 flex items-center justify-between transition-colors min-w-0">
        <div className="flex flex-col min-w-0">
          <span className="text-[11px] sm:text-xs font-medium text-label-secondary flex items-center gap-1.5">
            <Zap className="w-3.5 h-3.5 text-emerald-500 shrink-0" />
            <span className="truncate">{t('hud.totalHits')}</span>
          </span>
          <div className="flex items-baseline gap-1.5 mt-1 flex-wrap">
            <span className="text-lg sm:text-xl lg:text-2xl font-bold text-sys-green font-mono tnum leading-none tracking-tight">
              {counts.totalHits.toLocaleString()}
            </span>
            {counts.ramHits > 0 && (
              <span className="text-[10px] font-mono font-semibold text-sys-green bg-sys-greenBg px-1.5 py-0.5 rounded-full border border-emerald-500/20 leading-none whitespace-nowrap">
                {t('hud.ramHits', { count: counts.ramHits.toLocaleString() })}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 2. Upstream Static Downloads */}
      <div className="inset-panel rounded-xl px-3 sm:px-4 py-2.5 sm:py-3 flex items-center justify-between transition-colors min-w-0">
        <div className="flex flex-col min-w-0">
          <span className="text-[11px] sm:text-xs font-medium text-label-secondary flex items-center gap-1.5">
            <Download className="w-3.5 h-3.5 text-sky-500 shrink-0" />
            <span className="truncate">{t('hud.remoteDownloads')}</span>
          </span>
          <div className="flex items-baseline gap-1.5 mt-1">
            <span className="text-lg sm:text-xl lg:text-2xl font-bold text-sys-blue font-mono tnum leading-none tracking-tight">
              {counts.downloads.toLocaleString()}
            </span>
          </div>
        </div>
      </div>

      {/* 3. Transparent API Forwarding + Live 30s QPS Sparkline */}
      <div className="inset-panel rounded-xl px-3 sm:px-4 py-2.5 sm:py-3 flex items-center justify-between gap-2 transition-colors min-w-0">
        <div className="flex flex-col min-w-0">
          <span className="text-[11px] sm:text-xs font-medium text-label-secondary flex items-center gap-1.5">
            <ArrowLeftRight className="w-3.5 h-3.5 text-label-tertiary shrink-0" />
            <span className="truncate">{t('hud.apiForwarding')}</span>
          </span>
          <div className="flex items-baseline gap-2 mt-1">
            <span className="text-lg sm:text-xl lg:text-2xl font-bold text-label-primary font-mono tnum leading-none tracking-tight">
              {counts.apis.toLocaleString()}
            </span>
          </div>
        </div>

        {/* Live 30s QPS Sparkline (shown when container has wide room >=1024px) */}
        <div className="hidden lg:flex flex-col items-end shrink-0" aria-hidden="true">
          <span className="text-[10px] font-mono tnum text-label-tertiary leading-none mb-1">
            {running ? `${currentQps} req/s` : '0 req/s'}
          </span>
          <svg width="84" height="24" viewBox="0 0 84 24" className="overflow-visible">
            <defs>
              <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--spark-stroke)" stopOpacity="0.28" />
                <stop offset="100%" stopColor="var(--spark-stroke)" stopOpacity="0" />
              </linearGradient>
            </defs>
            <polygon points={sparkAreaPoints} fill={`url(#${gradId})`} />
            <polyline
              fill="none"
              stroke="var(--spark-stroke)"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
              points={sparkPoints}
            />
          </svg>
        </div>
      </div>
    </div>
  )
}
