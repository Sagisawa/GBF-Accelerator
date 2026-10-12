import React, { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import {
  Trash2,
  Cpu,
  HardDrive,
  Search,
  RefreshCw,
  XCircle,
  CheckCircle2,
  AlertTriangle,
  Layers,
  FolderGit2,
} from 'lucide-react'
import {
  clearCache,
  fetchCacheBreakdown,
  startCacheBreakdownScan,
  searchCacheSnapshot,
  invalidateCacheEntries,
  clearCacheCategories,
  fetchCacheTaskStatus,
  cancelCacheTask,
  formatBytesBinary,
} from '../../api'
import {
  BreakdownResponse,
  CategoryStat,
  SearchItem,
  SearchResponse,
  DeleteResult,
  CleanupProgress,
} from '../../types'

export interface ClearCacheModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
  onOpenSlimModal?: () => void
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

const CATEGORY_BADGES: Record<string, string> = {
  versioned: 'bg-sky-50 text-sky-700 border-sky-200',
  battle_cjs: 'bg-indigo-50 text-indigo-700 border-indigo-200',
  story: 'bg-amber-50 text-amber-800 border-amber-200',
  core_ui: 'bg-emerald-50 text-emerald-700 border-emerald-200',
  event_gacha: 'bg-rose-50 text-rose-700 border-rose-200',
  audio: 'bg-purple-50 text-purple-700 border-purple-200',
  other: 'bg-slate-100 text-slate-700 border-slate-300',
}

const DEFAULT_CAUTION_CATEGORIES = new Set(['battle_cjs', 'core_ui', 'other'])

function formatTimestamp(ms?: number): string {
  if (!ms || ms <= 0) return '--'
  try {
    return new Date(ms).toLocaleString('zh-CN', { hour12: false })
  } catch {
    return '--'
  }
}

export function computeDeduplicatedSearchDeleteBytes(items: SearchItem[]): number {
  const physicalBytes = new Map<string, number>()
  for (const item of items) {
    const ownBytes = (item.asset_bytes || 0) + (item.metadata_bytes || 0)
    const primaryKey = `${item.ns}:::${item.key}`
    physicalBytes.set(primaryKey, ownBytes)
    if (item.has_alias && item.alias_key) {
      const aliasBytes = Math.max(0, (item.estimated_delete_bytes || 0) - ownBytes)
      const aliasFullKey = `${item.ns}:::${item.alias_key}`
      physicalBytes.set(aliasFullKey, aliasBytes)
    }
  }
  let total = 0
  for (const b of physicalBytes.values()) {
    total += b
  }
  return total
}

export const ClearCacheModal: React.FC<ClearCacheModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
  onOpenSlimModal,
}) => {
  const [activeTab, setActiveTab] = useState<'breakdown' | 'search' | 'reset'>('breakdown')

  // Breakdown & Task State
  const [breakdown, setBreakdown] = useState<BreakdownResponse | null>(null)
  const [taskStatus, setTaskStatus] = useState<any | null>(null)
  const [startingScan, setStartingScan] = useState(false)

  // Category Cleanup Selection State
  const [selectedCategories, setSelectedCategories] = useState<string[]>([])
  const [selectedNamespaces, setSelectedNamespaces] = useState<string[]>(['gbf'])
  const [confirmingCategoryClean, setConfirmingCategoryClean] = useState(false)
  const [submittingCategoryClean, setSubmittingCategoryClean] = useState(false)

  // Targeted Search & Delete State
  const [searchQuery, setSearchQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [searchResult, setSearchResult] = useState<SearchResponse | null>(null)
  const [searchError, setSearchError] = useState<string | null>(null)
  const [selectedSearchKeys, setSelectedSearchKeys] = useState<Record<string, SearchItem>>({})
  const [submittingInvalidate, setSubmittingInvalidate] = useState(false)

  // RAM / Full Reset State
  const [busyRam, setBusyRam] = useState(false)
  const [busyAll, setBusyAll] = useState(false)
  const [confirmingFullClear, setConfirmingFullClear] = useState(false)

  const pollTimerRef = useRef<any>(null)
  const prevBusyRef = useRef<boolean>(false)
  const prevSnapshotIdRef = useRef<string | null>(null)
  const breakdownReqSeqRef = useRef<number>(0)
  const statusReqSeqRef = useRef<number>(0)
  const searchReqSeqRef = useRef<number>(0)
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
    try {
      const data = await fetchCacheTaskStatus()
      if (reqSeq !== statusReqSeqRef.current) return
      setTaskStatus(data)
      const busy = Boolean(
        data?.is_scanning ||
          data?.is_invalidating ||
          data?.is_clearing_category ||
          data?.is_slimming ||
          data?.is_auditing
      )
      if ((prevBusyRef.current && !busy) || (!data?.is_scanning && breakdownScanningRef.current)) {
        loadBreakdown()
        onRefresh()
      }
      prevBusyRef.current = busy
    } catch {
      // Ignore transient poll errors
    }
  }, [loadBreakdown, onRefresh])

  useEffect(() => {
    if (isOpen) {
      setConfirmingCategoryClean(false)
      setConfirmingFullClear(false)
      loadBreakdown()
      loadTaskStatus()
      pollTimerRef.current = setInterval(() => {
        loadTaskStatus()
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
      searchReqSeqRef.current++
      setConfirmingCategoryClean(false)
      setSelectedCategories([])
      setSearchResult(null)
      setSelectedSearchKeys({})
    } else if (searchResult && searchResult.snapshot_id !== activeSnapshotId) {
      searchReqSeqRef.current++
      setSearchResult(null)
      setSelectedSearchKeys({})
    }
  }, [activeSnapshotId, searchResult])

  const isScanning =
    taskStatus !== null ? Boolean(taskStatus?.is_scanning) : Boolean(breakdown?.is_scanning)
  const isCleaning = Boolean(taskStatus?.is_invalidating || taskStatus?.is_clearing_category)
  const hasAnyActiveTask = Boolean(
    taskStatus?.active_task_id ||
      isScanning ||
      isCleaning ||
      taskStatus?.is_slimming ||
      taskStatus?.is_auditing ||
      taskStatus?.is_boosting
  )

  const scanProgress = taskStatus?.scan_progress || breakdown?.scan_progress
  const cleanupProgress: CleanupProgress | undefined = taskStatus?.cleanup_progress
  const lastCleanupResult: DeleteResult | undefined = taskStatus?.last_cleanup_result

  // Compute per-category stats filtered by currently selected namespaces (defaulting to ['gbf'])
  const effectiveCategoryStats = useMemo<CategoryStat[]>(() => {
    if (!snapshot?.categories) return []
    const nsList = selectedNamespaces.length > 0 ? selectedNamespaces : ['gbf']
    const nsSet = new Set(nsList)
    const matchingNamespaces = (snapshot.namespaces || []).filter((n) => nsSet.has(n.namespace))

    // If all namespaces in snapshot are selected or namespace breakdown is unavailable, return snapshot.categories
    if (
      matchingNamespaces.length === (snapshot.namespaces || []).length &&
      matchingNamespaces.length > 0
    ) {
      return snapshot.categories
    }

    const totalAssetBytesInScope = matchingNamespaces.reduce((acc, n) => acc + n.asset_bytes, 0)
    return snapshot.categories.map((cat) => {
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
  }, [snapshot, selectedNamespaces])

  const selectedCategorySummary = useMemo(() => {
    const sel = new Set(selectedCategories)
    let assetFiles = 0
    let assetBytes = 0
    let metaFiles = 0
    let estDeleteBytes = 0
    for (const c of effectiveCategoryStats) {
      if (c.selectable && sel.has(c.category)) {
        assetFiles += c.asset_files
        assetBytes += c.asset_bytes
        metaFiles += c.metadata_files
        estDeleteBytes += c.estimated_delete_bytes
      }
    }
    return { assetFiles, assetBytes, metaFiles, estDeleteBytes }
  }, [effectiveCategoryStats, selectedCategories])

  const handleStartScan = async () => {
    setStartingScan(true)
    setConfirmingCategoryClean(false)
    searchReqSeqRef.current++
    setSearchResult(null)
    setSelectedSearchKeys({})
    setSearchError(null)
    try {
      await startCacheBreakdownScan()
      prevBusyRef.current = true
      onToast('磁盘缓存分类扫描已在后台启动', 'info')
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
      onToast('已发送任务取消请求', 'info')
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
      return next.length > 0 ? next : ['gbf']
    })
  }

  const handleExecuteCategoryCleanup = async () => {
    if (!snapshot || !isSnapshotReady) {
      onToast('当前扫描快照不可用或已过期，请先重新扫描', 'error')
      return
    }
    if (selectedCategories.length === 0) {
      onToast('请先勾选需要清理的缓存类别', 'error')
      return
    }
    setSubmittingCategoryClean(true)
    try {
      const nsList = selectedNamespaces.length > 0 ? selectedNamespaces : ['gbf']
      await clearCacheCategories(snapshot.snapshot_id, selectedCategories, nsList)
      prevBusyRef.current = true
      setConfirmingCategoryClean(false)
      setSelectedCategories([])
      onToast('按分类清理任务已在后台启动', 'info')
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(`分类清理启动失败: ${e.message}`, 'error')
    } finally {
      setSubmittingCategoryClean(false)
    }
  }

  const handleSearch = async (e?: React.FormEvent) => {
    if (e) e.preventDefault()
    const trimmed = searchQuery.trim()
    if (!trimmed) {
      setSearchError('请输入 URL、相对路径或文件名关键词')
      return
    }
    if (!snapshot || !isSnapshotReady) {
      setSearchError('当前无有效扫描快照，请先点击上方“开始扫描”建立索引')
      return
    }
    const reqSeq = ++searchReqSeqRef.current
    setSearching(true)
    setSearchError(null)
    try {
      const res = await searchCacheSnapshot({
        q: trimmed,
        limit: 100,
        snapshot_id: snapshot.snapshot_id,
      })
      if (reqSeq !== searchReqSeqRef.current) return
      setSearchResult(res)
      setSelectedSearchKeys({})
    } catch (err: any) {
      if (reqSeq !== searchReqSeqRef.current) return
      setSearchError(err.message || '搜索失败')
    } finally {
      if (reqSeq === searchReqSeqRef.current) {
        setSearching(false)
      }
    }
  }

  const makeItemKey = (item: SearchItem) => `${item.ns}:::${item.key}`

  const toggleSearchItem = (item: SearchItem) => {
    const k = makeItemKey(item)
    setSelectedSearchKeys((prev) => {
      const next = { ...prev }
      if (next[k]) {
        delete next[k]
      } else {
        if (Object.keys(next).length >= 200) {
          onToast('单次定点删除最多勾选 200 项', 'info')
          return prev
        }
        next[k] = item
      }
      return next
    })
  }

  const toggleSelectAllSearch = () => {
    if (!searchResult?.items?.length) return
    const currentCount = Object.keys(selectedSearchKeys).length
    if (currentCount > 0) {
      setSelectedSearchKeys({})
      return
    }
    const next: Record<string, SearchItem> = {}
    for (const item of searchResult.items.slice(0, 200)) {
      next[makeItemKey(item)] = item
    }
    setSelectedSearchKeys(next)
  }

  const handleDeleteSearchItems = async (items: SearchItem[]) => {
    const boundSnapshotId = searchResult?.snapshot_id
    if (!boundSnapshotId || !snapshot || !isSnapshotReady || snapshot.snapshot_id !== boundSnapshotId) {
      searchReqSeqRef.current++
      setSearchResult(null)
      setSelectedSearchKeys({})
      onToast('搜索结果对应的扫描快照已过期或已变更，请重新搜索后再执行删除本地缓存', 'error')
      return
    }
    if (items.length === 0) return
    setSubmittingInvalidate(true)
    try {
      const entries = items.map((item) => ({ ns: item.ns, key: item.key }))
      await invalidateCacheEntries(boundSnapshotId, entries)
      prevBusyRef.current = true
      searchReqSeqRef.current++
      onToast(`已启动删除本地缓存任务（共 ${entries.length} 项）`, 'info')
      setSearchResult(null)
      setSelectedSearchKeys({})
      await loadTaskStatus()
      await loadBreakdown()
    } catch (e: any) {
      onToast(`删除本地缓存失败: ${e.message}`, 'error')
    } finally {
      setSubmittingInvalidate(false)
    }
  }

  const handleClearRam = async () => {
    setBusyRam(true)
    try {
      await clearCache(true)
      onToast('RAM SLRU 热缓存已清空（ResidentPool 常驻池与磁盘文件不受影响）', 'success')
      onRefresh()
    } catch (e: any) {
      onToast(`清空 RAM SLRU 失败: ${e.message}`, 'error')
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
      onToast(`全部磁盘与内存缓存已清空，共删除磁盘文件 ${count} 个`, 'success')
      await loadBreakdown()
      onRefresh()
      onClose()
    } catch (e: any) {
      onToast(`清空全部缓存失败: ${e.message}`, 'error')
    } finally {
      setBusyAll(false)
    }
  }

  const selectedSearchList = Object.values(selectedSearchKeys)
  const selectedSearchEstBytes = useMemo(
    () => computeDeduplicatedSearchDeleteBytes(selectedSearchList),
    [selectedSearchList]
  )

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-sky-600" />
          <span>磁盘缓存分类透视与定点管理</span>
        </div>
      }
      subtitle="查看本地缓存分类构成、按类别选择性清理、按路径搜索删除本地缓存或清空 RAM SLRU"
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
                    尚未扫描（打开弹窗不自动遍历磁盘）
                  </span>
                ) : snapshot.stale || !snapshot.valid ? (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-amber-100 text-amber-800 font-semibold">
                    <AlertTriangle className="w-3 h-3" />
                    快照已过期，请重新扫描
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-emerald-100 text-emerald-800 font-semibold">
                    <CheckCircle2 className="w-3 h-3" />
                    有效快照 ({formatTimestamp(snapshot.completed_at_ms)})
                  </span>
                )}
              </div>
              <div className="text-[11px] text-slate-500 font-mono break-all">
                目录: {breakdown?.cache_base || snapshot?.cache_base || '--'}
              </div>
            </div>

            <div className="flex items-center gap-2">
              {isScanning || isCleaning ? (
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
                  {snapshot ? '重新扫描磁盘' : '开始分类扫描'}
                </Button>
              )}
            </div>
          </div>

          {/* Active Scan Progress */}
          {isScanning && (
            <div className="p-2.5 bg-sky-50 border border-sky-200 rounded text-[11px] text-sky-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                阶段: <strong>{scanProgress?.phase || 'scanning'}</strong> · 已扫描文件:{' '}
                <strong>{(scanProgress?.scanned_files ?? 0).toLocaleString()}</strong> 项
              </span>
              <span>
                已统计体积: <strong>{formatBytesBinary(scanProgress?.scanned_bytes ?? 0)}</strong>
              </span>
            </div>
          )}

          {/* Active Cleanup Progress */}
          {isCleaning && cleanupProgress && (
            <div className="p-2.5 bg-blue-50 border border-blue-200 rounded text-[11px] text-blue-900 flex items-center justify-between flex-wrap gap-2 font-mono">
              <span>
                正在执行删除本地缓存: <strong>{cleanupProgress.processed_files} / {cleanupProgress.total_files}</strong>
              </span>
              <span>
                已删素材: <strong>{cleanupProgress.deleted_asset_files}</strong> 项 (
                {formatBytesBinary(cleanupProgress.freed_bytes)})
              </span>
            </div>
          )}

          {/* Snapshot Global Metrics */}
          {snapshot && (
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 pt-1 border-t border-slate-200/80 text-[11px]">
              <div>
                <span className="text-slate-500">受管素材文件 / 素材体积：</span>
                <div className="font-mono font-bold text-slate-900">
                  {snapshot.asset_files.toLocaleString()} 项 · {formatBytesBinary(snapshot.asset_bytes)}
                </div>
              </div>
              <div>
                <span className="text-slate-500">配套 .ext 元数据：</span>
                <div className="font-mono font-bold text-slate-800">
                  {snapshot.metadata_files.toLocaleString()} 项 · {formatBytesBinary(snapshot.metadata_bytes)}
                </div>
              </div>
              <div>
                <span className="text-slate-500">孤立 .ext 元数据：</span>
                <div className="font-mono font-semibold text-slate-700">
                  {snapshot.orphan_metadata_files.toLocaleString()} 项 · {formatBytesBinary(snapshot.orphan_metadata_bytes)}
                </div>
              </div>
              <div>
                <span className="text-slate-500">排除项 (.tmp / 隔离)：</span>
                <div className="font-mono text-slate-600">
                  {snapshot.excluded_files.toLocaleString()} 项 · {formatBytesBinary(snapshot.excluded_bytes)}
                </div>
              </div>
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
                <span>数据文件 {lastCleanupResult.deleted_asset_files} 项</span>
                <span>.ext {lastCleanupResult.deleted_meta_files} 项</span>
                <span>释放 {formatBytesBinary(lastCleanupResult.freed_bytes)}</span>
                {lastCleanupResult.skipped_changed_count > 0 && (
                  <span className="text-amber-700">变化跳过 {lastCleanupResult.skipped_changed_count} 项</span>
                )}
                {lastCleanupResult.already_missing_count > 0 && (
                  <span>已不存在 {lastCleanupResult.already_missing_count} 项</span>
                )}
                {lastCleanupResult.failed_count > 0 && (
                  <span className="text-red-600">失败 {lastCleanupResult.failed_count} 项</span>
                )}
              </div>
            </div>
          )}
        </div>

        {/* Navigation Tabs */}
        <div className="flex items-center gap-1.5 border-b border-slate-200 pb-2">
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
            <span>分类透视与选择清理</span>
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('search')}
            className={`px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer flex items-center gap-1.5 ${
              activeTab === 'search'
                ? 'bg-sky-600 text-white shadow-2xs'
                : 'bg-slate-100 text-slate-700 hover:bg-slate-200'
            }`}
          >
            <Search className="w-3.5 h-3.5" />
            <span>定点搜索与删除本地缓存</span>
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
            <span>RAM SLRU 与全量清理</span>
          </button>
        </div>

        {/* TAB 1: Category Breakdown & Selective Cleanup */}
        {activeTab === 'breakdown' && (
          <div className="space-y-3">
            {!snapshot ? (
              <div className="p-6 text-center bg-slate-50 border border-dashed border-slate-300 rounded-lg space-y-2">
                <div className="text-slate-700 font-semibold">尚未生成磁盘缓存分类快照</div>
                <p className="text-[11px] text-slate-500 max-w-md mx-auto">
                  点击上方“开始分类扫描”，后台单协程将只读遍历缓存目录属性，汇总 7 类素材文件数、素材体积、配套 .ext 元数据及版本目录分布。
                </p>
              </div>
            ) : (
              <>
                {/* Visual Proportion Bar */}
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between text-[11px] text-slate-500">
                    <span>分类占比（以受管数据文件字节数为分母，文件逻辑大小）：</span>
                    <span className="font-mono">
                      已选作用域素材体积:{' '}
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
                      清理目标域名空间 (默认仅 gbf，非 gbf 需显式勾选)：
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

                {/* 7-Category Table */}
                <div className="border border-slate-200 rounded-lg overflow-hidden">
                  <div className="max-h-64 overflow-y-auto divide-y divide-slate-200">
                    {effectiveCategoryStats.map((cat) => {
                      const isChecked = selectedCategories.includes(cat.category)
                      const isCaution = DEFAULT_CAUTION_CATEGORIES.has(cat.category)
                      return (
                        <div
                          key={cat.category}
                          className={`p-2.5 flex items-start justify-between gap-3 transition-colors ${
                            isChecked ? 'bg-sky-50/60' : 'bg-white hover:bg-slate-50/80'
                          }`}
                        >
                          <div className="flex items-start gap-2.5 min-w-0">
                            {cat.selectable ? (
                              <input
                                type="checkbox"
                                checked={isChecked}
                                disabled={!isSnapshotReady || hasAnyActiveTask || cat.asset_files === 0}
                                onChange={() => toggleCategory(cat.category)}
                                className="w-4 h-4 mt-0.5 rounded text-sky-600 accent-sky-600 cursor-pointer shrink-0"
                              />
                            ) : (
                              <span
                                className="w-4 h-4 mt-0.5 inline-flex items-center justify-center text-slate-400 shrink-0"
                                title="版本化资源请通过保留 K 个版本专用操作清理"
                              >
                                🔒
                              </span>
                            )}
                            <div className="space-y-0.5 min-w-0">
                              <div className="flex items-center gap-2 flex-wrap">
                                <span
                                  className={`px-1.5 py-0.5 text-[10px] font-mono font-bold rounded border ${
                                    CATEGORY_BADGES[cat.category] || CATEGORY_BADGES.other
                                  }`}
                                >
                                  {cat.category}
                                </span>
                                <span className="font-bold text-slate-900">{cat.label}</span>
                                {isCaution && (
                                  <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-slate-600 border border-slate-200">
                                    默认不选
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
                              素材体积: {formatBytesBinary(cat.asset_bytes)}{' '}
                              <span className="text-slate-500 font-normal">({cat.percentage}%)</span>
                            </div>
                            <div className="text-[11px] text-slate-500">
                              数据文件 {cat.asset_files.toLocaleString()} 项 · .ext {cat.metadata_files.toLocaleString()} 项
                            </div>
                            <div className="text-[11px] text-sky-700 font-semibold">
                              预计删除字节数: {formatBytesBinary(cat.estimated_delete_bytes)}
                            </div>
                            {cat.category === 'versioned' && onOpenSlimModal && (
                              <div className="pt-1">
                                <button
                                  type="button"
                                  onClick={() => {
                                    onClose()
                                    onOpenSlimModal()
                                  }}
                                  className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-sans font-semibold bg-sky-50 text-sky-700 hover:bg-sky-100 border border-sky-200 cursor-pointer"
                                >
                                  <FolderGit2 className="w-3 h-3" />
                                  <span>保留 K 个版本清理...</span>
                                </button>
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
                      已选 {selectedCategories.length} 个类别 · 预计删除数据文件{' '}
                      <span className="font-mono text-sky-700">
                        {selectedCategorySummary.assetFiles.toLocaleString()}
                      </span>{' '}
                      项 (含配套 .ext {selectedCategorySummary.metaFiles.toLocaleString()} 项)
                    </div>
                    <div className="text-[11px] text-slate-500">
                      所选素材体积:{' '}
                      <strong className="font-mono text-slate-700">
                        {formatBytesBinary(selectedCategorySummary.assetBytes)}
                      </strong>{' '}
                      ｜ 预计删除字节数:{' '}
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
                          确认删除本地缓存
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
                          selectedCategorySummary.assetFiles === 0
                        }
                        onClick={() => setConfirmingCategoryClean(true)}
                      >
                        删除所选类别本地缓存
                      </Button>
                    )}
                  </div>
                </div>
              </>
            )}
          </div>
        )}

        {/* TAB 2: Targeted Search & Delete */}
        {activeTab === 'search' && (
          <div className="space-y-3">
            <form onSubmit={handleSearch} className="flex items-center gap-2">
              <input
                type="text"
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                maxLength={512}
                placeholder="输入完整 URL、相对路径 (如 assets/img/sp/...) 或文件名关键词 (如 3040001000)"
                className="flex-1 bg-white border border-slate-300 rounded-lg px-3 py-1.5 text-xs font-mono text-slate-800 focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500"
              />
              <Button
                type="submit"
                variant="primary"
                size="sm"
                loading={searching}
                disabled={!isSnapshotReady || hasAnyActiveTask}
                icon={<Search className="w-3.5 h-3.5" />}
              >
                搜索索引
              </Button>
            </form>

            {!isSnapshotReady && (
              <div className="p-2.5 bg-amber-50 border border-amber-200 rounded text-[11px] text-amber-800">
                提示：搜索基于最近一次有效分类扫描快照。请先点击上方“开始分类扫描”或“重新扫描磁盘”。
              </div>
            )}

            {searchError && (
              <div className="p-2.5 bg-red-50 border border-red-200 rounded text-[11px] text-red-700">
                {searchError}
              </div>
            )}

            {searchResult && (
              <div className="space-y-2">
                <div className="flex items-center justify-between text-[11px] text-slate-600 flex-wrap gap-2">
                  <div>
                    共匹配 <strong className="font-mono">{searchResult.total_matches}</strong> 项
                    {searchResult.truncated && `（仅展示前 ${searchResult.items.length} 项）`} · 快照时间:{' '}
                    <span className="font-mono">{formatTimestamp(searchResult.completed_at_ms)}</span>
                  </div>
                  {searchResult.items.length > 0 && (
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={toggleSelectAllSearch}
                        className="text-sky-600 hover:text-sky-800 font-medium cursor-pointer"
                      >
                        {selectedSearchList.length > 0 ? '取消全选' : '全选当前结果'}
                      </button>
                      {selectedSearchList.length > 0 && (
                        <Button
                          variant="danger"
                          size="xs"
                          loading={submittingInvalidate}
                          disabled={!isSnapshotReady || hasAnyActiveTask}
                          onClick={() => handleDeleteSearchItems(selectedSearchList)}
                        >
                          删除本地缓存 (已选 {selectedSearchList.length} 项 · 预计{' '}
                          {formatBytesBinary(selectedSearchEstBytes)})
                        </Button>
                      )}
                    </div>
                  )}
                </div>

                {searchResult.items.length === 0 ? (
                  <div className="p-4 text-center bg-slate-50 border border-slate-200 rounded text-slate-500">
                    未找到匹配的本地缓存条目
                  </div>
                ) : (
                  <div className="border border-slate-200 rounded-lg overflow-hidden">
                    <div className="max-h-60 overflow-y-auto divide-y divide-slate-200">
                      {searchResult.items.map((item) => {
                        const k = makeItemKey(item)
                        const checked = Boolean(selectedSearchKeys[k])
                        return (
                          <div
                            key={k}
                            className={`p-2 flex items-center justify-between gap-2 text-[11px] ${
                              checked ? 'bg-sky-50/70' : 'bg-white hover:bg-slate-50'
                            }`}
                          >
                            <div className="flex items-start gap-2 min-w-0 flex-1">
                              <input
                                type="checkbox"
                                checked={checked}
                                onChange={() => toggleSearchItem(item)}
                                disabled={!isSnapshotReady || hasAnyActiveTask}
                                className="w-3.5 h-3.5 mt-0.5 rounded text-sky-600 accent-sky-600 cursor-pointer shrink-0"
                              />
                              <div className="min-w-0 flex-1 space-y-0.5">
                                <div className="font-mono text-slate-900 break-all">
                                  <span className="px-1 py-0.5 rounded bg-slate-100 text-slate-700 border border-slate-200 mr-1.5 text-[10px]">
                                    {item.ns}
                                  </span>
                                  {item.key}
                                </div>
                                <div className="flex items-center gap-2 flex-wrap text-slate-500">
                                  <span
                                    className={`px-1.5 py-0.2 rounded border text-[10px] font-mono ${
                                      CATEGORY_BADGES[item.category] || CATEGORY_BADGES.other
                                    }`}
                                  >
                                    {item.category_label}
                                  </span>
                                  <span className="font-mono">
                                    素材体积: {formatBytesBinary(item.asset_bytes)}
                                  </span>
                                  <span className="font-mono text-sky-700">
                                    预计删除字节数: {formatBytesBinary(item.estimated_delete_bytes)}
                                  </span>
                                  <span className="font-mono">
                                    文件修改时间: {formatTimestamp(item.file_modified_ms)}
                                  </span>
                                  {item.has_alias && (
                                    <span className="text-amber-700 bg-amber-50 border border-amber-200 px-1 rounded text-[10px]">
                                      含回退路径副本 ({item.alias_key})
                                    </span>
                                  )}
                                </div>
                              </div>
                            </div>

                            <Button
                              variant="desktop"
                              size="xs"
                              loading={submittingInvalidate}
                              disabled={!isSnapshotReady || hasAnyActiveTask}
                              onClick={() => handleDeleteSearchItems([item])}
                            >
                              删除本地缓存
                            </Button>
                          </div>
                        )
                      })}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        {/* TAB 3: RAM SLRU & Full Disk Clear */}
        {activeTab === 'reset' && (
          <div className="space-y-3">
            {/* Option 1: RAM SLRU Only */}
            <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2.5">
              <div className="flex items-start gap-2.5">
                <div className="w-7 h-7 rounded-md bg-emerald-100 text-emerald-700 flex items-center justify-center shrink-0 mt-0.5">
                  <Cpu className="w-4 h-4" />
                </div>
                <div className="space-y-1">
                  <h4 className="text-xs font-bold text-slate-900">
                    清空 RAM SLRU 热缓存
                  </h4>
                  <p className="text-[11px] text-slate-600 leading-relaxed">
                    仅清空内存 16 分片双段 SLRU 中的热点缓存项，<strong>不影响 ResidentPool 常驻池与 SSD 磁盘缓存文件</strong>。
                  </p>
                </div>
              </div>

              <div className="flex justify-end">
                <Button
                  variant="desktop"
                  size="sm"
                  loading={busyRam}
                  onClick={handleClearRam}
                >
                  清空 RAM SLRU
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
                    清空 RAM SLRU、关闭 ResidentPool 常驻池，并删除缓存目录下全部受管理静态资源与 .ext 元数据。删除后经过代理且未命中缓存的请求将重新从上游获取。
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
                      确认清空全部磁盘与内存缓存
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
            说明：删除本地缓存仅移除本地副本，不自动清除浏览器自身缓存；大小按文件逻辑字节统计。
          </span>
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
