import React, { useState, useEffect, useRef, useCallback } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Sparkles, CheckCircle2, RefreshCw, XCircle, FolderGit2, AlertTriangle } from 'lucide-react'
import {
  slimCache,
  fetchCacheTaskStatus,
  cancelCacheTask,
  fetchCacheBreakdown,
  startCacheBreakdownScan,
  formatBytesBinary,
} from '../../api'
import { BreakdownResponse, VersionRetentionPreview } from '../../types'

export interface SlimModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const SlimModal: React.FC<SlimModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
}) => {
  const [keepCount, setKeepCount] = useState<number>(8)
  const [running, setRunning] = useState(false)
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const [breakdown, setBreakdown] = useState<BreakdownResponse | null>(null)
  const [startingScan, setStartingScan] = useState(false)
  const pollTimerRef = useRef<any>(null)
  const prevBusyRef = useRef<boolean>(false)
  const keepCountRef = useRef<number>(8)
  const breakdownReqSeqRef = useRef<number>(0)
  const statusReqSeqRef = useRef<number>(0)
  const statusInFlightCountRef = useRef<number>(0)
  const breakdownScanningRef = useRef<boolean>(false)

  const loadBreakdown = useCallback(async (k: number) => {
    const reqSeq = ++breakdownReqSeqRef.current
    try {
      const data = await fetchCacheBreakdown(k)
      if (reqSeq !== breakdownReqSeqRef.current) return
      breakdownScanningRef.current = Boolean(data?.is_scanning)
      setBreakdown(data)
    } catch {
      // Ignore transient poll errors
    }
  }, [])

  const fetchStatus = useCallback(async () => {
    const reqSeq = ++statusReqSeqRef.current
    statusInFlightCountRef.current++
    try {
      const data = await fetchCacheTaskStatus()
      if (reqSeq !== statusReqSeqRef.current) return
      setTaskStatus(data)
      const busy = Boolean(
        data?.is_slimming ||
          data?.is_scanning ||
          data?.is_invalidating ||
          data?.is_clearing_category ||
          data?.is_auditing
      )
      setRunning(Boolean(data?.is_slimming))
      if ((prevBusyRef.current && !busy) || (!data?.is_scanning && breakdownScanningRef.current)) {
        breakdownScanningRef.current = false
        loadBreakdown(keepCountRef.current)
        onRefresh()
      }
      prevBusyRef.current = busy
    } catch {
      // Ignore transient poll errors
    } finally {
      statusInFlightCountRef.current = Math.max(0, statusInFlightCountRef.current - 1)
    }
  }, [loadBreakdown, onRefresh])

  useEffect(() => {
    if (isOpen) {
      loadBreakdown(keepCountRef.current)
      fetchStatus()
      pollTimerRef.current = setInterval(() => {
        if (statusInFlightCountRef.current === 0) {
          fetchStatus()
        }
      }, 1000)
    } else {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current)
    }
    return () => {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current)
    }
  }, [isOpen, loadBreakdown, fetchStatus])

  const handleSelectKeep = (num: number) => {
    keepCountRef.current = num
    setKeepCount(num)
    loadBreakdown(num)
  }

  const handleStartScan = async () => {
    setStartingScan(true)
    try {
      await startCacheBreakdownScan()
      prevBusyRef.current = true
      onToast('已启动后台分类扫描以生成版本保留预览', 'info')
      await fetchStatus()
      await loadBreakdown(keepCountRef.current)
    } catch (e: any) {
      onToast(`启动扫描失败: ${e.message}`, 'error')
    } finally {
      setStartingScan(false)
    }
  }

  const handleStartSlim = async () => {
    setRunning(true)
    try {
      const validSnapId =
        breakdown?.snapshot && breakdown.snapshot.valid && !breakdown.snapshot.stale
          ? breakdown.snapshot.snapshot_id
          : undefined
      await slimCache(keepCount, validSnapId)
      prevBusyRef.current = true
      onToast(`版本清理任务已在后台启动（每组保留最新 ${keepCount} 个版本）`, 'info')
      await fetchStatus()
      await loadBreakdown(keepCountRef.current)
      onRefresh()
    } catch (e: any) {
      onToast(`版本清理启动失败: ${e.message}`, 'error')
      setRunning(false)
    }
  }

  const handleCancelTask = async () => {
    try {
      const activeId = taskStatus?.active_task_id || undefined
      await cancelCacheTask(activeId)
      onToast('已请求取消当前后台缓存任务', 'info')
      await fetchStatus()
      await loadBreakdown(keepCountRef.current)
    } catch (e: any) {
      onToast(`取消任务失败: ${e.message}`, 'error')
    }
  }

  const isSlimming = Boolean(taskStatus?.is_slimming)
  const isScanning =
    taskStatus !== null ? Boolean(taskStatus?.is_scanning) : Boolean(breakdown?.is_scanning)
  const isSubmittingAction = startingScan || running
  const hasOtherTask = Boolean(
    taskStatus?.active_task_id &&
      !isSlimming &&
      !isScanning
  )
  const slimProgress = taskStatus?.slim_progress
  const scanProgress = taskStatus?.scan_progress || breakdown?.scan_progress

  const snapshot = breakdown?.snapshot
  const preview: VersionRetentionPreview | undefined =
    snapshot?.retention_previews?.[String(keepCount)]

  const scanOutcomeStatus = !isScanning
    ? scanProgress?.status === 'failed' || scanProgress?.status === 'cancelled'
      ? scanProgress.status
      : snapshot?.status === 'failed' || snapshot?.status === 'cancelled'
        ? snapshot.status
        : null
    : null
  const scanOutcomeMessage = scanProgress?.message || snapshot?.error

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Sparkles className="w-4 h-4 text-blue-600" />
          <span>历史版本目录保留与清理</span>
        </div>
      }
      subtitle="按 namespace × 资源前缀分组保留最新 K 个版本目录，清理旧版本文件与配套元数据"
      maxWidth="max-w-xl"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-1.5 leading-relaxed">
          <div className="font-bold text-slate-900">版本目录清理机制：</div>
          <p className="text-slate-600">
            按 <code className="font-mono text-slate-800">namespace × 资源前缀 (assets / assets_en / assets_jp)</code> 独立分组，仅识别合法正整数版本目录并按数值排序，每组保留最新 <strong>{keepCount}</strong> 个版本目录。
          </p>
          <div className="text-[11px] text-slate-500">
            • 非版本目录下的常驻 UI、剧情、战斗动画与音频不在此操作范围内；删除后若客户端再次请求旧版本资源，代理将按正常流程从上游获取。
          </div>
        </div>

        {/* Keep Selector: 2 / 4 / 8 */}
        <div className="flex items-center justify-between p-3 bg-slate-50 border border-slate-200 rounded-lg flex-wrap gap-2">
          <span className="font-semibold text-slate-800">每组保留最新版本目录数 (K)：</span>
          <div className="flex items-center gap-2">
            {[2, 4, 8].map((num) => (
              <button
                key={num}
                type="button"
                disabled={isSlimming || isSubmittingAction}
                onClick={() => handleSelectKeep(num)}
                className={`px-3 py-1 rounded text-xs font-mono border transition-colors cursor-pointer ${
                  keepCount === num
                    ? 'bg-blue-600 text-white border-blue-600 font-bold'
                    : 'bg-white text-slate-700 border-slate-300 hover:bg-slate-100'
                } ${isSlimming || isSubmittingAction ? 'opacity-50 cursor-not-allowed' : ''}`}
              >
                保留 {num} 个{num === 8 ? ' (默认)' : ''}
              </button>
            ))}
          </div>
        </div>

        {/* Snapshot & Retention Preview Section */}
        <div className="p-3 bg-white border border-slate-200 rounded-lg space-y-2.5">
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <div className="flex items-center gap-1.5 font-semibold text-slate-800">
              <FolderGit2 className="w-3.5 h-3.5 text-sky-600" />
              <span>版本目录保留预览 (K = {keepCount})</span>
            </div>
            <div className="flex items-center gap-2">
              {snapshot?.stale && (
                <span className="inline-flex items-center gap-1 text-[11px] text-amber-700 bg-amber-50 border border-amber-200 px-2 py-0.5 rounded">
                  <AlertTriangle className="w-3 h-3" />
                  快照已过期
                </span>
              )}
              {isScanning ? (
                <Button
                  variant="desktop"
                  size="xs"
                  onClick={handleCancelTask}
                  icon={<XCircle className="w-3 h-3 text-red-600" />}
                >
                  取消扫描 ({scanProgress?.scanned_files ?? 0} 项)
                </Button>
              ) : (
                <Button
                  variant="desktop"
                  size="xs"
                  loading={startingScan}
                  disabled={isSlimming || hasOtherTask || isSubmittingAction}
                  onClick={handleStartScan}
                >
                  {snapshot ? '重新扫描预览' : '扫描生成预览'}
                </Button>
              )}
            </div>
          </div>

          {scanOutcomeStatus && (
            <div
              className={`p-2 border rounded text-[11px] flex items-center justify-between flex-wrap gap-2 ${
                scanOutcomeStatus === 'failed'
                  ? 'bg-red-50/90 border-red-200 text-red-900'
                  : 'bg-amber-50/90 border-amber-200 text-amber-900'
              }`}
            >
              <div className="font-medium flex items-center gap-1.5">
                <AlertTriangle
                  className={`w-3.5 h-3.5 shrink-0 ${
                    scanOutcomeStatus === 'failed' ? 'text-red-600' : 'text-amber-600'
                  }`}
                />
                <span>
                  {scanOutcomeStatus === 'failed' ? '分类扫描失败：' : '分类扫描已取消：'}
                  {scanOutcomeMessage || (scanOutcomeStatus === 'failed' ? '分类扫描未完成' : '任务已取消')}
                </span>
              </div>
            </div>
          )}

          {!snapshot ? (
            <div className="text-[11px] text-slate-500 py-2">
              当前尚无分类扫描快照。可点击右上角“扫描生成预览”查看各分组将保留与清理的版本目录及预计删除字节数，也可直接执行版本清理。
            </div>
          ) : preview ? (
            <div className="space-y-2.5">
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 bg-slate-50 p-2.5 rounded border border-slate-200/80 text-[11px]">
                <div>
                  <div className="text-slate-500">版本分组 / 总目录</div>
                  <div className="font-mono font-bold text-slate-800 mt-0.5">
                    {preview.total_groups} 组 / {preview.total_version_dirs} 个
                  </div>
                </div>
                <div>
                  <div className="text-slate-500">待清理版本目录</div>
                  <div className="font-mono font-bold text-amber-700 mt-0.5">
                    {preview.prune_dirs} 个目录
                  </div>
                </div>
                <div>
                  <div className="text-slate-500">待删素材体积</div>
                  <div className="font-mono font-bold text-slate-800 mt-0.5">
                    {formatBytesBinary(preview.prune_asset_bytes)} ({preview.prune_asset_files} 项)
                  </div>
                </div>
                <div>
                  <div className="text-slate-500">预计删除字节数 (含 .ext)</div>
                  <div className="font-mono font-bold text-blue-700 mt-0.5">
                    {formatBytesBinary(preview.estimated_delete_bytes)}
                  </div>
                </div>
              </div>

              {preview.groups && preview.groups.length > 0 ? (
                <div className="max-h-44 overflow-y-auto space-y-2 pr-1">
                  {preview.groups.map((g) => (
                    <div
                      key={g.group_key}
                      className="p-2 rounded border border-slate-200 bg-slate-50/60 space-y-1 text-[11px]"
                    >
                      <div className="flex items-center justify-between font-mono">
                        <span className="font-semibold text-slate-800">
                          {g.namespace} / {g.resource_prefix}
                        </span>
                        <span className="text-slate-600">
                          共 {g.total_dirs} 个版本 · 待清理 <strong>{g.prune_dirs}</strong> 个 · 预计删除{' '}
                          <strong>{formatBytesBinary(g.estimated_delete_bytes)}</strong>
                        </span>
                      </div>
                      <div className="text-slate-600 break-all">
                        <span className="text-emerald-700 font-medium">保留 ({g.kept_versions.length})：</span>
                        <span className="font-mono">
                          {g.kept_versions.length > 0 ? g.kept_versions.join(', ') : '无'}
                        </span>
                      </div>
                      {g.prune_versions.length > 0 && (
                        <div className="text-slate-600 break-all">
                          <span className="text-amber-700 font-medium">待清理 ({g.prune_versions.length})：</span>
                          <span className="font-mono">{g.prune_versions.join(', ')}</span>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              ) : (
                <div className="text-[11px] text-slate-500">
                  当前缓存目录中未发现带有版本号的资源目录。
                </div>
              )}
            </div>
          ) : null}
        </div>

        {/* Realtime Slim Progress */}
        {isSlimming && (
          <div className="p-3 bg-blue-50 border border-blue-200 rounded-lg text-blue-900 space-y-2">
            <div className="font-bold flex items-center justify-between">
              <span className="flex items-center gap-1.5">
                <RefreshCw className="w-3.5 h-3.5 animate-spin text-blue-600" />
                <span>正在清理历史版本目录...</span>
              </span>
              <Button
                variant="desktop"
                size="xs"
                onClick={handleCancelTask}
                icon={<XCircle className="w-3 h-3 text-red-600" />}
              >
                终止清理
              </Button>
            </div>
            <div className="grid grid-cols-2 gap-2 text-[11px] font-mono text-blue-800 pt-1">
              <div>
                目录进度: <strong>{slimProgress?.current_idx ?? 0} / {slimProgress?.total_dirs ?? 0}</strong>
              </div>
              <div>
                已删文件 (含 .ext): <strong>{slimProgress?.deleted_files ?? 0} 个</strong>
              </div>
              <div className="col-span-2">
                已删除字节数: <strong>{formatBytesBinary(slimProgress?.freed_bytes ?? 0)}</strong>
              </div>
            </div>
          </div>
        )}

        {!isSlimming && taskStatus?.last_slim_result && (
          <div
            className={`p-3 rounded-lg space-y-1 border ${
              taskStatus.last_slim_result.status === 'failed'
                ? 'bg-red-50 border-red-200 text-red-800'
                : taskStatus.last_slim_result.status === 'partial_failed' ||
                    taskStatus.last_slim_result.cancelled
                  ? 'bg-amber-50 border-amber-200 text-amber-800'
                  : 'bg-emerald-50 border-emerald-200 text-emerald-800'
            }`}
          >
            <div className="font-bold flex items-center gap-1.5">
              {taskStatus.last_slim_result.status === 'failed' ||
              taskStatus.last_slim_result.status === 'partial_failed' ? (
                <AlertTriangle className="w-4 h-4 shrink-0" />
              ) : (
                <CheckCircle2 className="w-4 h-4 shrink-0" />
              )}
              <span>
                {taskStatus.last_slim_result.status === 'failed'
                  ? '最近一次版本清理失败'
                  : taskStatus.last_slim_result.status === 'partial_failed'
                    ? '最近一次版本清理部分完成（含失败项）'
                    : taskStatus.last_slim_result.cancelled
                      ? '最近一次版本清理已部分完成 (用户取消)'
                      : '最近一次版本清理完成'}
              </span>
            </div>
            {taskStatus.last_slim_result.message && (
              <div className="text-[11px]">{taskStatus.last_slim_result.message}</div>
            )}
            <div className="text-[11px] font-mono flex flex-wrap gap-x-3 gap-y-1">
              <span>清理版本目录: {taskStatus.last_slim_result.deleted_dirs ?? 0} 个</span>
              <span>
                删除数据文件: {taskStatus.last_slim_result.deleted_asset_files ?? taskStatus.last_slim_result.deleted_files ?? 0} 个
              </span>
              {typeof taskStatus.last_slim_result.deleted_meta_files === 'number' && (
                <span>删除 .ext: {taskStatus.last_slim_result.deleted_meta_files} 个</span>
              )}
              <span>实际删除字节数: {formatBytesBinary(taskStatus.last_slim_result.freed_bytes ?? 0)}</span>
              {(taskStatus.last_slim_result.skipped_changed_count ?? 0) > 0 && (
                <span>变化跳过: {taskStatus.last_slim_result.skipped_changed_count} 个</span>
              )}
              {(taskStatus.last_slim_result.failed_count ?? 0) > 0 && (
                <span className="text-red-600">失败: {taskStatus.last_slim_result.failed_count} 个</span>
              )}
            </div>
          </div>
        )}

        <div className="flex items-center justify-between pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>

          {isSlimming ? (
            <Button
              variant="desktop"
              size="sm"
              onClick={handleCancelTask}
              icon={<XCircle className="w-3.5 h-3.5 text-red-600" />}
            >
              终止清理
            </Button>
          ) : (
            <Button
              variant="primary"
              size="sm"
              loading={running}
              disabled={isScanning || hasOtherTask || isSubmittingAction}
              onClick={handleStartSlim}
            >
              {isScanning
                ? '分类扫描执行中...'
                : hasOtherTask
                  ? '其他缓存任务执行中...'
                  : `清理旧版本 (每组保留 ${keepCount} 个)`}
            </Button>
          )}
        </div>
      </div>
    </Modal>
  )
}
