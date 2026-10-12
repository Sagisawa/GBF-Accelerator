import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import {
  Trash2,
  Cpu,
  HardDrive,
  RefreshCw,
  XCircle,
  CheckCircle2,
  AlertTriangle,
  Layers,
} from 'lucide-react'
import {
  clearCache,
  slimCache,
  fetchCacheBreakdown,
  startCacheBreakdownScan,
  clearCacheCategories,
  fetchCacheTaskStatus,
  cancelCacheTask,
  formatBytesBinary,
} from '../../api'
import {
  BreakdownResponse,
  CategoryStat,
  DeleteResult,
  CleanupProgress,
  VersionRetentionPreview,
} from '../../types'

export interface ClearCacheModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

const CATEGORY_COLORS: Record<string, string> = {
  versioned: 'bg-sky-500',
  battle_cjs: 'bg-indigo-500',
  story: 'bg-amber-500',
  core_ui: 'bg-emerald-500',
  event_gacha: 'bg-rose-500',
  audio: 'bg-purple-500',
  other: 'bg-slate-400',
}

const DEFAULT_CAUTION_CATEGORIES = new Set(['battle_cjs', 'core_ui', 'other'])

const CATEGORY_SORT_ORDER: Record<string, number> = {
  versioned: 0,
  story: 1,
  event_gacha: 2,
  audio: 3,
  battle_cjs: 4,
  core_ui: 5,
  other: 6,
}

function sortCategoriesWithCautionLast(categories: CategoryStat[]): CategoryStat[] {
  return [...categories].sort((a, b) => {
    const aCaution = DEFAULT_CAUTION_CATEGORIES.has(a.category) ? 1 : 0
    const bCaution = DEFAULT_CAUTION_CATEGORIES.has(b.category) ? 1 : 0
    if (aCaution !== bCaution) return aCaution - bCaution
    const aOrder = CATEGORY_SORT_ORDER[a.category] ?? 99
    const bOrder = CATEGORY_SORT_ORDER[b.category] ?? 99
    return aOrder - bOrder
  })
}

function formatTimestamp(ms?: number): string {
  if (!ms || ms <= 0) return '--'
  try {
    return new Date(ms).toLocaleString('zh-CN', { hour12: false })
  } catch {
    return '--'
  }
}

