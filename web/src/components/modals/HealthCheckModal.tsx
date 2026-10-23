import React, { useState, useEffect, useCallback } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import {
  Activity,
  RefreshCw,
  CheckCircle2,
  AlertTriangle,
  XCircle,
  HelpCircle,
  ShieldAlert,
  Server,
  Wrench,
} from 'lucide-react'
import { fetchHealth, repairHealth, testLatency } from '../../api'
import { HealthResponse, HealthItem, HealthStatusLevel } from '../../types'
import { useI18n } from '../../i18n'

export interface HealthCheckModalProps {
  isOpen: boolean
  onClose: () => void
}

interface PingProbeState {
  loading: boolean
  tested: boolean
  ok: boolean
  latencyMs: number | null
  error: string | null
}

export const HealthCheckModal: React.FC<HealthCheckModalProps> = ({
  isOpen,
  onClose,
}) => {
  const { t } = useI18n()
  const [loading, setLoading] = useState(false)
  const [data, setData] = useState<HealthResponse | null>(null)
  const [connError, setConnError] = useState<string | null>(null)
  const [repairing, setRepairing] = useState(false)
  const [repairingCode, setRepairingCode] = useState<string | null>(null)
  const [repairMessage, setRepairMessage] = useState<{ text: string; success: boolean } | null>(null)
  const [confirmItem, setConfirmItem] = useState<HealthItem | null>(null)
  const [pingState, setPingState] = useState<PingProbeState>({
    loading: false,
    tested: false,
    ok: false,
    latencyMs: null,
    error: null,
  })

  const runPingProbe = useCallback(async () => {
    setPingState((prev) => ({ ...prev, loading: true, error: null }))
    try {
      const res = await testLatency(undefined, true)
      if (res && res.ok) {
        const lat = Math.round(res.warm_mid_ms ?? res.cold_ms ?? 0)
        setPingState({
          loading: false,
          tested: true,
          ok: true,
          latencyMs: lat,
          error: null,
        })
      } else {
        setPingState({
          loading: false,
          tested: true,
          ok: false,
          latencyMs: null,
          error: res?.error || t('modals.healthCheck.cannotConnectTarget'),
        })
      }
    } catch (e: any) {
      setPingState({
        loading: false,
        tested: true,
        ok: false,
        latencyMs: null,
        error: e?.message || t('modals.healthCheck.timeout'),
      })
    }
  }, [t])

  const runHealthCheck = useCallback(async () => {
    setLoading(true)
    setConnError(null)
    try {
      const res = await fetchHealth()
      setData(res)
    } catch (e: any) {
      // Requirement 2: When /api/health request fails to connect, directly display error
      setConnError(t('modals.healthCheck.connErrorLead'))
      setData(null)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    if (isOpen) {
      runHealthCheck()
      runPingProbe()
    }
  }, [isOpen, runHealthCheck, runPingProbe])

  const executeRepair = async (code?: string) => {
    setRepairing(true)
    setRepairingCode(code || 'all')
    setRepairMessage(null)
    setConfirmItem(null)

    const isBatch = !code || code === 'all'
    const isControlPlaneTarget =
      code === 'CONTROL_PLANE_NON_LOOPBACK' ||
      (isBatch && data?.core_health?.control_plane?.status !== 'ok')

    const isNetworkOrDisconnectError = (error: any): boolean => {
      if (!error) return false
      if (error instanceof TypeError) return true
      const msg = String(error.message || error).toLowerCase()
      return (
        msg.includes('fetch') ||
        msg.includes('network') ||
        msg.includes('reset') ||
        msg.includes('connection') ||
        msg.includes('refused') ||
        msg.includes('socket') ||
        msg.includes('aborted')
      )
    }

    try {
      const res = await repairHealth(code ? { code } : { all: true })
      if (res.health) {
        setData(res.health)
      } else {
        await runHealthCheck()
      }
      setRepairMessage({
        text: res.message || (res.success ? t('modals.healthCheck.repairCompleted') : t('modals.healthCheck.partialRepairFailed')),
        success: res.success,
      })
    } catch (err: any) {
      if (isControlPlaneTarget || isNetworkOrDisconnectError(err)) {
        // Control Plane rebind may disconnect the in-flight HTTP request (TCP Reset, TypeError, etc.).
        // Use bounded fast retries with short backoffs [100, 200, 300, 400, 500ms] (total ~1.5s).
        const retryDelays = [100, 200, 300, 400, 500]
        for (const delay of retryDelays) {
          await new Promise((resolve) => setTimeout(resolve, delay))
          try {
            const healthRes = await fetchHealth()
            if (healthRes && healthRes.core_health) {
              const cp = healthRes.core_health.control_plane
              if (cp && cp.status === 'ok') {
                setData(healthRes)
                setConnError(null)
                setRepairMessage({
                  text:
                    code === 'CONTROL_PLANE_NON_LOOPBACK'
                      ? t('modals.healthCheck.loopbackRebound')
                      : healthRes.status === 'ok'
                      ? t('modals.healthCheck.allRepairedNormal')
                      : t('modals.healthCheck.reconnectedUpdated'),
                  success: true,
                })
                return
              } else if (cp && cp.code !== 'CONTROL_PLANE_NON_LOOPBACK') {
                setData(healthRes)
                setConnError(null)
                setRepairMessage({
                  text: t('modals.healthCheck.restoredUpdated'),
                  success: true,
                })
                return
              } else {
                // Reconnected, but Control Plane is still non-loopback -> repair truly failed
                setData(healthRes)
                setRepairMessage({
                  text: t('modals.healthCheck.reconnectedLoopbackRisk'),
                  success: false,
                })
                return
              }
            }
          } catch {
            // Keep waiting for socket rebind to settle
          }
        }

        // Bounded window expired without successful reconnection
        setData(null)
        setConnError(t('modals.healthCheck.connErrorLead'))
        setRepairMessage({
          text: t('modals.healthCheck.reloadNoResponse'),
          success: false,
        })
        return
      }

      setRepairMessage({
        text: err.message || t('modals.healthCheck.repairException'),
        success: false,
      })
    } finally {
      setRepairing(false)
      setRepairingCode(null)
    }
  }

  const handleRepairClick = (item: HealthItem) => {
    if (item.requires_confirmation) {
      setConfirmItem(item)
    } else {
      executeRepair(item.code)
    }
  }

  const handleBatchRepair = () => {
    executeRepair()
  }

  const repairableItems = data
    ? Object.values(data.core_health).filter((it) => it.repairable && it.status !== 'ok')
    : []
  const hasRepairable = repairableItems.length > 0
  const hasElevationItem = repairableItems.some((it) => it.requires_elevation)

  const renderStatusIcon = (status: HealthStatusLevel) => {
    switch (status) {
      case 'ok':
        return <CheckCircle2 className="w-4 h-4 text-emerald-600 shrink-0" />
      case 'warning':
        return <AlertTriangle className="w-4 h-4 text-amber-500 shrink-0" />
      case 'error':
        return <XCircle className="w-4 h-4 text-rose-600 shrink-0" />
      default:
        return <HelpCircle className="w-4 h-4 text-slate-400 shrink-0" />
    }
  }

  const renderStatusBadge = (status: HealthStatusLevel) => {
    switch (status) {
      case 'ok':
        return (
          <span className="px-2 py-0.5 rounded-md text-[10px] font-bold bg-emerald-50 text-emerald-700 border border-emerald-200">
            {t('common.running')}
          </span>
        )
      case 'warning':
        return (
          <span className="px-2 py-0.5 rounded-md text-[10px] font-bold bg-amber-50 text-amber-700 border border-amber-200">
            {t('common.warning')}
          </span>
        )
      case 'error':
        return (
          <span className="px-2 py-0.5 rounded-md text-[10px] font-bold bg-rose-50 text-rose-700 border border-rose-200">
            {t('common.error')}
          </span>
        )
    }
  }

  const renderItemCard = (key: string, item?: HealthItem) => {
    if (!item) return null
    const isThisRepairing = repairingCode === item.code
    return (
      <div
        key={key}
        className={`p-3 rounded-lg border transition-all ${
          item.status === 'error'
            ? 'bg-rose-50/50 border-rose-200 text-slate-900'
            : item.status === 'warning'
            ? 'bg-amber-50/40 border-amber-200 text-slate-900'
            : 'bg-white border-slate-200/90 text-slate-800'
        }`}
      >
        <div className="flex items-start justify-between gap-2">
          <div className="flex items-center gap-2 min-w-0">
            {renderStatusIcon(item.status)}
            <span className="text-xs font-bold text-slate-900 tracking-tight">
              {item.name}
            </span>
          </div>

          <div className="flex items-center gap-1.5 shrink-0 flex-wrap justify-end">
            {item.requires_elevation && item.status !== 'ok' && (
              <span
                className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-50 text-amber-800 border border-amber-300 flex items-center gap-1"
                title={t('modals.healthCheck.uacTooltip')}
              >
                <ShieldAlert className="w-2.5 h-2.5 text-amber-600" />
                <span>UAC</span>
              </span>
            )}
            {item.repairable && item.status !== 'ok' && (
              <button
                type="button"
                disabled={repairing || loading}
                onClick={() => handleRepairClick(item)}
                className="px-2 py-0.5 rounded text-[10px] font-semibold bg-sky-600 hover:bg-sky-700 text-white shadow-sm flex items-center gap-1 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                title={item.requires_confirmation ? t('modals.healthCheck.confirmParamTooltip') : t('modals.healthCheck.repairNowTooltip')}
              >
                {isThisRepairing ? (
                  <RefreshCw className="w-2.5 h-2.5 animate-spin" />
                ) : (
                  <Wrench className="w-2.5 h-2.5" />
                )}
                <span>{isThisRepairing ? t('modals.healthCheck.repairing') : t('modals.healthCheck.tryRepair')}</span>
              </button>
            )}
            {!item.repairable && item.status !== 'ok' && (
              <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-slate-100 text-slate-600 border border-slate-200">
                {t('modals.healthCheck.manualRequired')}
              </span>
            )}
            {renderStatusBadge(item.status)}
          </div>
        </div>

        <div className="mt-1.5 pl-6 text-xs text-slate-700 leading-relaxed">
          <p>{item.message}</p>
          <div className="flex items-center gap-2 mt-1 text-[11px] font-mono text-slate-400">
            <span>code: {item.code}</span>
          </div>
        </div>
      </div>
    )
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Activity className="w-4 h-4 text-sky-600" />
          <span>{t('modals.healthCheck.title')}</span>
        </div>
      }
      subtitle={t('modals.healthCheck.subtitle')}
      maxWidth="max-w-2xl"
    >
      <div className="space-y-4 max-h-[75vh] overflow-y-auto pr-1">
        {/* Loading Spinner */}
        {loading && (
          <div className="py-12 flex flex-col items-center justify-center gap-3 text-slate-500">
            <RefreshCw className="w-6 h-6 animate-spin text-sky-600" />
            <span className="text-xs font-medium">{t('modals.healthCheck.checking')}</span>
          </div>
        )}

        {/* Connection Failure Card (Requirement 2) */}
        {!loading && connError && (
          <div className="p-4 bg-rose-50 border border-rose-200 rounded-xl space-y-2">
            <div className="flex items-center gap-2 text-rose-700 font-bold text-sm">
              <ShieldAlert className="w-5 h-5 shrink-0" />
              <span>{connError}</span>
            </div>
            <p className="text-xs text-rose-700/90 leading-relaxed">
              {t('modals.healthCheck.connErrorDesc')}
            </p>
          </div>
        )}

        {/* Repair Notification Message */}
        {repairMessage && (
          <div
            className={`p-3 rounded-xl border flex items-start justify-between gap-2 text-xs transition-all ${
              repairMessage.success
                ? 'bg-emerald-50 border-emerald-200 text-emerald-900'
                : 'bg-rose-50 border-rose-200 text-rose-900'
            }`}
          >
            <div className="flex items-center gap-2">
              {repairMessage.success ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-600 shrink-0" />
              ) : (
                <AlertTriangle className="w-4 h-4 text-rose-600 shrink-0" />
              )}
              <span className="font-medium">{repairMessage.text}</span>
            </div>
            <button
              type="button"
              onClick={() => setRepairMessage(null)}
              className="text-slate-400 hover:text-slate-600 text-xs px-1 cursor-pointer"
            >
              ✕
            </button>
          </div>
        )}

        {/* Health Check Details */}
        {!loading && data && (
          <>
            {/* Top Status Banner */}
            <div
              className={`p-3.5 rounded-xl border flex items-center justify-between gap-3 ${
                data.status === 'ok'
                  ? 'bg-emerald-50 border-emerald-200 text-emerald-950'
                  : data.status === 'warning'
                  ? 'bg-amber-50 border-amber-200 text-amber-950'
                  : 'bg-rose-50 border-rose-200 text-rose-950'
              }`}
            >
              <div className="flex items-center gap-2.5">
                <div
                  className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 ${
                    data.status === 'ok'
                      ? 'bg-emerald-600 text-white'
                      : data.status === 'warning'
                      ? 'bg-amber-500 text-white'
                      : 'bg-rose-600 text-white'
                  }`}
                >
                  <Server className="w-4 h-4" />
                </div>
                <div>
                  <h4 className="text-xs sm:text-sm font-bold">
                    {data.status === 'ok'
                      ? t('modals.healthCheck.statusOkBanner', { passed: data.summary.passed, total: data.summary.total_checks })
                      : data.status === 'warning'
                      ? t('modals.healthCheck.statusWarnBanner', { warnings: data.summary.warnings, passed: data.summary.passed, total: data.summary.total_checks })
                      : t('modals.healthCheck.statusErrBanner', { errors: data.summary.errors, warnings: data.summary.warnings })}
                  </h4>
                  <p className="text-[11px] opacity-80">
                    {t('modals.healthCheck.checkTime', { time: new Date(data.timestamp).toLocaleTimeString() })}
                  </p>
                </div>
              </div>

              <div className="text-right shrink-0">
                <span
                  className={`px-2.5 py-1 rounded-lg text-xs font-bold ${
                    data.status === 'ok'
                      ? 'bg-emerald-100 text-emerald-800'
                      : data.status === 'warning'
                      ? 'bg-amber-100 text-amber-800'
                      : 'bg-rose-100 text-rose-800'
                  }`}
                >
                  {data.status.toUpperCase()}
                </span>
              </div>
            </div>

            {/* Prominent One-Click Batch Repair Card */}
            {hasRepairable && (
              <div className="p-3 bg-sky-50/80 border border-sky-200 rounded-xl flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3">
                <div className="flex items-center gap-2.5">
                  <div className="w-7 h-7 rounded-lg bg-sky-600 text-white flex items-center justify-center shrink-0">
                    <Wrench className="w-3.5 h-3.5" />
                  </div>
                  <div>
                    <div className="text-xs font-bold text-sky-950 flex items-center gap-2">
                      <span>{t('modals.healthCheck.detectedRepairable', { count: repairableItems.length })}</span>
                      {hasElevationItem && (
                        <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-amber-100 text-amber-800 border border-amber-300">
                          {t('modals.healthCheck.hasUacBadge')}
                        </span>
                      )}
                    </div>
                    <div className="text-[11px] text-sky-700">
                      {t('modals.healthCheck.batchRepairDetail')}{hasElevationItem ? ' ' + t('modals.healthCheck.uacNoticeSuffix') : ''}
                    </div>
                  </div>
                </div>
                <Button
                  variant="primary"
                  size="sm"
                  onClick={handleBatchRepair}
                  disabled={repairing || loading}
                  className="shrink-0 w-full sm:w-auto"
                  icon={<Wrench className={`w-3.5 h-3.5 ${repairingCode === 'all' ? 'animate-spin' : ''}`} />}
                >
                  {repairingCode === 'all' ? t('modals.healthCheck.batchRepairing') : t('modals.healthCheck.oneClickRepair')}
                </Button>
              </div>
            )}

            {/* Repair Progress Notification */}
            {repairing && (
              <div className="p-3 bg-sky-50 border border-sky-200 rounded-xl flex items-center gap-3 text-xs text-sky-900 animate-pulse">
                <RefreshCw className="w-4 h-4 animate-spin text-sky-600 shrink-0" />
                <div className="min-w-0">
                  <div className="font-bold">
                    {repairingCode === 'all'
                      ? t('modals.healthCheck.repairingAll')
                      : t('modals.healthCheck.repairingSpecific', { code: repairingCode || '' })}
                  </div>
                  <p className="text-[11px] text-sky-700 mt-0.5">
                    {hasElevationItem
                      ? t('modals.healthCheck.uacPromptNotice')
                      : t('modals.healthCheck.waitingSecureLoopback')}
                  </p>
                </div>
              </div>
            )}

            {/* Core Health Grid */}
            <div className="space-y-2">
              <h5 className="text-xs font-bold text-slate-700 flex items-center gap-1.5">
                <span>{t('modals.healthCheck.coreSecurityHealth')}</span>
                <span className="text-[10px] font-normal text-slate-400">
                  {t('modals.healthCheck.corePlaneSub')}
                </span>
              </h5>

              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                {renderItemCard('core', data.core_health.core)}
                {renderItemCard('control_plane', data.core_health.control_plane)}
                {renderItemCard('data_plane', data.core_health.data_plane)}
                {renderItemCard('pac', data.core_health.pac)}
                {renderItemCard('root_ca', data.core_health.root_ca)}
                {renderItemCard('cache', data.core_health.cache)}
                {renderItemCard('config', data.core_health.config)}
                {renderItemCard('lan_firewall', data.core_health.lan_firewall)}
              </div>
            </div>

            {/* Runtime Status & Observations (Informational) */}
            <div className="space-y-2 pt-2 border-t border-slate-200/80">
              <h5 className="text-xs font-bold text-slate-700 flex items-center gap-1.5">
                <span>{t('modals.healthCheck.realtimeMetrics')}</span>
                <span className="text-[10px] font-normal text-slate-400">
                  {t('modals.healthCheck.metricsSub')}
                </span>
              </h5>

              <div className="grid grid-cols-2 sm:grid-cols-6 gap-2 text-xs">
                <div className="col-span-2 sm:col-span-2 p-2 sm:p-2.5 bg-slate-50 border border-slate-200 rounded-lg min-w-0">
                  <div className="text-[10px] text-slate-500 font-medium">{t('modals.healthCheck.upstreamRoute')}</div>
                  <div
                    className="font-mono text-[11px] sm:text-xs font-bold text-slate-800 mt-0.5 truncate select-all"
                    title={String(data.runtime_status.upstream_route?.effective_proxy || t('modals.healthCheck.directMode'))}
                  >
                    {data.runtime_status.upstream_route?.effective_proxy || t('modals.healthCheck.directMode')}
                  </div>
                </div>

                <div className="col-span-1 sm:col-span-1 p-2 sm:p-2.5 bg-slate-50 border border-slate-200 rounded-lg min-w-0">
                  <div className="flex items-center justify-between text-[10px] text-slate-500 font-medium">
                    <span title="Target: https://game.granbluefantasy.jp/">{t('modals.healthCheck.gameConnectivity')}</span>
                    <button
                      type="button"
                      onClick={runPingProbe}
                      disabled={pingState.loading}
                      title={t('modals.healthCheck.retestSpeed')}
                      className="text-slate-400 hover:text-sky-600 cursor-pointer disabled:opacity-40 transition-colors p-0.5 -mr-1"
                    >
                      <RefreshCw className={`w-2.5 h-2.5 ${pingState.loading ? 'animate-spin text-sky-500' : ''}`} />
                    </button>
                  </div>
                  <div className="mt-0.5 truncate">
                    {pingState.loading ? (
                      <span className="text-slate-400 text-xs font-mono animate-pulse">{t('modals.healthCheck.testing')}</span>
                    ) : pingState.ok ? (
                      <div className="flex items-center gap-1 font-bold">
                        <span className={`w-1.5 h-1.5 rounded-full shrink-0 ${
                          (pingState.latencyMs ?? 0) <= 150
                            ? 'bg-emerald-500'
                            : (pingState.latencyMs ?? 0) <= 280
                              ? 'bg-sky-500'
                              : 'bg-amber-500'
                        }`} />
                        <span className={`font-mono ${
                          (pingState.latencyMs ?? 0) <= 150
                            ? 'text-emerald-600'
                            : (pingState.latencyMs ?? 0) <= 280
                              ? 'text-sky-600'
                              : 'text-amber-600'
                        }`}>
                          {pingState.latencyMs}ms
                        </span>
                        <span className="text-[10px] font-normal text-slate-500 font-sans">
                          {(pingState.latencyMs ?? 0) <= 150 ? t('modals.healthCheck.latencyExcellent') : (pingState.latencyMs ?? 0) <= 280 ? t('modals.healthCheck.latencyGood') : t('modals.healthCheck.latencyFair')}
                        </span>
                      </div>
                    ) : pingState.tested ? (
                      <div className="flex items-center gap-1 font-bold text-rose-600" title={pingState.error || t('modals.healthCheck.connectFailed')}>
                        <span className="w-1.5 h-1.5 rounded-full bg-rose-500 shrink-0" />
                        <span className="text-[11px] truncate">{t('modals.healthCheck.cannotConnectTarget')}</span>
                      </div>
                    ) : (
                      <span className="text-slate-400 text-xs">{t('modals.healthCheck.notProbed')}</span>
                    )}
                  </div>
                </div>

                <div className="col-span-1 sm:col-span-1 p-2 sm:p-2.5 bg-slate-50 border border-slate-200 rounded-lg min-w-0">
                  <div className="text-[10px] text-slate-500 font-medium">{t('modals.healthCheck.apiRetriesTotal')}</div>
                  <div className="font-bold text-slate-800 mt-0.5">
                    {t('modals.healthCheck.times', { count: data.runtime_status.api_retries })}
                  </div>
                </div>

                <div className="col-span-1 sm:col-span-1 p-2 sm:p-2.5 bg-slate-50 border border-slate-200 rounded-lg min-w-0">
                  <div className="text-[10px] text-slate-500 font-medium">{t('modals.healthCheck.reuseRatio')}</div>
                  <div className="font-bold text-slate-800 mt-0.5">
                    {data.runtime_status.traffic_metrics?.reuse_rate_percent ?? 0}%
                  </div>
                </div>

                <div className="col-span-1 sm:col-span-1 p-2 sm:p-2.5 bg-slate-50 border border-slate-200 rounded-lg min-w-0">
                  <div className="text-[10px] text-slate-500 font-medium">{t('modals.healthCheck.staticCacheHit')}</div>
                  <div className="font-bold text-slate-800 mt-0.5">
                    {data.runtime_status.traffic_metrics?.total_hits ?? 0}
                  </div>
                </div>
              </div>

              {/* Recent Error Logs */}
              {data.runtime_status.recent_errors && data.runtime_status.recent_errors.length > 0 ? (
                <div className="p-3 bg-slate-900 rounded-lg text-slate-300 text-[11px] font-mono space-y-1 max-h-32 overflow-y-auto">
                  <div className="text-[10px] font-bold text-rose-400 mb-1">{t('modals.healthCheck.recentErrorSummary')}</div>
                  {data.runtime_status.recent_errors.map((err, idx) => (
                    <div key={idx} className="leading-tight">
                      <span className="text-slate-500">[{err.time}]</span>{' '}
                      <span className="text-rose-400 font-bold">{err.level}:</span>{' '}
                      <span className="text-slate-200">{err.msg}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="p-2.5 bg-emerald-50/50 border border-emerald-200/60 rounded-lg text-[11px] text-emerald-800 flex items-center gap-1.5">
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                  <span>{t('modals.healthCheck.noRecentErrors')}</span>
                </div>
              )}
            </div>
          </>
        )}

        {/* Confirmation Modal */}
        {confirmItem && (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm p-4">
            <div className="bg-white rounded-xl shadow-2xl border border-slate-200 max-w-md w-full p-5 space-y-4">
              <div className="flex items-center gap-2.5 text-amber-600">
                <AlertTriangle className="w-5 h-5 shrink-0" />
                <h4 className="text-sm font-bold text-slate-900">{t('modals.healthCheck.confirmRepairTitle')}</h4>
              </div>
              <p className="text-xs text-slate-700 leading-relaxed">
                {t('modals.healthCheck.confirmRepairPrompt', { name: confirmItem.name })}
                <br />
                <span className="font-mono text-slate-500 text-[11px]">[{confirmItem.code}] {confirmItem.message}</span>
              </p>
              {confirmItem.requires_elevation && (
                <div className="p-2.5 bg-amber-50 border border-amber-200 rounded-lg text-[11px] text-amber-800 flex items-center gap-2">
                  <ShieldAlert className="w-4 h-4 shrink-0 text-amber-600" />
                  <span>{t('modals.healthCheck.confirmUacWarning')}</span>
                </div>
              )}
              <div className="flex items-center justify-end gap-2 pt-2 border-t border-slate-100">
                <Button
                  variant="desktop"
                  size="sm"
                  onClick={() => setConfirmItem(null)}
                  disabled={repairing}
                >
                  {t('common.cancel')}
                </Button>
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => executeRepair(confirmItem.code)}
                  disabled={repairing}
                  icon={<Wrench className="w-3.5 h-3.5" />}
                >
                  {t('modals.healthCheck.tryRepair')}
                </Button>
              </div>
            </div>
          </div>
        )}

        {/* Modal Footer Actions */}
        <div className="flex items-center justify-between pt-2 border-t border-slate-200">
          <div className="flex items-center gap-2">
            <Button
              variant="desktop"
              size="sm"
              onClick={() => {
                runHealthCheck()
                runPingProbe()
              }}
              disabled={loading || repairing}
              icon={<RefreshCw className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} />}
            >
              {t('modals.healthCheck.startCheck')}
            </Button>
            {hasRepairable && (
              <Button
                variant="primary"
                size="sm"
                onClick={handleBatchRepair}
                disabled={loading || repairing}
                icon={<Wrench className={`w-3.5 h-3.5 ${repairingCode === 'all' ? 'animate-spin' : ''}`} />}
              >
                {repairingCode === 'all' ? t('modals.healthCheck.repairing') : t('modals.healthCheck.tryRepair')}
              </Button>
            )}
          </div>

          <Button variant="desktop" size="sm" onClick={onClose} disabled={repairing}>
            {t('common.closeEsc')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
