import React, { useState, useEffect, useCallback } from 'react'
import {
  RuntimeStatus,
  TelemetrySummary,
  CacheStats,
  PrefetchStatus,
  LogItem,
} from './types'
import {
  fetchStatus,
  fetchConfig,
  fetchTelemetry,
  fetchCacheStats,
  fetchPrefetchStatus,
  fetchLogs,
  toggleProxy,
  applyConfig,
} from './api'

import { HeaderBar } from './components/cockpit/HeaderBar'
import { TelemetryStrip } from './components/cockpit/TelemetryStrip'
import { CacheDeck } from './components/cockpit/CacheDeck'
import { UpstreamDeck } from './components/cockpit/UpstreamDeck'
import { NetworkDeck } from './components/cockpit/NetworkDeck'
import { AdvancedDisclosure } from './components/cockpit/AdvancedDisclosure'
import { UtilityDock } from './components/cockpit/UtilityDock'

import { LogTerminalDrawer } from './components/modals/LogTerminalDrawer'
import { MobileGuideModal } from './components/modals/MobileGuideModal'
import { ClearCacheModal } from './components/modals/ClearCacheModal'
import { ShortcutsModal } from './components/modals/ShortcutsModal'

import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts'
import { AlertTriangle, CheckCircle2, Info, XCircle } from 'lucide-react'

