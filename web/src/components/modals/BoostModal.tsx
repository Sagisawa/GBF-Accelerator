import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Zap, CheckCircle2, AlertTriangle, ShieldAlert, XCircle, PauseCircle } from 'lucide-react'
import { startBoost, disableBoost, cancelCacheTask, fetchCacheTaskStatus } from '../../api'
import { CacheStats, BoostProgress } from '../../types'
import { useI18n } from '../../i18n'

export interface BoostModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
  cacheStats: CacheStats | null
}

export const BoostModal: React.FC<BoostModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
  cacheStats,
}) => {
  const { t } = useI18n()
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const [actionLoading, setActionLoading] = useState<boolean>(false)
  const boostTaskIdRef = useRef<string | null>(null)

  const fetchStatus = async () => {
    try {
      const data = await fetchCacheTaskStatus()
      setTaskStatus(data)
      if (data?.is_boosting && data?.active_task_id) {
        boostTaskIdRef.current = String(data.active_task_id)
      } else if (!data?.is_boosting) {
        boostTaskIdRef.current = null
      }
    } catch {}
  }

  useEffect(() => {
    if (!isOpen) return

    fetchStatus()

    const onBoostProgress = (e: Event) => {
      const data = (e as CustomEvent).detail
      if (!data) return
      setTaskStatus((prev: any) => ({
        ...prev,
        is_boosting: data.state === 'loading',
        boost_progress: data,
      }))
    }

    const onBoostDone = (e: Event) => {
      const data = (e as CustomEvent).detail
      boostTaskIdRef.current = null
      setTaskStatus((prev: any) => ({
        ...prev,
        is_boosting: false,
        boost_progress: data,
      }))
      onRefresh()
    }

    const onBoostDisabled = () => {
      boostTaskIdRef.current = null
      setTaskStatus((prev: any) => ({
        ...prev,
        is_boosting: false,
        boost_progress: undefined,
      }))
      onRefresh()
    }

    window.addEventListener('gbf-boost-progress', onBoostProgress)
    window.addEventListener('gbf-boost-done', onBoostDone)
    window.addEventListener('gbf-boost-disabled', onBoostDisabled)

    // Slow fallback polling (10s) in case SSE disconnects
    const fallbackTimer = setInterval(fetchStatus, 10000)

    return () => {
      window.removeEventListener('gbf-boost-progress', onBoostProgress)
      window.removeEventListener('gbf-boost-done', onBoostDone)
      window.removeEventListener('gbf-boost-disabled', onBoostDisabled)
      clearInterval(fallbackTimer)
    }
  }, [isOpen, onRefresh])

  const isBoosting = Boolean(taskStatus?.is_boosting)
  const isOtherTaskActive =
    !isBoosting &&
    Boolean(
      taskStatus?.active_task_id ||
        taskStatus?.is_auditing ||
        taskStatus?.is_slimming ||
        taskStatus?.is_scanning ||
        taskStatus?.is_clearing_category
    )
  const boostProgress: BoostProgress | undefined = taskStatus?.boost_progress
  const boostState = isBoosting
    ? 'loading'
    : (boostProgress?.state || cacheStats?.boost_state || 'disabled')

  const handleStart = async () => {
    setActionLoading(true)
    try {
      const res = await startBoost()
      if (res?.task_id) {
        boostTaskIdRef.current = String(res.task_id)
      }
      onToast(t('modals.boost.boostRunning'), 'info')
      await fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(t('toasts.startFailed', { error: e.message }), 'error')
    } finally {
      setActionLoading(false)
    }
  }

  const handleCancel = async () => {
    setActionLoading(true)
    try {
      const targetTaskId =
        (taskStatus?.is_boosting && taskStatus?.active_task_id
          ? String(taskStatus.active_task_id)
          : '') ||
        boostTaskIdRef.current ||
        ''
      if (!targetTaskId) {
        await fetchStatus()
        return
      }
      await cancelCacheTask(targetTaskId)
      onToast(t('modals.boost.stopLoading'), 'info')
      await fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`${t('common.failed')}: ${e.message}`, 'error')
    } finally {
      setActionLoading(false)
    }
  }

  const handleDisable = async () => {
    setActionLoading(true)
    try {
      await disableBoost()
      boostTaskIdRef.current = null
      onToast(t('modals.boost.disableBoost'), 'success')
      await fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`${t('common.failed')}: ${e.message}`, 'error')
    } finally {
      setActionLoading(false)
    }
  }

  const residentMB = cacheStats?.resident_mb ?? 0
  const residentItems = cacheStats?.resident_items ?? 0
  const ramMaxMB = cacheStats?.ram_max_mb ?? 256
  const ramMB = cacheStats?.ram_mb ?? 0
  const totalRamMB = cacheStats?.total_ram_mb ?? (ramMB + residentMB)
  const sysAvailMB = cacheStats?.system_avail_mb ?? 0
  const sysTotalMB = cacheStats?.system_total_mb ?? 0

  const loadedFiles = boostProgress?.loaded_files ?? residentItems
  const totalFiles = boostProgress?.total_files ?? loadedFiles
  const skippedFiles = boostProgress?.skipped_files ?? 0
  const loadedMB = Math.round((boostProgress?.loaded_bytes ?? 0) / (1024 * 1024) * 10) / 10
  const totalMB = Math.round((boostProgress?.total_bytes ?? 0) / (1024 * 1024) * 10) / 10
  const speedMB = Math.round((boostProgress?.speed_bytes_sec ?? 0) / (1024 * 1024) * 10) / 10
  const percent = totalFiles > 0 ? Math.min(100, Math.round((loadedFiles / totalFiles) * 100)) : 0
  const isPaused = Boolean(boostProgress?.is_paused_by_fg)
  const unprewarmedMB = totalMB > loadedMB ? Math.round((totalMB - loadedMB) * 10) / 10 : 0

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Zap className="w-4 h-4 text-amber-500 fill-amber-500" />
          <span>{t('modals.boost.title')}</span>
        </div>
      }
      subtitle={t('modals.boost.subtitle')}
      maxWidth="max-w-lg"
    >
      <div className="space-y-4 text-xs text-slate-700">
        {/* Memory Budget & Resource Meters */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          <div className="p-2.5 bg-slate-50 border border-slate-200/80 rounded-lg">
            <div className="text-[11px] text-slate-500 font-medium">{t('modals.boost.ramCache')}</div>
            <div className="text-sm font-bold text-slate-800 font-mono mt-0.5">{Math.round(ramMB)} MB</div>
          </div>
          <div className="p-2.5 bg-amber-50/70 border border-amber-200/80 rounded-lg">
            <div className="text-[11px] text-amber-700 font-medium">{t('modals.boost.residentMemory')}</div>
            <div className="text-sm font-bold text-amber-900 font-mono mt-0.5">{Math.round(residentMB)} MB</div>
          </div>
          <div className="p-2.5 bg-slate-50 border border-slate-200/80 rounded-lg">
            <div className="text-[11px] text-slate-500 font-medium">{t('modals.boost.acceleratorQuota')}</div>
            <div className="text-sm font-bold text-slate-800 font-mono mt-0.5">
              {Math.round(totalRamMB)} / {ramMaxMB} MB
            </div>
          </div>
          <div className="p-2.5 bg-slate-50 border border-slate-200/80 rounded-lg">
            <div className="text-[11px] text-slate-500 font-medium">{t('modals.boost.sysAvail')}</div>
            <div className="text-sm font-bold text-slate-800 font-mono mt-0.5">
              {sysAvailMB > 0 ? (sysTotalMB > 0 ? `${(sysAvailMB / 1024).toFixed(1)} / ${(sysTotalMB / 1024).toFixed(1)} GB` : `${(sysAvailMB / 1024).toFixed(1)} GB`) : t('common.detecting')}
            </div>
          </div>
        </div>

        {/* State: Disabled / Not running */}
        {boostState === 'disabled' && (
          <div className="space-y-3">
            <div className="p-3.5 bg-amber-50/50 border border-amber-200/60 rounded-xl space-y-2.5 leading-relaxed">
              <div className="font-bold text-amber-900 flex items-center gap-1.5">
                <span>{t('modals.boost.mechanismLead')}</span>
              </div>
              <p className="text-slate-600">
                {t('modals.boost.mechanismDesc')}
              </p>
              <div className="text-[11px] text-slate-600 space-y-1.5">
                <div>{t('modals.boost.mechanismB1')}</div>
                <div>{t('modals.boost.mechanismB2')}</div>
                <div>{t('modals.boost.mechanismB3')}</div>
                <div>{t('modals.boost.mechanismB4')}</div>
              </div>
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button variant="secondary" onClick={onClose} disabled={actionLoading}>
                {t('common.cancel')}
              </Button>
              <Button
                variant="primary"
                onClick={handleStart}
                disabled={actionLoading || isOtherTaskActive}
                className="bg-amber-600 hover:bg-amber-500 text-white font-medium"
              >
                {actionLoading
                  ? t('common.loading')
                  : isOtherTaskActive
                    ? t('modals.boost.otherTaskActive')
                    : t('modals.boost.startBoost')}
              </Button>
            </div>
          </div>
        )}

        {/* State: Loading */}
        {boostState === 'loading' && (
          <div className="space-y-3.5 p-3.5 bg-slate-50 border border-slate-200 rounded-xl">
            <div className="flex items-center justify-between">
              <div className="font-bold text-slate-900 flex items-center gap-2">
                <span className="animate-spin text-base">⏳</span>
                <span>{t('modals.boost.loadingTitle')} ({percent}%)</span>
              </div>
              {speedMB > 0 && (
                <span className="font-mono text-xs text-slate-500">{speedMB} MB/s</span>
              )}
            </div>

            {/* Progress bar */}
            <div className="w-full bg-slate-200 rounded-full h-2.5 overflow-hidden">
              <div
                className="bg-amber-500 h-2.5 rounded-full transition-all duration-300"
                style={{ width: `${percent}%` }}
              />
            </div>

            <div className="flex items-center justify-between text-[11px] text-slate-600 font-mono">
              <span>{loadedFiles.toLocaleString()} / {totalFiles.toLocaleString()}</span>
              <span>{loadedMB} / {totalMB} MB</span>
            </div>

            {skippedFiles > 0 && (
              <div className="p-2 bg-amber-50/70 border border-amber-200/60 rounded-lg flex items-center gap-1.5 text-amber-800 text-[11px] font-medium">
                <AlertTriangle className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                <span>{skippedFiles.toLocaleString()} skipped</span>
              </div>
            )}

            {isPaused && (
              <div className="p-2 bg-amber-50 border border-amber-200 rounded-lg flex items-center gap-2 text-amber-800 text-[11px]">
                <PauseCircle className="w-4 h-4 text-amber-600 shrink-0" />
                <span>{t('modals.boost.pausedByFg')}</span>
              </div>
            )}

            <div className="flex items-center justify-between pt-1 border-t border-slate-200">
              <span className="text-[11px] text-slate-500"></span>
              <Button
                variant="secondary"
                onClick={handleCancel}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? t('common.loading') : `⏹ ${t('modals.boost.stopLoadingPending')}`}
              </Button>
            </div>
          </div>
        )}

        {/* State: Completed */}
        {boostState === 'completed' && (
          <div className="space-y-3.5">
            <div className="p-3.5 bg-emerald-50/70 border border-emerald-200/80 rounded-xl space-y-2">
              <div className="font-bold text-emerald-900 flex items-center gap-2">
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                <span>{t('modals.boost.completedTitle')}</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                {t('modals.boost.completedDesc', { count: residentItems.toLocaleString(), mb: Math.round(residentMB) })}
              </p>
              {skippedFiles > 0 && (
                <div className="p-2 bg-amber-50/70 border border-amber-200/60 rounded-lg flex items-center gap-1.5 text-amber-800 text-[11px] font-medium">
                  <AlertTriangle className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                  <span>{skippedFiles.toLocaleString()} skipped</span>
                </div>
              )}
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button
                variant="secondary"
                onClick={handleDisable}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50 flex items-center gap-1.5"
              >
                <XCircle className="w-3.5 h-3.5" />
                <span>{actionLoading ? t('common.loading') : t('modals.boost.disableBoost')}</span>
              </Button>
              <Button variant="secondary" onClick={onClose}>
                {t('common.ok')}
              </Button>
            </div>
          </div>
        )}

        {/* State: Partial */}
        {boostState === 'partial' && (
          <div className="space-y-3.5">
            <div className="p-3.5 bg-amber-50/80 border border-amber-200 rounded-xl space-y-2">
              <div className="font-bold text-amber-900 flex items-center gap-2">
                <AlertTriangle className="w-4 h-4 text-amber-600" />
                <span>{t('modals.boost.acceleratorQuota')} ({ramMaxMB} MB)</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                {t('modals.boost.completedDesc', { count: residentItems.toLocaleString(), mb: Math.round(residentMB) })}
              </p>
              {skippedFiles > 0 && (
                <div className="p-2 bg-amber-100/70 border border-amber-300/60 rounded-lg flex items-center gap-1.5 text-amber-900 text-[11px] font-medium">
                  <AlertTriangle className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                  <span>{skippedFiles.toLocaleString()} skipped</span>
                </div>
              )}
              {unprewarmedMB > 0 && (
                <div className="text-[11px] text-amber-800 font-mono mt-1 font-medium">
                  {unprewarmedMB} MB
                </div>
              )}
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button
                variant="secondary"
                onClick={handleDisable}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? t('common.loading') : t('modals.boost.disableBoost')}
              </Button>
              <Button variant="secondary" onClick={onClose}>
                {t('common.ok')}
              </Button>
            </div>
          </div>
        )}

        {/* State: Cancelled */}
        {boostState === 'cancelled' && (
          <div className="space-y-3.5">
            <div className="p-3.5 bg-slate-100 border border-slate-200 rounded-xl space-y-2">
              <div className="font-bold text-slate-800 flex items-center gap-2">
                <span>⏹</span>
                <span>{t('modals.boost.stopLoading')}</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                {t('modals.boost.completedDesc', { count: residentItems.toLocaleString(), mb: Math.round(residentMB) })}
              </p>
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button
                variant="secondary"
                onClick={handleDisable}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? t('common.loading') : t('modals.boost.disableBoost')}
              </Button>
              <div className="flex items-center gap-2">
                <Button variant="secondary" onClick={onClose}>
                  {t('common.ok')}
                </Button>
                <Button
                  variant="primary"
                  onClick={handleStart}
                  disabled={actionLoading || isOtherTaskActive}
                  className="bg-amber-600 hover:bg-amber-500 text-white"
                >
                  {isOtherTaskActive ? t('modals.boost.otherTaskActive') : t('modals.boost.startBoost')}
                </Button>
              </div>
            </div>
          </div>
        )}

        {/* State: Memory Guard Stopped */}
        {boostState === 'memory_guard_stopped' && (
          <div className="space-y-3.5">
            <div className="p-3.5 bg-rose-50/70 border border-rose-200 rounded-xl space-y-2">
              <div className="font-bold text-rose-900 flex items-center gap-2">
                <ShieldAlert className="w-4 h-4 text-rose-600" />
                <span>{t('modals.boost.sysAvail')}</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                {t('modals.boost.completedDesc', { count: residentItems.toLocaleString(), mb: Math.round(residentMB) })}
              </p>
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button
                variant="secondary"
                onClick={handleDisable}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? t('common.loading') : t('modals.boost.disableBoost')}
              </Button>
              <Button variant="secondary" onClick={onClose}>
                {t('common.ok')}
              </Button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