export const ClearCacheModal: React.FC<ClearCacheModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
}) => {
  const [activeTab, setActiveTab] = useState<'breakdown' | 'reset'>('breakdown')

  // Breakdown & Task State
  const [breakdown, setBreakdown] = useState<BreakdownResponse | null>(null)
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const [startingScan, setStartingScan] = useState(false)

  // Category & Version Cleanup Selection State
  const [selectedCategories, setSelectedCategories] = useState<string[]>([])
  const [selectedNamespaces, setSelectedNamespaces] = useState<string[]>(['gbf'])
  const [confirmingCategoryClean, setConfirmingCategoryClean] = useState(false)
  const [submittingCategoryClean, setSubmittingCategoryClean] = useState(false)

  // RAM / Full Reset State
  const [busyRam, setBusyRam] = useState(false)
  const [busyAll, setBusyAll] = useState(false)
  const [confirmingFullClear, setConfirmingFullClear] = useState(false)

  const pollTimerRef = useRef<any>(null)
  const prevBusyRef = useRef<boolean>(false)
  const prevSnapshotIdRef = useRef<string | null>(null)
  const breakdownReqSeqRef = useRef<number>(0)
  const statusReqSeqRef = useRef<number>(0)
  const statusInFlightCountRef = useRef<number>(0)
  const breakdownScanningRef = useRef<boolean>(false)

  const loadBreakdown = useCallback(async () => {
    const reqSeq = ++breakdownReqSeqRef.current
    try {
      const data = await fetchCacheBreakdown()
      if (reqSeq !== breakdownReqSeqRef.current) return
      breakdownScanningRef.current = Boolean(data?.is_scanning)
      setBreakdown(data)
    } catch {
      // Ignore transient poll errors
    }
  }, [])

  const loadTaskStatus = useCallback(async () => {
    const reqSeq = ++statusReqSeqRef.current
    statusInFlightCountRef.current++
    try {
      const data = await fetchCacheTaskStatus()
      if (reqSeq !== statusReqSeqRef.current) return
      setTaskStatus(data)
      const busy = Boolean(
        data?.is_scanning ||
          data?.is_clearing_category ||
          data?.is_slimming ||
          data?.is_auditing
      )
      if ((prevBusyRef.current && !busy) || (!data?.is_scanning && breakdownScanningRef.current)) {
        breakdownScanningRef.current = false
        loadBreakdown()
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
      setConfirmingCategoryClean(false)
      setConfirmingFullClear(false)
      loadBreakdown()
      loadTaskStatus()
      pollTimerRef.current = setInterval(() => {
        if (statusInFlightCountRef.current === 0) {
          loadTaskStatus()
        }
      }, 1000)
    } else {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current)
    }
    return () => {
      if (pollTimerRef.current) clearInterval(pollTimerRef.current)
    }
  }, [isOpen, loadBreakdown, loadTaskStatus])

  const snapshot = breakdown?.snapshot
  const isSnapshotReady = Boolean(snapshot && snapshot.status === 'completed' && snapshot.valid && !snapshot.stale)
  const activeSnapshotId = isSnapshotReady && snapshot ? snapshot.snapshot_id : null

  useEffect(() => {
    if (prevSnapshotIdRef.current !== activeSnapshotId) {
      prevSnapshotIdRef.current = activeSnapshotId
      setConfirmingCategoryClean(false)
      setSelectedCategories([])
    }
  }, [activeSnapshotId])

  const isScanning =
    taskStatus !== null ? Boolean(taskStatus?.is_scanning) : Boolean(breakdown?.is_scanning)
  const isCleaning = Boolean(taskStatus?.is_clearing_category)
  const isSlimming = Boolean(taskStatus?.is_slimming)
  const isSubmittingAction =
    startingScan || submittingCategoryClean || busyRam || busyAll
  const hasAnyActiveTask = Boolean(
    isSubmittingAction ||
      taskStatus?.active_task_id ||
      isScanning ||
      isCleaning ||
      isSlimming ||
      taskStatus?.is_auditing ||
      taskStatus?.is_boosting
  )

  const scanProgress = taskStatus?.scan_progress || breakdown?.scan_progress
  const cleanupProgress: CleanupProgress | undefined = taskStatus?.cleanup_progress
  const slimProgress = taskStatus?.slim_progress
  const lastCleanupResult: DeleteResult | undefined = taskStatus?.last_cleanup_result
  const isGbfNamespaceSelected =
    selectedNamespaces.length === 0 || selectedNamespaces.includes('gbf')
  const preview: VersionRetentionPreview | undefined = isGbfNamespaceSelected
    ? snapshot?.retention_previews?.['8']
    : undefined

  // Compute per-category stats filtered by currently selected namespaces (defaulting to ['gbf'])
  const effectiveCategoryStats = useMemo<CategoryStat[]>(() => {
    if (!snapshot?.categories) return []
    const nsList = selectedNamespaces.length > 0 ? selectedNamespaces : ['gbf']
    const nsSet = new Set(nsList)
    const matchingNamespaces = (snapshot.namespaces || []).filter((n) => nsSet.has(n.namespace))

    // If all namespaces in snapshot are selected or namespace breakdown is unavailable, return sorted snapshot.categories
    if (
      matchingNamespaces.length === (snapshot.namespaces || []).length &&
      matchingNamespaces.length > 0
    ) {
      return sortCategoriesWithCautionLast(snapshot.categories)
    }

    const totalAssetBytesInScope = matchingNamespaces.reduce((acc, n) => acc + n.asset_bytes, 0)
    const computed = snapshot.categories.map((cat) => {
      let assetFiles = 0
      let assetBytes = 0
      let metaFiles = 0
      let metaBytes = 0
      let estDelete = 0
      for (const nsStat of matchingNamespaces) {
        const cStat = nsStat.categories?.[cat.category]
        if (cStat) {
          assetFiles += cStat.asset_files
          assetBytes += cStat.asset_bytes
          metaFiles += cStat.metadata_files
          metaBytes += cStat.metadata_bytes
          estDelete += cStat.estimated_delete_bytes
        }
      }
      const pct =
        totalAssetBytesInScope > 0
          ? Math.round((assetBytes / totalAssetBytesInScope) * 10000) / 100
          : 0
      return {
        ...cat,
        asset_files: assetFiles,
        asset_bytes: assetBytes,
        metadata_files: metaFiles,
        metadata_bytes: metaBytes,
        estimated_delete_bytes: estDelete,
        percentage: pct,
      }
    })
    return sortCategoriesWithCautionLast(computed)
  }, [snapshot, selectedNamespaces])

  const selectedCategorySummary = useMemo(() => {
    const sel = new Set(selectedCategories)
    let assetFiles = 0
    let estDeleteBytes = 0
    let pruneDirs = 0
    for (const c of effectiveCategoryStats) {
      if (!sel.has(c.category)) continue
      if (c.category === 'versioned') {
        assetFiles += preview?.prune_asset_files ?? 0
        estDeleteBytes += preview?.estimated_delete_bytes ?? 0
        pruneDirs += preview?.prune_dirs ?? 0
      } else if (c.selectable) {
        assetFiles += c.asset_files
        estDeleteBytes += c.estimated_delete_bytes
      }
    }
    return { assetFiles, estDeleteBytes, pruneDirs }
  }, [effectiveCategoryStats, selectedCategories, preview])

  const handleStartScan = async () => {
    setStartingScan(true)
    setConfirmingCategoryClean(false)
    try {
      await startCacheBreakdownScan()
      prevBusyRef.current = true
      onToast('磁盘缓存扫描已在后台启动', 'info')
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(`启动扫描失败: ${e.message}`, 'error')
    } finally {
      setStartingScan(false)
    }
  }

  const handleCancelActiveTask = async () => {
    try {
      const activeId = taskStatus?.active_task_id || undefined
      await cancelCacheTask(activeId)
      onToast('已发送取消请求', 'info')
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(`取消任务失败: ${e.message}`, 'error')
    }
  }

  const toggleCategory = (cat: string) => {
    setConfirmingCategoryClean(false)
    setSelectedCategories((prev) =>
      prev.includes(cat) ? prev.filter((c) => c !== cat) : [...prev, cat]
    )
  }

  const toggleNamespace = (ns: string) => {
    setConfirmingCategoryClean(false)
    setSelectedNamespaces((prev) => {
      const next = prev.includes(ns) ? prev.filter((item) => item !== ns) : [...prev, ns]
      const resolved = next.length > 0 ? next : ['gbf']
      if (!resolved.includes('gbf')) {
        setSelectedCategories((cats) => cats.filter((c) => c !== 'versioned'))
      }
      return resolved
    })
  }

  const handleExecuteCategoryCleanup = async () => {
    if (!snapshot || !isSnapshotReady) {
      onToast('当前扫描结果已过期，请先重新扫描', 'error')
      return
    }
    if (selectedCategories.length === 0) {
      onToast('请先勾选需要清理的项目', 'error')
      return
    }
    setSubmittingCategoryClean(true)
    try {
      const hasVersioned = selectedCategories.includes('versioned')
      const regularCategories = selectedCategories.filter((c) => c !== 'versioned')
      const nsList = selectedNamespaces.length > 0 ? selectedNamespaces : ['gbf']

      if (regularCategories.length === 0 && hasVersioned) {
        await slimCache(8, snapshot.snapshot_id)
        onToast('历史旧版本清理已在后台启动（自动保留最新 8 个版本）', 'info')
      } else {
        await clearCacheCategories(snapshot.snapshot_id, regularCategories, nsList, hasVersioned)
        onToast('缓存清理任务已在后台启动', 'info')
      }
      prevBusyRef.current = true
      setConfirmingCategoryClean(false)
      setSelectedCategories([])
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(`清理启动失败: ${e.message}`, 'error')
    } finally {
      setSubmittingCategoryClean(false)
    }
  }

  const handleClearRam = async () => {
    setBusyRam(true)
    try {
      await clearCache(true)
      onToast('内存缓存已清空（磁盘缓存文件不受影响）', 'success')
      onRefresh()
    } catch (e: any) {
      onToast(`清空内存缓存失败: ${e.message}`, 'error')
    } finally {
      setBusyRam(false)
    }
  }

  const handleClearAll = async () => {
    setBusyAll(true)
    try {
      const res = await clearCache(false)
      const count = res.disk_files_deleted ?? 0
      setConfirmingFullClear(false)
      onToast(`全部磁盘与内存缓存已清空，共删除文件 ${count} 个`, 'success')
      await loadBreakdown()
      onRefresh()
      onClose()
    } catch (e: any) {
      onToast(`清空全部缓存失败: ${e.message}`, 'error')
    } finally {
      setBusyAll(false)
    }
  }

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
          <Layers className="w-4 h-4 text-sky-600" />
          <span>缓存清理与瘦身</span>
        </div>
      }
      subtitle="查看各类型缓存占用，按需清理过期版本、剧情语音或清空内存缓存"
      maxWidth="max-w-3xl"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        {/* Top Scan & Snapshot Status Bar */}
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2">
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <div className="space-y-0.5">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="font-bold text-slate-900">缓存快照状态：</span>
                {isScanning ? (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-sky-100 text-sky-800 font-semibold">
                    <RefreshCw className="w-3 h-3 animate-spin" />
                    后台扫描中...
                  </span>
                ) : !snapshot ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded bg-slate-200 text-slate-700 font-medium">
                    尚未扫描
                  </span>
                ) : snapshot.stale || !snapshot.valid ? (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-amber-100 text-amber-800 font-semibold">
                    <AlertTriangle className="w-3 h-3" />
                    数据已更新，请重新扫描
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-emerald-100 text-emerald-800 font-semibold">
                    <CheckCircle2 className="w-3 h-3" />
                    有效快照 ({formatTimestamp(snapshot.completed_at_ms)})
                  </span>
                )}
              </div>
              <div className="text-[11px] text-slate-500 flex items-center gap-2 flex-wrap">
                <span className="font-mono break-all">
                  目录: {breakdown?.cache_base || snapshot?.cache_base || '--'}
                </span>
                {snapshot && (
                  <span className="text-slate-700 font-medium">
                    ｜ 已缓存 <strong className="font-mono">{snapshot.asset_files.toLocaleString()}</strong> 个文件 · 共占用{' '}
                    <strong className="font-mono">
                      {formatBytesBinary(snapshot.asset_bytes + snapshot.metadata_bytes)}
                    </strong>
                  </span>
                )}
              </div>
            </div>

            <div className="flex items-center gap-2">
              {isScanning || isCleaning || isSlimming ? (
                <Button
                  variant="desktop"
                  size="xs"
                  onClick={handleCancelActiveTask}
                  icon={<XCircle className="w-3.5 h-3.5 text-red-600" />}
                >
                  取消当前任务
                </Button>
              ) : (
                <Button
                  variant="primary"
                  size="xs"
                  loading={startingScan}
                  disabled={hasAnyActiveTask}
                  onClick={handleStartScan}
                  icon={<RefreshCw className="w-3 h-3" />}
                >
                  {snapshot ? '重新扫描磁盘' : '开始扫描缓存'}
                </Button>
              )}
            </div>
          </div>

          {/* Active Scan Progress */}
          {isScanning && (
            <div className="p-2.5 bg-sky-50 border border-sky-200 rounded text-[11px] text-sky-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                已扫描文件: <strong>{(scanProgress?.scanned_files ?? 0).toLocaleString()}</strong> 个
              </span>
              <span>
                已统计大小: <strong>{formatBytesBinary(scanProgress?.scanned_bytes ?? 0)}</strong>
              </span>
            </div>
          )}

          {/* Scan Failed / Cancelled Outcome Banner */}
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

          {/* Active Cleanup Progress */}
          {isCleaning && cleanupProgress && (
            <div className="p-2.5 bg-blue-50 border border-blue-200 rounded text-[11px] text-blue-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                正在清理缓存: <strong>{cleanupProgress.processed_files} / {cleanupProgress.total_files}</strong>
              </span>
              <span>
                已清理: <strong>{cleanupProgress.deleted_asset_files}</strong> 个文件 (
                {formatBytesBinary(cleanupProgress.freed_bytes)})
              </span>
            </div>
          )}

          {/* Active Slim Progress */}
          {isSlimming && (
            <div className="p-2.5 bg-blue-50 border border-blue-200 rounded text-[11px] text-blue-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                正在清理历史版本目录: <strong>{slimProgress?.current_idx ?? 0} / {slimProgress?.total_dirs ?? 0}</strong>
              </span>
              <span>
                已清理: <strong>{slimProgress?.deleted_files ?? 0}</strong> 个文件 (
                {formatBytesBinary(slimProgress?.freed_bytes ?? 0)})
              </span>
            </div>
          )}

          {/* Last Cleanup Outcome Banner */}
          {!isCleaning && lastCleanupResult && (
            <div
              className={`p-2 border rounded text-[11px] flex items-center justify-between flex-wrap gap-2 ${
                lastCleanupResult.status === 'failed'
                  ? 'bg-red-50/90 border-red-200 text-red-900'
                  : lastCleanupResult.status === 'partial_failed' ||
                      lastCleanupResult.cancelled
                    ? 'bg-amber-50/90 border-amber-200 text-amber-900'
                    : 'bg-emerald-50/90 border-emerald-200 text-emerald-900'
              }`}
            >
              <div className="font-medium flex items-center gap-1.5">
                {lastCleanupResult.status === 'failed' ||
                lastCleanupResult.status === 'partial_failed' ||
                lastCleanupResult.cancelled ? (
                  <AlertTriangle
                    className={`w-3.5 h-3.5 shrink-0 ${
                      lastCleanupResult.status === 'failed'
                        ? 'text-red-600'
                        : 'text-amber-600'
                    }`}
                  />
                ) : (
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600 shrink-0" />
                )}
                <span>
                  最近一次清理结果：{lastCleanupResult.message || '执行完成'}
                </span>
              </div>
              <div
                className={`font-mono text-[11px] flex items-center gap-2 flex-wrap ${
                  lastCleanupResult.status === 'failed'
                    ? 'text-red-800'
                    : lastCleanupResult.status === 'partial_failed' ||
                        lastCleanupResult.cancelled
                      ? 'text-amber-800'
                      : 'text-emerald-800'
                }`}
              >
                <span>共清理 {lastCleanupResult.deleted_asset_files} 个文件</span>
                <span>释放 {formatBytesBinary(lastCleanupResult.freed_bytes)}</span>
                {lastCleanupResult.failed_count > 0 && (
                  <span className="text-red-600">失败 {lastCleanupResult.failed_count} 项</span>
                )}
              </div>
            </div>
          )}

          {/* Last Slim Outcome Banner */}
          {!isSlimming && !lastCleanupResult && taskStatus?.last_slim_result && (
            <div
              className={`p-2 border rounded text-[11px] flex items-center justify-between flex-wrap gap-2 ${
                taskStatus.last_slim_result.status === 'failed'
                  ? 'bg-red-50/90 border-red-200 text-red-900'
                  : taskStatus.last_slim_result.status === 'partial_failed' ||
                      taskStatus.last_slim_result.cancelled
                    ? 'bg-amber-50/90 border-amber-200 text-amber-900'
                    : 'bg-emerald-50/90 border-emerald-200 text-emerald-900'
              }`}
            >
              <div className="font-medium flex items-center gap-1.5">
                {taskStatus.last_slim_result.status === 'failed' ||
                taskStatus.last_slim_result.status === 'partial_failed' ||
                taskStatus.last_slim_result.cancelled ? (
                  <AlertTriangle
                    className={`w-3.5 h-3.5 shrink-0 ${
                      taskStatus.last_slim_result.status === 'failed'
                        ? 'text-red-600'
                        : 'text-amber-600'
                    }`}
                  />
                ) : (
                  <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600 shrink-0" />
                )}
                <span>
                  {taskStatus.last_slim_result.status === 'failed'
                    ? '最近一次版本清理失败'
                    : taskStatus.last_slim_result.status === 'partial_failed'
                      ? '最近一次版本清理部分完成（含失败项）'
                      : taskStatus.last_slim_result.cancelled
                        ? '最近一次版本清理已取消'
                        : '最近一次版本清理完成'}
                </span>
              </div>
              <div className="font-mono text-[11px] flex items-center gap-2 flex-wrap">
                <span>清理旧版本 {taskStatus.last_slim_result.deleted_dirs ?? 0} 个</span>
                <span>释放 {formatBytesBinary(taskStatus.last_slim_result.freed_bytes ?? 0)}</span>
              </div>
            </div>
          )}
        </div>

        {/* Navigation Tabs */}
        <div className="flex items-center gap-1.5 border-b border-slate-200 pb-2 flex-wrap">
          <button
            type="button"
            onClick={() => setActiveTab('breakdown')}
            className={`px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer flex items-center gap-1.5 ${
              activeTab === 'breakdown'
                ? 'bg-sky-600 text-white shadow-2xs'
                : 'bg-slate-100 text-slate-700 hover:bg-slate-200'
            }`}
          >
            <Layers className="w-3.5 h-3.5" />
            <span>分类与版本清理</span>
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('reset')}
            className={`px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer flex items-center gap-1.5 ${
              activeTab === 'reset'
                ? 'bg-rose-600 text-white shadow-2xs'
                : 'bg-slate-100 text-slate-700 hover:bg-slate-200'
            }`}
          >
            <Trash2 className="w-3.5 h-3.5" />
            <span>内存与全量清空</span>
          </button>
        </div>

        {/* TAB 1: Unified Category & Version Cleanup */}
        {activeTab === 'breakdown' && (
          <div className="space-y-3">
            {!snapshot ? (
              <div className="p-6 text-center bg-slate-50 border border-dashed border-slate-300 rounded-lg space-y-2">
                <div className="text-slate-700 font-semibold">尚未扫描缓存目录</div>
                <p className="text-[11px] text-slate-500 max-w-md mx-auto">
                  点击上方“开始扫描缓存”，即可查看各类型文件占用大小，并勾选清理过期版本、剧情立绘或语音文件。
                </p>
              </div>
            ) : (
              <>
                {/* Visual Proportion Bar */}
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between text-[11px] text-slate-500">
                    <span>各类型缓存占比：</span>
                    <span className="font-mono">
                      共{' '}
                      {formatBytesBinary(
                        effectiveCategoryStats.reduce((a, b) => a + b.asset_bytes, 0)
                      )}
                    </span>
                  </div>
                  <div className="w-full h-3 bg-slate-200 rounded-full overflow-hidden flex">
                    {effectiveCategoryStats.map((cat) =>
                      cat.percentage > 0 ? (
                        <div
                          key={cat.category}
                          className={`${CATEGORY_COLORS[cat.category] || 'bg-slate-400'} h-full transition-all duration-300`}
                          style={{ width: `${Math.max(cat.percentage, 0.5)}%` }}
                          title={`${cat.label}: ${formatBytesBinary(cat.asset_bytes)} (${cat.percentage}%)`}
                        />
                      ) : null
                    )}
                  </div>
                </div>

                {/* Namespace Filter (when multiple namespaces exist) */}
                {snapshot.namespaces && snapshot.namespaces.length > 1 && (
                  <div className="p-2.5 bg-slate-50 border border-slate-200 rounded-lg flex items-center justify-between flex-wrap gap-2 text-[11px]">
                    <span className="font-semibold text-slate-700">
                      清理域名范围 (默认仅 gbf)：
                    </span>
                    <div className="flex items-center gap-3 flex-wrap">
                      {snapshot.namespaces.map((ns) => (
                        <label
                          key={ns.namespace}
                          className="inline-flex items-center gap-1.5 cursor-pointer select-none font-mono"
                        >
                          <input
                            type="checkbox"
                            checked={selectedNamespaces.includes(ns.namespace)}
                            onChange={() => toggleNamespace(ns.namespace)}
                            disabled={!isSnapshotReady || hasAnyActiveTask}
                            className="w-3.5 h-3.5 rounded text-sky-600 accent-sky-600"
                          />
                          <span>
                            {ns.namespace} ({formatBytesBinary(ns.asset_bytes)})
                          </span>
                        </label>
                      ))}
                    </div>
                  </div>
                )}

                {/* 7-Category List (all rows use checkboxes uniformly) */}
                <div className="border border-slate-200 rounded-lg overflow-hidden">
                  <div className="max-h-72 overflow-y-auto divide-y divide-slate-200">
                    {effectiveCategoryStats.map((cat) => {
                      const isChecked = selectedCategories.includes(cat.category)
                      const isCaution = DEFAULT_CAUTION_CATEGORIES.has(cat.category)
                      const isVersioned = cat.category === 'versioned'

                      const versionedPruneFiles = preview?.prune_asset_files ?? 0
                      const versionedPruneDirs = preview?.prune_dirs ?? 0
                      const versionedEstBytes = preview?.estimated_delete_bytes ?? 0

                      const canCheck = isVersioned
                        ? isSnapshotReady &&
                          !hasAnyActiveTask &&
                          (versionedPruneFiles > 0 || versionedPruneDirs > 0)
                        : isSnapshotReady && !hasAnyActiveTask && cat.selectable && cat.asset_files > 0

                      return (
                        <div
                          key={cat.category}
                          className={`p-2.5 flex items-start justify-between gap-3 transition-colors ${
                            isChecked ? 'bg-sky-50/60' : 'bg-white hover:bg-slate-50/80'
                          }`}
                        >
                          <div className="flex items-start gap-2.5 min-w-0">
                            <input
                              type="checkbox"
                              checked={isChecked}
                              disabled={!canCheck}
                              onChange={() => toggleCategory(cat.category)}
                              className="w-4 h-4 mt-0.5 rounded text-sky-600 accent-sky-600 cursor-pointer shrink-0 disabled:opacity-40 disabled:cursor-not-allowed"
                            />
                            <div className="space-y-0.5 min-w-0">
                              <div className="flex items-center gap-2 flex-wrap">
                                <span
                                  className={`w-2.5 h-2.5 rounded-full shrink-0 ${
                                    CATEGORY_COLORS[cat.category] || 'bg-slate-400'
                                  }`}
                                />
                                <span className="font-bold text-slate-900">{cat.label}</span>
                                {isCaution && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-slate-600 border border-slate-200">
                                    推荐不选
                                  </span>
                                )}
                              </div>
                              <p className="text-[11px] text-slate-500 leading-snug">
                                {cat.description}
                              </p>
                            </div>
                          </div>

                          <div className="text-right shrink-0 font-mono space-y-0.5">
                            <div className="text-xs font-bold text-slate-900">
                              占用: {formatBytesBinary(cat.asset_bytes)}{' '}
                              <span className="text-slate-500 font-normal">({cat.percentage}%)</span>
                            </div>
                            <div className="text-[11px] text-slate-500">
                              共 {cat.asset_files.toLocaleString()} 个文件
                              {isVersioned && versionedPruneDirs > 0
                                ? ` · 含 ${versionedPruneDirs} 个过期版本`
                                : ''}
                            </div>
                            {isVersioned ? (
                              <div
                                className={`text-[11px] font-semibold ${
                                  versionedEstBytes > 0 || versionedPruneDirs > 0
                                    ? 'text-sky-700'
                                    : 'text-slate-400'
                                }`}
                              >
                                {versionedEstBytes > 0
                                  ? `可释放旧版本: ${formatBytesBinary(versionedEstBytes)}`
                                  : versionedPruneDirs > 0
                                    ? `可清理 ${versionedPruneDirs} 个空旧版目录`
                                    : '无过期旧版本可清理'}
                              </div>
                            ) : (
                              <div className="text-[11px] text-sky-700 font-semibold">
                                预计释放: {formatBytesBinary(cat.estimated_delete_bytes)}
                              </div>
                            )}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                </div>

                {/* Selective Cleanup Footer Action */}
                <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg flex items-center justify-between flex-wrap gap-2">
                  <div className="space-y-0.5">
                    <div className="font-semibold text-slate-800">
                      已选 {selectedCategories.length} 个类别 · 预计清理{' '}
                      <span className="font-mono text-sky-700">
                        {selectedCategorySummary.assetFiles.toLocaleString()}
                      </span>{' '}
                      个文件
                    </div>
                    <div className="text-[11px] text-slate-500">
                      预计释放空间:{' '}
                      <strong className="font-mono text-sky-700">
                        {formatBytesBinary(selectedCategorySummary.estDeleteBytes)}
                      </strong>
                    </div>
                  </div>

                  <div className="flex items-center gap-2">
                    {confirmingCategoryClean ? (
                      <>
                        <Button
                          variant="desktop"
                          size="xs"
                          onClick={() => setConfirmingCategoryClean(false)}
                        >
                          取消
                        </Button>
                        <Button
                          variant="danger"
                          size="xs"
                          loading={submittingCategoryClean}
                          disabled={!isSnapshotReady || hasAnyActiveTask}
                          onClick={handleExecuteCategoryCleanup}
                        >
                          确认清理
                        </Button>
                      </>
                    ) : (
                      <Button
                        variant="primary"
                        size="xs"
                        disabled={
                          !isSnapshotReady ||
                          hasAnyActiveTask ||
                          selectedCategories.length === 0 ||
                          (selectedCategorySummary.assetFiles === 0 &&
                            selectedCategorySummary.pruneDirs === 0)
                        }
                        onClick={() => setConfirmingCategoryClean(true)}
                      >
                        清理所选缓存
                      </Button>
                    )}
                  </div>
                </div>
              </>
            )}
          </div>
        )}

        {/* TAB 2: Memory & Full Disk Clear */}
        {activeTab === 'reset' && (
          <div className="space-y-3">
            {/* Option 1: RAM Only */}
            <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2.5">
              <div className="flex items-start gap-2.5">
                <div className="w-7 h-7 rounded-md bg-emerald-100 text-emerald-700 flex items-center justify-center shrink-0 mt-0.5">
                  <Cpu className="w-4 h-4" />
                </div>
                <div className="space-y-1">
                  <h4 className="text-xs font-bold text-slate-900">
                    仅清空内存缓存 (RAM)
                  </h4>
                  <p className="text-[11px] text-slate-600 leading-relaxed">
                    仅释放当前占用的内存热缓存，<strong>不删除硬盘上的已下载文件</strong>，适合游戏页面显示异常时快速刷新。
                  </p>
                </div>
              </div>

              <div className="flex justify-end">
                <Button
                  variant="desktop"
                  size="sm"
                  loading={busyRam}
                  disabled={hasAnyActiveTask}
                  onClick={handleClearRam}
                >
                  仅清空内存缓存
                </Button>
              </div>
            </div>

            {/* Option 2: Full Disk & Memory Clear */}
            <div className="p-3.5 bg-red-50/70 border border-red-200 rounded-lg space-y-2.5">
              <div className="flex items-start gap-2.5">
                <div className="w-7 h-7 rounded-md bg-red-100 text-red-700 flex items-center justify-center shrink-0 mt-0.5">
                  <HardDrive className="w-4 h-4" />
                </div>
                <div className="space-y-1">
                  <h4 className="text-xs font-bold text-red-700 flex items-center gap-1.5">
                    <span>清空全部磁盘与内存缓存</span>
                  </h4>
                  <p className="text-[11px] text-slate-600 leading-relaxed">
                    彻底删除硬盘缓存目录下的全部已下载文件并清空内存。清空后所有游戏素材均需重新下载。
                  </p>
                </div>
              </div>

              <div className="flex justify-end items-center gap-2">
                {confirmingFullClear ? (
                  <>
                    <Button
                      variant="desktop"
                      size="sm"
                      onClick={() => setConfirmingFullClear(false)}
                    >
                      取消
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      loading={busyAll}
                      disabled={hasAnyActiveTask}
                      onClick={handleClearAll}
                    >
                      确认清空全部缓存
                    </Button>
                  </>
                ) : (
                  <Button
                    variant="danger"
                    size="sm"
                    disabled={hasAnyActiveTask}
                    onClick={() => setConfirmingFullClear(true)}
                  >
                    清空全部磁盘与内存缓存
                  </Button>
                )}
              </div>
            </div>
          </div>
        )}

        <div className="flex justify-between items-center pt-1 border-t border-slate-100 text-[11px] text-slate-500">
          <span>
            说明：清理本地缓存仅移除加速器本地文件，不会清除浏览器自身的 Cookie 或登录状态。
          </span>
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