export const App: React.FC = () => {
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [telemetry, setTelemetry] = useState<TelemetrySummary | null>(null)
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null)
  const [prefetch, setPrefetch] = useState<PrefetchStatus | null>(null)
  const [config, setConfig] = useState<Record<string, any>>({})
  const [logs, setLogs] = useState<LogItem[]>([])
  const [loadingProxy, setLoadingProxy] = useState<boolean>(false)

  // Modals and Drawers
  const [isLogDrawerOpen, setIsLogDrawerOpen] = useState<boolean>(false)
  const [isMobileModalOpen, setIsMobileModalOpen] = useState<boolean>(false)
  const [isClearModalOpen, setIsClearModalOpen] = useState<boolean>(false)
  const [isShortcutsModalOpen, setIsShortcutsModalOpen] = useState<boolean>(false)

  // Floating Toast
  const [toast, setToast] = useState<{
    msg: string
    type: 'success' | 'info' | 'error'
  } | null>(null)

  const showToast = useCallback(
    (msg: string, type: 'success' | 'info' | 'error' = 'info') => {
      setToast({ msg, type })
      setTimeout(() => setToast(null), 3000)
    },
    []
  )

  // Refresh functions
  const loadState = useCallback(async () => {
    try {
      const [s, c, t, cs, pf, lg] = await Promise.allSettled([
        fetchStatus(),
        fetchConfig(),
        fetchTelemetry(),
        fetchCacheStats(),
        fetchPrefetchStatus(),
        fetchLogs(),
      ])
      if (s.status === 'fulfilled') setStatus(s.value)
      if (c.status === 'fulfilled') setConfig(c.value)
      if (t.status === 'fulfilled') setTelemetry(t.value)
      if (cs.status === 'fulfilled') setCacheStats(cs.value)
      if (pf.status === 'fulfilled') setPrefetch(pf.value)
      if (lg.status === 'fulfilled') setLogs(lg.value)
    } catch (e) {
      console.error('Failed to fetch initial state', e)
    }
  }, [])

  useEffect(() => {
    loadState()

    // Establish SSE stream
    let es: EventSource | null = null
    try {
      es = new EventSource('/api/events')

      es.addEventListener('status', (e) => {
        try {
          const data = JSON.parse(e.data)
          setStatus(data)
        } catch {}
      })

      es.addEventListener('metrics', (e) => {
        try {
          const data = JSON.parse(e.data)
          if (data.telemetry) setTelemetry(data.telemetry)
        } catch {}
      })

      es.addEventListener('log', (e) => {
        try {
          const item: LogItem = JSON.parse(e.data)
          setLogs((prev) => [...prev.slice(-300), item])
        } catch {}
      })
    } catch (e) {
      console.warn('SSE connection failed, falling back to polling', e)
    }

    // Safety net polling
    const interval = setInterval(() => {
      fetchStatus().then(setStatus).catch(() => {})
      fetchTelemetry().then(setTelemetry).catch(() => {})
    }, 4000)

    return () => {
      clearInterval(interval)
      if (es) es.close()
    }
  }, [loadState])

  // Proxy Start / Stop
  const handleToggleProxy = async () => {
    if (!status) return
    setLoadingProxy(true)
    const targetRunning = !status.proxy_running
    try {
      const newStatus = await toggleProxy(targetRunning)
      setStatus(newStatus)
      showToast(targetRunning ? '代理服务已启动' : '代理服务已停止', 'success')
    } catch (e: any) {
      showToast(`代理操作失败: ${e.message}`, 'error')
    } finally {
      setLoadingProxy(false)
    }
  }

  // Toggle Direct Mode via shortcut
  const handleToggleDirect = async () => {
    const currentDirect = Boolean(config.direct_mode ?? status?.direct_mode)
    const nextDirect = !currentDirect
    try {
      await applyConfig({ direct_mode: nextDirect })
      setConfig((prev) => ({ ...prev, direct_mode: nextDirect }))
      showToast(
        nextDirect ? '已开启直连模式 (官方CDN直连)' : '已关闭直连模式 (走上游代理)',
        'info'
      )
      loadState()
    } catch (e: any) {
      showToast(`切换直连模式失败: ${e.message}`, 'error')
    }
  }

  // Close all modals / drawers
  const handleCloseAll = () => {
    setIsLogDrawerOpen(false)
    setIsMobileModalOpen(false)
    setIsClearModalOpen(false)
    setIsShortcutsModalOpen(false)
  }

  const isAnyModalOpen =
    isLogDrawerOpen || isMobileModalOpen || isClearModalOpen || isShortcutsModalOpen

  // Keyboard Shortcuts Registration
  useKeyboardShortcuts({
    onToggleProxy: handleToggleProxy,
    onToggleLogs: () => setIsLogDrawerOpen((prev) => !prev),
    onToggleDirect: handleToggleDirect,
    onOpenClearModal: () => setIsClearModalOpen(true),
    onOpenShortcutsModal: () => setIsShortcutsModalOpen(true),
    onCloseAll: handleCloseAll,
    isModalOpen: isAnyModalOpen,
  })

  return (
    <div className="min-h-screen bg-canvas text-label-primary flex flex-col font-sans selection:bg-apple-red/30">
      {/* Toast Notification (Apple Pill) */}
      {toast && (
        <div className="fixed top-6 left-1/2 -translate-x-1/2 z-50 animate-in fade-in slide-in-from-top-3 duration-200">
          <div className="px-4 py-2.5 rounded-full bg-[#1c1c1e]/95 backdrop-blur-2xl border border-white/[0.12] shadow-apple-pop flex items-center gap-2.5 text-xs">
            {toast.type === 'success' && <CheckCircle2 className="w-4 h-4 text-apple-green shrink-0" />}
            {toast.type === 'error' && <XCircle className="w-4 h-4 text-apple-red shrink-0" />}
            {toast.type === 'info' && <Info className="w-4 h-4 text-apple-blue shrink-0" />}
            <span className="font-medium text-white tracking-tight">{toast.msg}</span>
          </div>
        </div>
      )}

      {/* 1. Apple Music Style Persistent Player Bar */}
      <HeaderBar
        status={status}
        telemetry={telemetry}
        config={config}
        loading={loadingProxy}
        onToggleProxy={handleToggleProxy}
        onOpenMobileGuide={() => setIsMobileModalOpen(true)}
        onToggleLogs={() => setIsLogDrawerOpen((prev) => !prev)}
        onOpenClearModal={() => setIsClearModalOpen(true)}
        onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
        logCount={logs.length}
      />

      {/* 2. Main Apple Inset Dashboard Canvas */}
      <main className="flex-1 max-w-5xl w-full mx-auto px-4 sm:px-6 py-6 space-y-5">
        {/* Error Alert if any */}
        {status?.last_error && (
          <div className="p-4 bg-apple-red/10 border border-apple-red/30 rounded-2xl text-xs text-apple-red flex items-center gap-3 animate-in fade-in">
            <AlertTriangle className="w-4 h-4 shrink-0" />
            <span>代理内核运行异常: {status.last_error}</span>
          </div>
        )}

        {/* Apple Curated Feature Cards (Telemetry & Status) */}
        <TelemetryStrip
          status={status}
          telemetry={telemetry}
          prefetch={prefetch}
        />

        {/* Section 1: Local Cache Storage Inset Group */}
        <CacheDeck
          status={status}
          cacheStats={cacheStats}
          onRefresh={loadState}
          onToast={showToast}
        />

        {/* Section 2: Upstream Routing Inset Group */}
        <UpstreamDeck
          status={status}
          config={config}
          onConfigUpdated={loadState}
          onToast={showToast}
        />

        {/* Section 3: Port & Multi-Device Inset Group */}
        <NetworkDeck
          status={status}
          config={config}
          onConfigUpdated={loadState}
          onOpenMobileGuide={() => setIsMobileModalOpen(true)}
          onToast={showToast}
        />

        {/* Section 4: Advanced Tuning Inset Group */}
        <AdvancedDisclosure
          status={status}
          config={config}
          onConfigUpdated={loadState}
          onToast={showToast}
        />

        {/* Bottom Apple Music Style Footer */}
        <UtilityDock
          onOpenClearModal={() => setIsClearModalOpen(true)}
          onToggleLogs={() => setIsLogDrawerOpen((prev) => !prev)}
          onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
          logCount={logs.length}
        />
      </main>

      {/* Modals & Slide-Up Drawer */}
      <LogTerminalDrawer
        isOpen={isLogDrawerOpen}
        onClose={() => setIsLogDrawerOpen(false)}
        logs={logs}
        onClearLogs={() => setLogs([])}
      />

      <MobileGuideModal
        isOpen={isMobileModalOpen}
        onClose={() => setIsMobileModalOpen(false)}
        status={status}
        onConfigUpdated={loadState}
        onToast={showToast}
      />

      <ClearCacheModal
        isOpen={isClearModalOpen}
        onClose={() => setIsClearModalOpen(false)}
        onRefresh={loadState}
        onToast={showToast}
      />

      <ShortcutsModal
        isOpen={isShortcutsModalOpen}
        onClose={() => setIsShortcutsModalOpen(false)}
      />
    </div>
  )
}

export default App
