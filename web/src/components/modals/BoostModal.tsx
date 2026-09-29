import React, { useState, useEffect } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Zap, CheckCircle2, AlertTriangle, ShieldAlert, XCircle, PauseCircle } from 'lucide-react'
import { startBoost, disableBoost, cancelCacheTask, fetchCacheTaskStatus } from '../../api'
import { CacheStats, BoostProgress } from '../../types'

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
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const [actionLoading, setActionLoading] = useState<boolean>(false)

  const fetchStatus = async () => {
    try {
      const data = await fetchCacheTaskStatus()
      setTaskStatus(data)
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
      setTaskStatus((prev: any) => ({
        ...prev,
        is_boosting: false,
        boost_progress: data,
      }))
      onRefresh()
    }

    const onBoostDisabled = () => {
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
  const boostProgress: BoostProgress | undefined = taskStatus?.boost_progress
  const boostState = isBoosting
    ? 'loading'
    : (boostProgress?.state || cacheStats?.boost_state || 'disabled')

  const handleStart = async () => {
    setActionLoading(true)
    try {
      await startBoost()
      onToast('RAM Boost 全量预载入已启动', 'info')
      await fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`启动失败: ${e.message}`, 'error')
    } finally {
      setActionLoading(false)
    }
  }

  const handleCancel = async () => {
    setActionLoading(true)
    try {
      await cancelCacheTask()
      onToast('已请求停止后续载入', 'info')
      await fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`停止失败: ${e.message}`, 'error')
    } finally {
      setActionLoading(false)
    }
  }

  const handleDisable = async () => {
    setActionLoading(true)
    try {
      await disableBoost()
      onToast('RAM Boost 已关闭，驻留内存已释放', 'success')
      await fetchStatus()
      onRefresh()
    } catch (e: any) {
      onToast(`关闭失败: ${e.message}`, 'error')
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
          <span>内存全量加速 (RAM Boost)</span>
        </div>
      }
      subtitle="把本地素材全量载入内存，减少游戏时的硬盘读写，提升响应速度。"
      maxWidth="max-w-lg"
    >
      <div className="space-y-4 text-xs text-slate-700">
        {/* Memory Budget & Resource Meters */}
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
          <div className="p-2.5 bg-slate-50 border border-slate-200/80 rounded-lg">
            <div className="text-[11px] text-slate-500 font-medium">常规内存缓存</div>
            <div className="text-sm font-bold text-slate-800 font-mono mt-0.5">{Math.round(ramMB)} MB</div>
          </div>
          <div className="p-2.5 bg-amber-50/70 border border-amber-200/80 rounded-lg">
            <div className="text-[11px] text-amber-700 font-medium">全量驻留内存</div>
            <div className="text-sm font-bold text-amber-900 font-mono mt-0.5">{Math.round(residentMB)} MB</div>
          </div>
          <div className="p-2.5 bg-slate-50 border border-slate-200/80 rounded-lg">
            <div className="text-[11px] text-slate-500 font-medium">加速器内存配额</div>
            <div className="text-sm font-bold text-slate-800 font-mono mt-0.5">
              {Math.round(totalRamMB)} / {ramMaxMB} MB
            </div>
          </div>
          <div className="p-2.5 bg-slate-50 border border-slate-200/80 rounded-lg">
            <div className="text-[11px] text-slate-500 font-medium">系统可用物理内存</div>
            <div className="text-sm font-bold text-slate-800 font-mono mt-0.5">
              {sysAvailMB > 0 ? (sysTotalMB > 0 ? `${(sysAvailMB / 1024).toFixed(1)} / ${(sysTotalMB / 1024).toFixed(1)} GB` : `${(sysAvailMB / 1024).toFixed(1)} GB`) : '检测中'}
            </div>
          </div>
        </div>

        {/* State: Disabled / Not running */}
        {boostState === 'disabled' && (
          <div className="space-y-3">
            <div className="p-3.5 bg-amber-50/50 border border-amber-200/60 rounded-xl space-y-2.5 leading-relaxed">
              <div className="font-bold text-amber-900 flex items-center gap-1.5">
                <span>预载入机制与安全保障：</span>
              </div>
              <p className="text-slate-600">
                将当前本地磁盘中已缓存的静态素材全量预载入内存，游戏读取静态资源时无需频繁访问硬盘。
              </p>
              <div className="text-[11px] text-slate-600 space-y-1.5">
                <div>• <strong>瞬时 I/O 告知：</strong>初次扫描与载入会有短暂的磁盘读取负载，载入完成后恢复</div>
                <div>• <strong>前台动态避让：</strong>检测到游戏战斗或 API 请求时自动挂起，绝不挤占游戏带宽</div>
                <div>• <strong>物理内存保护：</strong>系统可用物理内存低于 2 GB 时自动终止载入，防止宿主机卡顿</div>
                <div>• <strong>安全生命周期：</strong>可随时停止载入，或一键“关闭 Boost”完整释放驻留内存</div>
              </div>
            </div>

            <div className="flex items-center justify-end gap-2 pt-1">
              <Button variant="secondary" onClick={onClose} disabled={actionLoading}>
                取消
              </Button>
              <Button
                variant="primary"
                onClick={handleStart}
                disabled={actionLoading}
                className="bg-amber-600 hover:bg-amber-500 text-white font-medium"
              >
                {actionLoading ? '启动中...' : '开始全量载入'}
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
                <span>正在全量载入快照 ({percent}%)</span>
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
              <span>{loadedFiles.toLocaleString()} / {totalFiles.toLocaleString()} 文件</span>
              <span>{loadedMB} / {totalMB} MB</span>
            </div>

            {skippedFiles > 0 && (
              <div className="p-2 bg-amber-50/70 border border-amber-200/60 rounded-lg flex items-center gap-1.5 text-amber-800 text-[11px] font-medium">
                <AlertTriangle className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                <span>已跳过 {skippedFiles.toLocaleString()} 个损坏/无效文件</span>
              </div>
            )}

            {isPaused && (
              <div className="p-2 bg-amber-50 border border-amber-200 rounded-lg flex items-center gap-2 text-amber-800 text-[11px]">
                <PauseCircle className="w-4 h-4 text-amber-600 shrink-0" />
                <span>检测到前台网络或战斗 API 活动，后台载入已主动挂起避让...</span>
              </div>
            )}

            <div className="flex items-center justify-between pt-1 border-t border-slate-200">
              <span className="text-[11px] text-slate-500">当前正在读取的文件完成后安全停止</span>
              <Button
                variant="secondary"
                onClick={handleCancel}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? '停止中...' : '⏹ 停止后续载入'}
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
                <span>静态素材已全量驻留内存</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                共驻留 <strong>{residentItems.toLocaleString()}</strong> 个素材 · <strong>{Math.round(residentMB)} MB</strong>。
                后续静态请求直接由内存高速响应，无需读取本地磁盘。
              </p>
              {skippedFiles > 0 && (
                <div className="p-2 bg-amber-50/70 border border-amber-200/60 rounded-lg flex items-center gap-1.5 text-amber-800 text-[11px] font-medium">
                  <AlertTriangle className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                  <span>已跳过 {skippedFiles.toLocaleString()} 个损坏/无效文件（未载入内存）</span>
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
                <span>{actionLoading ? '关闭中...' : '关闭 Boost (释放驻留内存)'}</span>
              </Button>
              <Button variant="secondary" onClick={onClose}>
                完成
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
                <span>已达加速器设置的内存配额上限 ({ramMaxMB} MB)</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                已驻留 <strong>{residentItems.toLocaleString()}</strong> 个素材 · <strong>{Math.round(residentMB)} MB</strong>。
                超出配额的素材将继续由常规内存缓存或本地磁盘透明提供。如需载入更多素材，可先在上方调高“内存缓存上限”。
              </p>
              {skippedFiles > 0 && (
                <div className="p-2 bg-amber-100/70 border border-amber-300/60 rounded-lg flex items-center gap-1.5 text-amber-900 text-[11px] font-medium">
                  <AlertTriangle className="w-3.5 h-3.5 text-amber-600 shrink-0" />
                  <span>已跳过 {skippedFiles.toLocaleString()} 个损坏/无效文件（未载入内存）</span>
                </div>
              )}
              {unprewarmedMB > 0 && (
                <div className="text-[11px] text-amber-800 font-mono mt-1 font-medium">
                  超出配额未载入：{unprewarmedMB} MB
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
                {actionLoading ? '关闭中...' : '关闭 Boost'}
              </Button>
              <Button variant="secondary" onClick={onClose}>
                确定
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
                <span>已停止后续预载入</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                当前已载入 <strong>{residentItems.toLocaleString()}</strong> 个素材 · <strong>{Math.round(residentMB)} MB</strong>，已载入素材正常生效加速。点击“重新载入”将重新扫描本地缓存并载入。
              </p>
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button
                variant="secondary"
                onClick={handleDisable}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? '关闭中...' : '关闭 Boost'}
              </Button>
              <div className="flex items-center gap-2">
                <Button variant="secondary" onClick={onClose}>
                  确定
                </Button>
                <Button
                  variant="primary"
                  onClick={handleStart}
                  disabled={actionLoading}
                  className="bg-amber-600 hover:bg-amber-500 text-white"
                >
                  重新载入
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
                <span>触发系统可用物理内存熔断保护</span>
              </div>
              <p className="text-slate-600 leading-relaxed">
                系统可用物理内存低于 2 GB 安全阈值，为确保宿主机稳定流畅，已主动终止后续载入。
                当前已成功载入的 <strong>{residentItems.toLocaleString()}</strong> 个素材正常生效。
              </p>
            </div>

            <div className="flex items-center justify-between pt-1">
              <Button
                variant="secondary"
                onClick={handleDisable}
                disabled={actionLoading}
                className="text-rose-600 border-rose-200 hover:bg-rose-50"
              >
                {actionLoading ? '关闭中...' : '关闭 Boost'}
              </Button>
              <Button variant="secondary" onClick={onClose}>
                知道了
              </Button>
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
