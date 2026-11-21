import React from 'react'
import { Zap, Globe, Wifi } from 'lucide-react'
import { useI18n } from '../../i18n'
import { Card, InsetPanel } from '../common/Card'

export interface TelemetryPanelProps {
  totalHitRate: number
  ramHitPct: number
  diskHitPct: number
  missHitPct: number
  ramHitsCount: number
  diskHitsCount: number
  downloadsCount: number
  hitsCount: number
  connReusePercent: number
  reusedConnections: number
  newConnections: number
  activeForeground: number
  h2Count: number
  h1Count: number
  latencySamples: number
  p50Latency: number
  p95Latency: number
  activeApiCount: number
  isPrefetch: boolean
  prefetchReusedCount: number
}

export const TelemetryPanel: React.FC<TelemetryPanelProps> = ({
  totalHitRate,
  ramHitPct,
  diskHitPct,
  missHitPct,
  ramHitsCount,
  diskHitsCount,
  downloadsCount,
  hitsCount,
  connReusePercent,
  reusedConnections,
  newConnections,
  activeForeground,
  h2Count,
  h1Count,
  latencySamples,
  p50Latency,
  p95Latency,
  activeApiCount,
  isPrefetch,
  prefetchReusedCount,
}) => {
  const { t } = useI18n()

  return (
    <Card>
      <div className="flex items-center justify-between pb-2.5 hairline-b">
        <div className="flex items-center gap-2.5">
          <span className="w-2 h-2 rounded-full bg-sky-500 shrink-0" />
          <h3 className="text-sm sm:text-[15px] font-bold text-label-primary tracking-tight">
            {t('telemetry.title')}
          </h3>
          <span className="text-[11px] text-label-tertiary font-mono tracking-wide hidden sm:inline">
            ENGINE TELEMETRY
          </span>
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
        {/* Col 1: Asset Routing & Cache Ratio */}
        <InsetPanel className="p-3.5 sm:p-4 flex flex-col justify-between gap-3">
          <div className="flex items-center justify-between">
            <span className="text-[13px] font-semibold text-label-secondary flex items-center gap-1.5">
              <Zap className="w-4 h-4 text-emerald-500 shrink-0" />
              <span>{t('telemetry.ratioTitle')}</span>
            </span>
            <span className="text-sm sm:text-base font-bold font-mono tnum text-sys-green">
              {totalHitRate > 0 ? `${totalHitRate}%` : '--'}
            </span>
          </div>

          <div className="space-y-1.5">
            <div className="w-full h-2.5 bg-slate-500/15 rounded-full overflow-hidden flex shadow-inner">
              <div
                className="h-full bg-emerald-500 transition-all duration-300"
                style={{ width: `${ramHitPct}%` }}
                title={t('telemetry.ramHitsTip', { count: ramHitsCount, pct: ramHitPct })}
              />
              <div
                className="h-full bg-sky-500 transition-all duration-300"
                style={{ width: `${diskHitPct}%` }}
                title={t('telemetry.diskHitsTip', { count: diskHitsCount, pct: diskHitPct })}
              />
              <div
                className="h-full bg-amber-400/90 transition-all duration-300"
                style={{ width: `${missHitPct}%` }}
                title={t('telemetry.downloadsTip', { count: downloadsCount, pct: missHitPct })}
              />
            </div>
            <div className="flex items-center justify-between text-xs text-label-tertiary font-mono tnum">
              <span className="flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0" />
                RAM: {ramHitsCount}
              </span>
              <span className="flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-sky-500 shrink-0" />
                {t('telemetry.diskCount', { count: diskHitsCount })}
              </span>
              <span className="flex items-center gap-1">
                <span className="w-2 h-2 rounded-full bg-amber-400 shrink-0" />
                {t('telemetry.downloadsCount', { count: downloadsCount })}
              </span>
            </div>
          </div>

          <div className="text-xs text-label-tertiary flex items-center justify-between pt-2 hairline-t">
            <span>{t('telemetry.savedRequests')}</span>
            <span className="font-mono tnum font-semibold text-label-primary">
              {t('telemetry.savedTimes', { count: hitsCount })}
            </span>
          </div>
        </InsetPanel>

        {/* Col 2: HTTP/2 Multiplexing & Handshake Savings */}
        <InsetPanel className="p-3.5 sm:p-4 flex flex-col justify-between gap-3">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-semibold text-label-secondary flex items-center gap-1.5 min-w-0">
              <Globe className="w-4 h-4 text-sky-500 shrink-0" />
              <span className="truncate">{t('telemetry.h2Title')}</span>
            </span>
            <span className="text-sm sm:text-base font-bold font-mono tnum text-sys-blue shrink-0">
              {connReusePercent > 0 ? `${connReusePercent}%` : '--'}
            </span>
          </div>

          <div className="space-y-2 text-xs text-label-secondary">
            <div className="flex items-center justify-between gap-2">
              <span className="text-label-tertiary">{t('telemetry.avoidHandshakes')}</span>
              <span className="font-mono tnum font-semibold text-sys-green whitespace-nowrap shrink-0">
                {t('telemetry.timesPlus', { count: reusedConnections })}
              </span>
            </div>
            <div className="flex items-center justify-between gap-2">
              <span className="text-label-tertiary">{t('telemetry.newVsActive')}</span>
              <span className="font-mono tnum text-label-primary whitespace-nowrap shrink-0">
                {newConnections} / {activeForeground}
              </span>
            </div>
          </div>

          <div className="text-xs text-label-tertiary flex items-center justify-between gap-2 pt-2 hairline-t">
            <span>{t('telemetry.protocolMultiplex')}</span>
            <span className="font-mono tnum font-medium text-sys-blue whitespace-nowrap shrink-0">
              H2: {h2Count} | H1: {h1Count}
            </span>
          </div>
        </InsetPanel>

        {/* Col 3: Upstream Latency & Yielding Guardrail */}
        <InsetPanel className="p-3.5 sm:p-4 flex flex-col justify-between gap-3">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-semibold text-label-secondary flex items-center gap-1.5 min-w-0">
              <Wifi className="w-4 h-4 text-indigo-500 shrink-0" />
              <span className="truncate">{t('telemetry.upstreamLatencyTitle')}</span>
            </span>
            <span className="text-xs font-mono tnum text-label-tertiary whitespace-nowrap shrink-0">
              {t('telemetry.sampleCount', { count: latencySamples })}
            </span>
          </div>

          <div className="space-y-2 text-xs text-label-secondary">
            <div className="flex items-center justify-between gap-2">
              <span className="text-label-tertiary">{t('telemetry.latencyMetrics')}</span>
              <span className="font-mono tnum font-semibold text-label-primary whitespace-nowrap shrink-0">
                {p50Latency > 0 ? `${p50Latency}ms` : '--'} / {p95Latency > 0 ? `${p95Latency}ms` : '--'}
              </span>
            </div>
            <div className="flex items-center justify-between gap-2">
              <span className="text-label-tertiary">{t('telemetry.apiAvoidance')}</span>
              <span className={`font-medium whitespace-nowrap shrink-0 ${activeApiCount > 0 ? 'text-sys-amber font-semibold' : 'text-sys-green'}`}>
                {activeApiCount > 0
                  ? t('telemetry.avoidingActive', { count: activeApiCount })
                  : t('telemetry.avoidingReady')}
              </span>
            </div>
          </div>

          <div className="text-xs text-label-tertiary flex items-center justify-between gap-2 pt-2 hairline-t">
            <span>{t('telemetry.prefetchHitsTitle')}</span>
            {!isPrefetch ? (
              <span className="text-label-tertiary font-medium whitespace-nowrap shrink-0">{t('cards.upstream.failoverNotEnabled')}</span>
            ) : prefetchReusedCount > 0 ? (
              <span className="font-mono tnum font-semibold text-indigo-500 dark:text-indigo-400 whitespace-nowrap shrink-0">
                {t('telemetry.prefetchHitItems', { count: prefetchReusedCount })}
              </span>
            ) : diskHitsCount + ramHitsCount > 0 ? (
              <span className="font-medium text-sys-green whitespace-nowrap shrink-0">{t('telemetry.prefetchReady')}</span>
            ) : (
              <span className="font-medium text-label-tertiary whitespace-nowrap shrink-0">{t('telemetry.prefetchPending')}</span>
            )}
          </div>
        </InsetPanel>
      </div>
    </Card>
  )
}
