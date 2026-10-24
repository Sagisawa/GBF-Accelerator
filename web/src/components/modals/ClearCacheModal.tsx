import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { useI18n } from '../../i18n'
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
  BreakdownSnapshot,
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

export function resolveDefaultNamespaces(
  snapshot: Pick<BreakdownSnapshot, 'namespaces' | 'retention_previews'> | null | undefined
): string[] {
  const nsEntries = snapshot?.namespaces || []
  if (nsEntries.length === 0) {
    return ['gbf']
  }
  const gbfEntry = nsEntries.find((n) => n.namespace === 'gbf')
  const gbfPruneDirs = snapshot?.retention_previews?.['8']?.prune_dirs ?? 0
  if (gbfEntry && (gbfEntry.asset_files > 0 || gbfPruneDirs > 0)) {
    return ['gbf']
  }
  const nonEmpty = nsEntries.filter((n) => n.asset_files > 0).map((n) => n.namespace)
  if (nonEmpty.length > 0) {
    return nonEmpty
  }
  if (gbfEntry) {
    return ['gbf']
  }
  return [nsEntries[0].namespace]
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
  const { t } = useI18n()
  const getCategoryLabel = useCallback(
    (category: string, defaultLabel: string) => {
      const key = `modals.clearCache.categories.${category}`
      const val = t(key)
      return val !== key ? val : defaultLabel
    },
    [t]
  )
  const getCategoryDesc = useCallback(
    (category: string, defaultDesc: string) => {
      const key = `modals.clearCache.categoryDescriptions.${category}`
      const val = t(key)
      return val !== key ? val : defaultDesc
    },
    [t]
  )
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
  const activeModalTaskIdRef = useRef<string | null>(null)

  const loadBreakdown = useCallback(async () => {
    const reqSeq = ++breakdownReqSeqRef.current
    try {
      const data = await fetchCacheBreakdown()
      if (reqSeq !== breakdownReqSeqRef.current) return
      breakdownScanningRef.current = Boolean(data?.is_scanning)
      if (data?.is_scanning && data?.active_task_id) {
        activeModalTaskIdRef.current = String(data.active_task_id)
      }
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
      const isModalTaskRunning = Boolean(
        data?.is_scanning || data?.is_clearing_category || data?.is_slimming
      )
      if (isModalTaskRunning && data?.active_task_id) {
        activeModalTaskIdRef.current = String(data.active_task_id)
      } else if (!isModalTaskRunning) {
        activeModalTaskIdRef.current = null
      }
      const busy = Boolean(
        data?.is_scanning ||
          data?.is_clearing_category ||
          data?.is_slimming ||
          data?.is_auditing ||
          data?.is_boosting ||
          data?.active_task_id
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
  const defaultNamespaces = useMemo(() => resolveDefaultNamespaces(snapshot), [snapshot])

  useEffect(() => {
    if (prevSnapshotIdRef.current !== activeSnapshotId) {
      prevSnapshotIdRef.current = activeSnapshotId
      setConfirmingCategoryClean(false)
      setSelectedCategories((prev) => (prev.length === 0 ? prev : []))
      setSelectedNamespaces(resolveDefaultNamespaces(snapshot))
    }
  }, [activeSnapshotId, snapshot])

  const effectiveSelectedNamespaces = useMemo<string[]>(() => {
    const available = new Set((snapshot?.namespaces || []).map((n) => n.namespace))
    if (available.size === 0) {
      return selectedNamespaces.length > 0 ? selectedNamespaces : defaultNamespaces
    }
    const validSelected = selectedNamespaces.filter((ns) => available.has(ns))
    return validSelected.length > 0 ? validSelected : defaultNamespaces
  }, [snapshot, selectedNamespaces, defaultNamespaces])

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
    effectiveSelectedNamespaces.length === 0 || effectiveSelectedNamespaces.includes('gbf')
  const preview: VersionRetentionPreview | undefined = isGbfNamespaceSelected
    ? snapshot?.retention_previews?.['8']
    : undefined

  // Compute per-category stats filtered by currently selected namespaces
  const effectiveCategoryStats = useMemo<CategoryStat[]>(() => {
    if (!snapshot?.categories) return []
    const nsList =
      effectiveSelectedNamespaces.length > 0 ? effectiveSelectedNamespaces : defaultNamespaces
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
  }, [snapshot, effectiveSelectedNamespaces, defaultNamespaces])

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
      const res = await startCacheBreakdownScan()
      if (res?.task_id) {
        activeModalTaskIdRef.current = String(res.task_id)
      }
      prevBusyRef.current = true
      onToast(t('modals.clearCache.scanStarted'), 'info')
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(t('modals.clearCache.scanFailedMsg', { error: e.message }), 'error')
    } finally {
      setStartingScan(false)
    }
  }

  const handleCancelActiveTask = async () => {
    try {
      const isModalTaskRunning = Boolean(
        taskStatus?.is_scanning || taskStatus?.is_clearing_category || taskStatus?.is_slimming
      )
      const activeId =
        (isModalTaskRunning && taskStatus?.active_task_id
          ? String(taskStatus.active_task_id)
          : '') ||
        activeModalTaskIdRef.current ||
        (breakdown?.is_scanning && breakdown?.active_task_id
          ? String(breakdown.active_task_id)
          : '')
      if (!activeId) {
        await loadTaskStatus()
        await loadBreakdown()
        return
      }
      await cancelCacheTask(activeId)
      onToast(t('modals.clearCache.taskCancelled'), 'info')
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(t('modals.clearCache.cancelFailed', { error: e.message }), 'error')
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
    setSelectedNamespaces(() => {
      const base = effectiveSelectedNamespaces
      const next = base.includes(ns) ? base.filter((item) => item !== ns) : [...base, ns]
      const resolved = next.length > 0 ? next : defaultNamespaces
      if (!resolved.includes('gbf')) {
        setSelectedCategories((cats) => cats.filter((c) => c !== 'versioned'))
      }
      return resolved
    })
  }

  const handleExecuteCategoryCleanup = async () => {
    if (!snapshot || !isSnapshotReady) {
      onToast(t('modals.clearCache.snapshotExpired'), 'error')
      return
    }
    if (selectedCategories.length === 0) {
      onToast(t('modals.clearCache.selectCategoryRequired'), 'error')
      return
    }
    setSubmittingCategoryClean(true)
    try {
      const hasVersioned = selectedCategories.includes('versioned')
      const regularCategories = selectedCategories.filter((c) => c !== 'versioned')
      const nsList =
        effectiveSelectedNamespaces.length > 0 ? effectiveSelectedNamespaces : defaultNamespaces

      if (regularCategories.length === 0 && hasVersioned) {
        const res = await slimCache(8, snapshot.snapshot_id)
        if (res?.task_id) {
          activeModalTaskIdRef.current = String(res.task_id)
        }
        onToast(t('modals.clearCache.slimStarted', { count: 8 }), 'info')
      } else {
        const res = await clearCacheCategories(
          snapshot.snapshot_id,
          regularCategories,
          nsList,
          hasVersioned
        )
        if (res?.task_id) {
          activeModalTaskIdRef.current = String(res.task_id)
        }
        onToast(t('modals.clearCache.cleanupStarted'), 'info')
      }
      prevBusyRef.current = true
      setConfirmingCategoryClean(false)
      setSelectedCategories([])
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(t('modals.clearCache.cleanupFailed', { error: e.message }), 'error')
    } finally {
      setSubmittingCategoryClean(false)
    }
  }

  const handleClearRam = async () => {
    setBusyRam(true)
    try {
      await clearCache(true)
      onToast(t('modals.clearCache.ramFlushed'), 'success')
      onRefresh()
    } catch (e: any) {
      onToast(t('modals.clearCache.ramFlushFailed', { error: e.message }), 'error')
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
      onToast(t('modals.clearCache.fullResetCompleted', { count }), 'success')
      await loadBreakdown()
      onRefresh()
      onClose()
    } catch (e: any) {
      onToast(t('modals.clearCache.fullResetFailed', { error: e.message }), 'error')
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
          <span>{t('modals.clearCache.title')}</span>
        </div>
      }
      subtitle={t('modals.clearCache.subtitle')}
      maxWidth="max-w-3xl"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        {/* Top Scan & Snapshot Status Bar */}
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2">
          <div className="flex items-center justify-between gap-2 flex-wrap">
            <div className="space-y-0.5">
              <div className="flex items-center gap-2 flex-wrap">
                <span className="font-bold text-slate-900">{t('modals.clearCache.diskStatus')}</span>
                {isScanning ? (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-sky-100 text-sky-800 font-semibold">
                    <RefreshCw className="w-3 h-3 animate-spin" />
                    {t('modals.clearCache.backgroundScanning')}
                  </span>
                ) : !snapshot ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded bg-slate-200 text-slate-700 font-medium">
                    {t('modals.clearCache.notScanned')}
                  </span>
                ) : snapshot.stale || !snapshot.valid ? (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-amber-100 text-amber-800 font-semibold">
                    <AlertTriangle className="w-3 h-3" />
                    {t('modals.clearCache.cacheUpdatedSuggestRescan')}
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-emerald-100 text-emerald-800 font-semibold">
                    <CheckCircle2 className="w-3 h-3" />
                    {t('modals.clearCache.validSnapshot')} ({formatTimestamp(snapshot.completed_at_ms)})
                  </span>
                )}
              </div>
              <div className="text-[11px] text-slate-500 flex items-center gap-2 flex-wrap">
                <span className="font-mono break-all">
                  {t('modals.clearCache.cacheDir', { dir: breakdown?.cache_base || snapshot?.cache_base || '--' })}
                </span>
                {snapshot && (
                  <span className="text-slate-700 font-medium">
                    ｜ {t('modals.clearCache.cachedFilesSize', { count: snapshot.asset_files.toLocaleString(), size: formatBytesBinary(snapshot.asset_bytes + snapshot.metadata_bytes) })}
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
                  {t('modals.clearCache.cancelTask')}
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
                  {snapshot ? t('modals.clearCache.rescanDisk') : t('modals.clearCache.startScan')}
                </Button>
              )}
            </div>
          </div>

          {/* Active Scan Progress */}
          {isScanning && (
            <div className="p-2.5 bg-sky-50 border border-sky-200 rounded text-[11px] text-sky-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                {t('modals.clearCache.scannedFiles', { count: (scanProgress?.scanned_files ?? 0).toLocaleString() })}
              </span>
              <span>
                {t('modals.clearCache.scannedSize', { size: formatBytesBinary(scanProgress?.scanned_bytes ?? 0) })}
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
                  {scanOutcomeStatus === 'failed' ? t('modals.clearCache.scanFailed') : t('modals.clearCache.scanCancelled')}
                  {scanOutcomeMessage || (scanOutcomeStatus === 'failed' ? t('modals.clearCache.scanFailed') : t('modals.clearCache.scanCancelledTitle'))}
                </span>
              </div>
            </div>
          )}

          {/* Active Cleanup Progress */}
          {isCleaning && cleanupProgress && (
            <div className="p-2.5 bg-blue-50 border border-blue-200 rounded text-[11px] text-blue-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                {t('modals.clearCache.cleanupProgress', { processed: cleanupProgress.processed_files, total: cleanupProgress.total_files })}
              </span>
              <span>
                {t('modals.clearCache.cleanedFiles', { count: cleanupProgress.deleted_asset_files, size: formatBytesBinary(cleanupProgress.freed_bytes) })}
              </span>
            </div>
          )}

          {/* Active Slim Progress */}
          {isSlimming && (
            <div className="p-2.5 bg-blue-50 border border-blue-200 rounded text-[11px] text-blue-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                {t('modals.clearCache.slimmingProgress')} <strong>{slimProgress?.current_idx ?? 0} / {slimProgress?.total_dirs ?? 0}</strong>
              </span>
              <span>
                {t('modals.clearCache.cleanedFiles', { count: slimProgress?.deleted_files ?? 0, size: formatBytesBinary(slimProgress?.freed_bytes ?? 0) })}
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
                  {t('modals.clearCache.lastCleanupResult', { msg: lastCleanupResult.message || 'OK' })}
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
                <span>{t('modals.clearCache.cleanedResultFiles', { count: lastCleanupResult.deleted_asset_files })}</span>
                <span>{t('modals.clearCache.freedSpace', { size: formatBytesBinary(lastCleanupResult.freed_bytes) })}</span>
                {lastCleanupResult.failed_count > 0 && (
                  <span className="text-red-600">{t('modals.clearCache.failedItems', { count: lastCleanupResult.failed_count })}</span>
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
                    ? t('modals.clearCache.lastSlimFailed')
                    : taskStatus.last_slim_result.status === 'partial_failed'
                      ? t('modals.clearCache.lastSlimPartial')
                      : taskStatus.last_slim_result.cancelled
                        ? t('modals.clearCache.lastSlimCancelled')
                        : t('modals.clearCache.slimmingCompleted')}
                </span>
              </div>
              <div className="font-mono text-[11px] flex items-center gap-2 flex-wrap">
                <span>{t('modals.clearCache.slimmedOldVersions', { count: taskStatus.last_slim_result.deleted_dirs ?? 0 })}</span>
                <span>{t('modals.clearCache.freedSpace', { size: formatBytesBinary(taskStatus.last_slim_result.freed_bytes ?? 0) })}</span>
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
            <span>{t('modals.clearCache.tabCategories')}</span>
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
            <span>{t('modals.clearCache.tabFullReset')}</span>
          </button>
        </div>

        {/* TAB 1: Unified Category & Version Cleanup */}
        {activeTab === 'breakdown' && (
          <div className="space-y-3">
            {!snapshot ? (
              <div className="p-6 text-center bg-slate-50 border border-dashed border-slate-300 rounded-lg space-y-2">
                <div className="text-slate-700 font-semibold">{t('modals.clearCache.noScanYet')}</div>
                <p className="text-[11px] text-slate-500 max-w-md mx-auto">
                  {t('modals.clearCache.noScanYetDesc')}
                </p>
              </div>
            ) : (
              <>
                {/* Visual Proportion Bar */}
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between text-[11px] text-slate-500">
                    <span>{t('modals.clearCache.categoryRatio')}</span>
                    <span className="font-mono">
                      {t('modals.clearCache.totalSize', {
                        size: formatBytesBinary(
                          effectiveCategoryStats.reduce((a, b) => a + b.asset_bytes, 0)
                        ),
                      })}
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

                {/* Namespace Filter (when multiple namespaces or a non-gbf namespace exist) */}
                {snapshot.namespaces &&
                  (snapshot.namespaces.length > 1 ||
                    (snapshot.namespaces.length === 1 &&
                      snapshot.namespaces[0].namespace !== 'gbf')) && (
                  <div className="p-2.5 bg-slate-50 border border-slate-200 rounded-lg flex items-center justify-between flex-wrap gap-2 text-[11px]">
                    <span className="font-semibold text-slate-700">
                      {t('modals.clearCache.namespaceScope')}
                    </span>
                    <div className="flex items-center gap-3 flex-wrap">
                      {snapshot.namespaces.map((ns) => (
                        <label
                          key={ns.namespace}
                          className="inline-flex items-center gap-1.5 cursor-pointer select-none font-mono"
                        >
                          <input
                            type="checkbox"
                            checked={effectiveSelectedNamespaces.includes(ns.namespace)}
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
                                <span className="font-bold text-slate-900">{getCategoryLabel(cat.category, cat.label)}</span>
                                {isCaution && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-slate-600 border border-slate-200">
                                    {t('modals.clearCache.recommendedNotSelect')}
                                  </span>
                                )}
                              </div>
                              <p className="text-[11px] text-slate-500 leading-snug">
                                {getCategoryDesc(cat.category, cat.description)}
                              </p>
                            </div>
                          </div>

                          <div className="text-right shrink-0 font-mono space-y-0.5">
                            <div className="text-xs font-bold text-slate-900">
                              {t('modals.clearCache.occupied', { size: formatBytesBinary(cat.asset_bytes) })}{' '}
                              <span className="text-slate-500 font-normal">({cat.percentage}%)</span>
                            </div>
                            <div className="text-[11px] text-slate-500">
                              {t('modals.clearCache.filesCount', { count: cat.asset_files.toLocaleString() })}
                              {isVersioned && versionedPruneDirs > 0
                                ? t('modals.clearCache.expiredVersions', { count: versionedPruneDirs })
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
                                  ? t('modals.clearCache.releasableOldVersions', { size: formatBytesBinary(versionedEstBytes) })
                                  : versionedPruneDirs > 0
                                    ? t('modals.clearCache.cleanableEmptyDirs', { count: versionedPruneDirs })
                                    : t('modals.clearCache.noExpiredVersions')}
                              </div>
                            ) : (
                              <div className="text-[11px] text-sky-700 font-semibold">
                                {t('modals.clearCache.estimatedRelease', { size: formatBytesBinary(cat.estimated_delete_bytes) })}
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
                      {t('modals.clearCache.selectedCategories', { count: selectedCategories.length })} · {t('modals.clearCache.estPruneFiles', { count: selectedCategorySummary.assetFiles.toLocaleString() })}
                    </div>
                    <div className="text-[11px] text-slate-500">
                      {t('modals.clearCache.estFreeSpace', { size: formatBytesBinary(selectedCategorySummary.estDeleteBytes) })}
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
                          {t('common.cancel')}
                        </Button>
                        <Button
                          variant="danger"
                          size="xs"
                          loading={submittingCategoryClean}
                          disabled={!isSnapshotReady || hasAnyActiveTask}
                          onClick={handleExecuteCategoryCleanup}
                        >
                          {t('modals.clearCache.confirmClean')}
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
                        {t('modals.clearCache.cleanSelected')}
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
                    {t('modals.clearCache.clearRamOnlyTitle')}
                  </h4>
                  <p className="text-[11px] text-slate-600 leading-relaxed">
                    {t('modals.clearCache.clearRamOnlyDesc')}
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
                  {t('modals.clearCache.clearRamOnlyBtn')}
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
                    <span>{t('modals.clearCache.clearAllTitle')}</span>
                  </h4>
                  <p className="text-[11px] text-slate-600 leading-relaxed">
                    {t('modals.clearCache.clearAllDesc')}
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
                      {t('common.cancel')}
                    </Button>
                    <Button
                      variant="danger"
                      size="sm"
                      loading={busyAll}
                      disabled={hasAnyActiveTask}
                      onClick={handleClearAll}
                    >
                      {t('modals.clearCache.fullResetConfirm')}
                    </Button>
                  </>
                ) : (
                  <Button
                    variant="danger"
                    size="sm"
                    disabled={hasAnyActiveTask}
                    onClick={() => setConfirmingFullClear(true)}
                  >
                    {t('modals.clearCache.fullResetConfirm')}
                  </Button>
                )}
              </div>
            </div>
          </div>
        )}

        <div className="flex justify-between items-center pt-1 border-t border-slate-100 text-[11px] text-slate-500">
          <span>
          </span>
          <Button variant="desktop" size="sm" onClick={onClose}>
            {t('common.closeEsc')}
          </Button>
        </div>
      </div>
    </Modal>
  )
}
