import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Network, Check, Radio, Loader2, Zap, RefreshCw, AlertCircle } from 'lucide-react'
import { ProxyCandidate } from '../../types'
import { testLatency } from '../../api'
import { cn } from '../../utils/cn'

export interface UpstreamSelectModalProps {
  isOpen: boolean
  onClose: () => void
  candidates: ProxyCandidate[]
  currentProxy?: string
  onSelect: (candidate: ProxyCandidate) => Promise<void>
}

interface LatencyState {
  status: 'idle' | 'testing' | 'success' | 'error'
  displayMs?: number
  warmMs?: number
  coldMs?: number
  error?: string
}

const STORAGE_KEY_LATENCY_ENABLED = 'gbf_upstream_latency_test_enabled'

export const UpstreamSelectModal: React.FC<UpstreamSelectModalProps> = ({
  isOpen,
  onClose,
  candidates,
  currentProxy,
  onSelect,
}) => {
  const [selectedUrl, setSelectedUrl] = useState<string>('')
  const [submitting, setSubmitting] = useState(false)
  const [latencies, setLatencies] = useState<Record<string, LatencyState>>({})
  const [userSelected, setUserSelected] = useState(false)

  // Allow user to decide whether to enable latency testing
  const [enableTesting, setEnableTesting] = useState<boolean>(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY_LATENCY_ENABLED)
      return saved !== null ? saved === 'true' : true
    } catch {
      return true
    }
  })

  // Session ref to avoid stale async state updates when disabled or retested
  const sessionRef = useRef(0)

  const handleToggleTesting = (enabled: boolean) => {
    setEnableTesting(enabled)
    try {
      localStorage.setItem(STORAGE_KEY_LATENCY_ENABLED, String(enabled))
    } catch {}

    if (!enabled) {
      sessionRef.current += 1
      setLatencies({})
    } else if (candidates.length > 0) {
      runBatchTests(candidates)
    }
  }

  // Run latency tests for all candidates concurrently
  const runBatchTests = (cands: ProxyCandidate[]) => {
    if (cands.length === 0 || !enableTesting) return

    const currentSession = sessionRef.current + 1
    sessionRef.current = currentSession

    const initialMap: Record<string, LatencyState> = {}
    cands.forEach((c) => {
      initialMap[c.url] = { status: 'testing' }
    })
    setLatencies(initialMap)

    cands.forEach(async (cand) => {
      try {
        const res = await testLatency(cand.url, true)
        if (sessionRef.current !== currentSession) return

        if (res && res.ok) {
          // Strictly exclude cold connect handshake; use warm keep-alive RTT
          const warmMs = res.warm_min_ms > 0 ? Math.round(res.warm_min_ms) : undefined
          const coldMs = res.cold_ms !== undefined ? Math.round(res.cold_ms) : undefined
          const displayMs = warmMs !== undefined ? warmMs : (coldMs ?? 0)

          setLatencies((prev) => ({
            ...prev,
            [cand.url]: { status: 'success', displayMs, warmMs, coldMs },
          }))
        } else {
          setLatencies((prev) => ({
            ...prev,
            [cand.url]: { status: 'error', error: res?.error || '连接失败' },
          }))
        }
      } catch (err: any) {
        if (sessionRef.current !== currentSession) return
        setLatencies((prev) => ({
          ...prev,
          [cand.url]: { status: 'error', error: err?.message || '请求超时' },
        }))
      }
    })
  }

  useEffect(() => {
    if (isOpen && candidates.length > 0) {
      setUserSelected(false)
      const match = candidates.find((c) => c.url.toLowerCase() === currentProxy?.toLowerCase())
      setSelectedUrl(match ? match.url : candidates[0].url)

      if (enableTesting) {
        runBatchTests(candidates)
      } else {
        setLatencies({})
      }
    } else if (!isOpen) {
      sessionRef.current += 1
      setLatencies({})
    }
  }, [isOpen, candidates, currentProxy, enableTesting])

  // Compute best latency candidate (based on pure Keep-Alive RTT)
  const successfulCandidates = candidates
    .map((c) => ({ url: c.url, ms: latencies[c.url]?.displayMs }))
    .filter((x): x is { url: string; ms: number } => typeof x.ms === 'number')

  const minLatency =
    successfulCandidates.length > 0
      ? Math.min(...successfulCandidates.map((x) => x.ms))
      : null

  const isAllTested =
    enableTesting &&
    candidates.length > 0 &&
    candidates.every(
      (c) => latencies[c.url]?.status === 'success' || latencies[c.url]?.status === 'error'
    )
  const isAnyTesting = enableTesting && candidates.some((c) => latencies[c.url]?.status === 'testing')

  // Auto-select lowest latency candidate if user hasn't made a manual pick and no current proxy matched
  useEffect(() => {
    if (!isOpen || userSelected || !enableTesting) return
    const match = candidates.find((c) => c.url.toLowerCase() === currentProxy?.toLowerCase())
    if (match) return

    if (isAllTested && minLatency !== null) {
      const best = candidates.find(
        (c) => latencies[c.url]?.status === 'success' && latencies[c.url]?.displayMs === minLatency
      )
      if (best) {
        setSelectedUrl(best.url)
      }
    }
  }, [isAllTested, minLatency, userSelected, isOpen, currentProxy, candidates, latencies, enableTesting])

  const handleConfirm = async () => {
    const target = candidates.find((c) => c.url === selectedUrl) || candidates[0]
    if (!target) return
    setSubmitting(true)
    try {
      await onSelect(target)
      onClose()
    } finally {
      setSubmitting(false)
    }
  }

  // Keyboard Enter shortcut to confirm
  useEffect(() => {
    if (!isOpen) return
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Enter' && !submitting) {
        e.preventDefault()
        handleConfirm()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, submitting, selectedUrl, candidates])

  const renderLatencyBadge = (candUrl: string) => {
    if (!enableTesting) return null
    const lat = latencies[candUrl]
    if (!lat || lat.status === 'idle') return null

    if (lat.status === 'testing') {
      return (
        <span className="inline-flex items-center gap-1 text-[11px] font-medium text-slate-400 bg-slate-100 px-2 py-0.5 rounded border border-slate-200">
          <Loader2 className="w-3 h-3 animate-spin text-slate-400" />
          <span>测速中</span>
        </span>
      )
    }

    if (lat.status === 'error') {
      return (
        <span
          className="inline-flex items-center gap-1 text-[11px] font-medium px-2 py-0.5 rounded bg-rose-50 text-rose-600 border border-rose-200"
          title={lat.error || '无法连通 GBF 节点'}
        >
          <AlertCircle className="w-3 h-3 text-rose-500" />
          <span>不可达</span>
        </span>
      )
    }

    const ms = lat.displayMs ?? 0
    const isLowest = minLatency !== null && ms === minLatency && successfulCandidates.length > 1

    let colorClass = 'bg-emerald-50 text-emerald-700 border-emerald-200'
    if (ms > 350) {
      colorClass = 'bg-rose-50 text-rose-700 border-rose-200'
    } else if (ms > 220) {
      colorClass = 'bg-amber-50 text-amber-700 border-amber-200'
    } else if (ms > 120) {
      colorClass = 'bg-blue-50 text-blue-700 border-blue-200'
    }

    const tooltip =
      lat.warmMs !== undefined
        ? `真实链路 RTT: ${lat.warmMs} ms (已排除首次握手建连开销)${
            lat.coldMs !== undefined ? `\n首包建连耗时: ${lat.coldMs} ms` : ''
          }`
        : `网络延迟: ${ms} ms`

    return (
      <div className="flex items-center gap-1.5" title={tooltip}>
        {isLowest && (
          <span className="inline-flex items-center gap-0.5 text-[10px] font-semibold px-1.5 py-0.5 rounded bg-amber-100 text-amber-800 border border-amber-300 shadow-2xs">
            <Zap className="w-2.5 h-2.5 text-amber-600 fill-amber-600" />
            <span>最优</span>
          </span>
        )}
        <span className={cn('inline-flex items-center text-xs font-mono font-semibold px-2 py-0.5 rounded border cursor-help', colorClass)}>
          {ms} ms
        </span>
      </div>
    )
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Network className="w-4 h-4 text-blue-600" />
          <span>选择上游网络代理</span>
        </div>
      }
      subtitle={`检测到本机有 ${candidates.length} 个活跃代理端口，请选择要使用的服务`}
      maxWidth="max-w-lg"
    >
      <div className="space-y-3">
        {/* Testing Preference & Actions Header */}
        <div className="flex items-center justify-between text-[11px] text-slate-500 px-0.5 bg-slate-50/80 p-2 rounded border border-slate-200/80">
          <label className="flex items-center gap-1.5 cursor-pointer select-none text-slate-700 hover:text-slate-900">
            <input
              type="checkbox"
              checked={enableTesting}
              onChange={(e) => handleToggleTesting(e.target.checked)}
              className="w-3.5 h-3.5 rounded border-slate-300 text-blue-600 focus:ring-blue-500 cursor-pointer"
            />
            <span className="font-medium">测速选优</span>
            <span className="text-[10px] text-slate-400 font-normal">（测试 GBF 链路 RTT，已排除首次握手）</span>
          </label>

          {enableTesting && (
            <button
              type="button"
              onClick={() => runBatchTests(candidates)}
              disabled={isAnyTesting}
              className="inline-flex items-center gap-1 text-blue-600 hover:text-blue-700 disabled:text-slate-400 font-medium transition-colors cursor-pointer shrink-0"
              title="重新测试所有候选代理延迟"
            >
              <RefreshCw className={cn('w-3 h-3', isAnyTesting && 'animate-spin')} />
              <span>重新测速</span>
            </button>
          )}
        </div>

        {/* Candidate List */}
        <div className="space-y-2 max-h-[320px] overflow-y-auto pr-0.5">
          {candidates.map((cand) => {
            const isSelected = cand.url === selectedUrl
            const isCurrent = cand.url.toLowerCase() === currentProxy?.toLowerCase()

            return (
              <div
                key={cand.url}
                onClick={() => {
                  setSelectedUrl(cand.url)
                  setUserSelected(true)
                }}
                onDoubleClick={handleConfirm}
                className={cn(
                  'flex items-center justify-between p-3 rounded-lg border transition-all cursor-pointer select-none text-left',
                  isSelected
                    ? 'border-blue-500 bg-blue-50/70 shadow-2xs ring-1 ring-blue-500/30'
                    : 'border-slate-200 bg-white hover:border-slate-300 hover:bg-slate-50'
                )}
              >
                <div className="flex items-center gap-3 min-w-0 flex-1 mr-2">
                  <div
                    className={cn(
                      'w-4 h-4 rounded-full border flex items-center justify-center shrink-0 transition-colors',
                      isSelected
                        ? 'border-blue-600 bg-blue-600 text-white'
                        : 'border-slate-300 bg-white'
                    )}
                  >
                    {isSelected && <div className="w-1.5 h-1.5 rounded-full bg-white" />}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-xs font-semibold text-slate-800 truncate flex items-center gap-1.5">
                      <span>{cand.name}</span>
                      {isCurrent && (
                        <span className="text-[10px] font-normal px-1.5 py-0.2 bg-emerald-100 text-emerald-700 rounded border border-emerald-200">
                          当前使用
                        </span>
                      )}
                    </div>
                    <div className="text-[11px] font-mono text-slate-500 truncate mt-0.5">
                      {cand.url}
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2 shrink-0">
                  {renderLatencyBadge(cand.url)}
                  {isSelected && (
                    <Check className="w-4 h-4 text-blue-600 shrink-0 ml-1" />
                  )}
                </div>
              </div>
            )
          })}
        </div>

        <div className="text-[11px] text-slate-500 bg-slate-50 p-2.5 rounded border border-slate-200 flex items-start gap-1.5">
          <Radio className="w-3.5 h-3.5 text-blue-600 shrink-0 mt-0.5" />
          <span>选定后将即刻断开旧上游连接池，切换到所选代理地址并保存配置。</span>
        </div>

        <div className="pt-2 flex justify-end gap-2 border-t border-slate-100">
          <Button
            variant="desktop"
            size="sm"
            onClick={onClose}
            disabled={submitting}
          >
            取消
          </Button>
          <Button
            variant="primary"
            size="sm"
            onClick={handleConfirm}
            loading={submitting}
          >
            确认选用
          </Button>
        </div>
      </div>
    </Modal>
  )
}
