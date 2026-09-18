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
  openCacheFolder,
} from './api'

import { Sidebar } from './components/cockpit/Sidebar'
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
import {
  AlertTriangle,
  CheckCircle2,
  Info,
  XCircle,
  Play,
  Square,
  Zap,
  FolderOpen,
  Sparkles,
} from 'lucide-react'

export const App: React.FC = () => {
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [telemetry, setTelemetry] = useState<TelemetrySummary | null>(null)
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null)
  const [prefetch, setPrefetch] = useState<PrefetchStatus | null>(null)
  const [config, setConfig] = useState<Record<string, any>>({})
  const [logs, setLogs] = useState<LogItem[]>([])
  const [loadingProxy, setLoadingProxy] = useState<boolean>(false)
  const [activeSection, setActiveSection] = useState<string>('overview')

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
      showToast(targetRunning ? '代理加速服务已启动' : '代理加速服务已停止', 'success')
    } catch (e: any) {
      showToast(`代理操作失败: ${e.message}`, 'error')
    } finally {
      setLoadingProxy(false)
    }
  }

  // Toggle Direct Mode via shortcut or UI
  const handleToggleDirect = async () => {
    const currentDirect = Boolean(config.direct_mode ?? status?.direct_mode)
    const nextDirect = !currentDirect
    try {
      await applyConfig({ direct_mode: nextDirect })
      setConfig((prev) => ({ ...prev, direct_mode: nextDirect }))
      showToast(
        nextDirect ? '已开启日本官方直连 (Direct Mode)' : '已恢复上游代理链路',
        'info'
      )
      loadState()
    } catch (e: any) {
      showToast(`切换直连模式失败: ${e.message}`, 'error')
    }
  }

  // Open Cache folder directly
  const handleOpenCacheFolder = async () => {
    try {
      await openCacheFolder()
      showToast('已在系统文件管理器中打开缓存目录', 'info')
    } catch (e: any) {
      showToast(`打开目录失败: ${e.message}`, 'error')
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

  // Scroll to section
  const handleSelectSection = (sectionId: string) => {
    setActiveSection(sectionId)
    const element = document.getElementById(sectionId)
    if (element) {
      element.scrollIntoView({ behavior: 'smooth' })
    }
  }

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

  const isRunning = Boolean(status?.proxy_running)
  const isDirect = Boolean(config.direct_mode ?? status?.direct_mode)

  return (
    <div className="min-h-screen bg-canvas text-label-primary flex font-sans selection:bg-apple-red/30">
      {/* Toast Notification (Apple Floating Capsule) */}
      {toast && (
        <div className="fixed top-6 left-1/2 -translate-x-1/2 z-50 animate-in fade-in slide-in-from-top-3 duration-200">
          <div className="px-4 py-2.5 rounded-full bg-[#1c1c1e]/95 backdrop-blur-2xl border border-white/[0.14] shadow-apple-pop flex items-center gap-2.5 text-xs">
            {toast.type === 'success' && <CheckCircle2 className="w-4 h-4 text-apple-green shrink-0" />}
            {toast.type === 'error' && <XCircle className="w-4 h-4 text-apple-red shrink-0" />}
            {toast.type === 'info' && <Info className="w-4 h-4 text-apple-blue shrink-0" />}
            <span className="font-semibold text-white tracking-tight">{toast.msg}</span>
          </div>
        </div>
      )}

      {/* 1. Left Navigation Sidebar (Apple Music Web Desktop Standard) */}
      <Sidebar
        activeSection={activeSection}
        onSelectSection={handleSelectSection}
        status={status}
        config={config}
        onToggleLogs={() => setIsLogDrawerOpen((prev) => !prev)}
        onOpenMobileGuide={() => setIsMobileModalOpen(true)}
        onOpenClearModal={() => setIsClearModalOpen(true)}
        onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
        logCount={logs.length}
      />

      {/* Main Content Pane */}
      <div className="flex-1 flex flex-col min-w-0 lg:pl-60">
        {/* 2. Apple Music Style Persistent Top Player Bar */}
        <HeaderBar
          status={status}
          telemetry={telemetry}
          config={config}
          loading={loadingProxy}
          onToggleProxy={handleToggleProxy}
          onToggleDirect={handleToggleDirect}
          onOpenMobileGuide={() => setIsMobileModalOpen(true)}
          onToggleLogs={() => setIsLogDrawerOpen((prev) => !prev)}
          onOpenClearModal={() => setIsClearModalOpen(true)}
          onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
          onRefresh={loadState}
          logCount={logs.length}
        />

        {/* 3. Main Editorial Canvas Area */}
        <main className="flex-1 max-w-5xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-6 sm:py-8 space-y-6">
          {/* Error Alert if any */}
          {status?.last_error && (
            <div className="p-4 bg-apple-red/10 border border-apple-red/30 rounded-2xl text-xs text-apple-red flex items-center gap-3 animate-in fade-in">
              <AlertTriangle className="w-4 h-4 shrink-0" />
              <span>代理核心运行异常: {status.last_error}</span>
            </div>
          )}

          {/* Apple Music Hero Featured Banner ("今日聚焦 / 浏览" Style) */}
          <div
            id="overview"
            className="relative overflow-hidden rounded-3xl bg-gradient-to-br from-[#1c1c1e] via-[#161618] to-black border border-white/[0.08] p-6 sm:p-8 shadow-apple select-none scroll-mt-24"
          >
            {/* Ambient Lighting Gradient */}
            <div className="absolute -right-12 -top-12 w-64 h-64 bg-apple-red/15 rounded-full blur-3xl pointer-events-none" />
            <div className="absolute -left-12 -bottom-12 w-64 h-64 bg-apple-blue/10 rounded-full blur-3xl pointer-events-none" />

            <div className="relative z-10 max-w-2xl space-y-3">
              <div className="flex items-center gap-2">
                <span className="text-xs font-bold text-apple-red tracking-wider uppercase flex items-center gap-1.5">
                  <Sparkles className="w-3.5 h-3.5" />
                  GBF-ACCELERATOR · 本地极速与安全分流
                </span>
                <span className="text-[10px] font-mono px-2 py-0.5 rounded-full bg-white/[0.08] text-label-secondary border border-white/[0.06]">
                  v{status?.version || '2.0'}
                </span>
              </div>

              <h1 className="text-2xl sm:text-4xl font-bold tracking-tight text-white leading-tight">
                沉浸式空之物语，零卡顿静态加载
              </h1>

              <p className="text-xs sm:text-sm text-label-secondary leading-relaxed">
                基于 HTTP/2 多路复用与本地多级 RAM/SSD 智能缓存，静态资源微秒直达；游戏动态 API 与官方探测 100% 原样穿透，杜绝风控风险与重发隐患。
              </p>

              {/* Quick Action Pill Row */}
              <div className="flex items-center gap-3 pt-2 flex-wrap">
                <button
                  type="button"
                  disabled={loadingProxy}
                  onClick={handleToggleProxy}
                  className={`px-5 py-2.5 rounded-full font-semibold text-xs transition-all duration-200 shadow-apple active:scale-95 flex items-center gap-2 ${
                    isRunning
                      ? 'bg-apple-red text-white hover:bg-apple-redHover'
                      : 'bg-white text-black hover:bg-white/90'
                  }`}
                >
                  {isRunning ? (
                    <>
                      <Square className="w-3.5 h-3.5 fill-current" />
                      <span>停止加速服务</span>
                    </>
                  ) : (
                    <>
                      <Play className="w-3.5 h-3.5 fill-current translate-x-0.5" />
                      <span>启动加速服务</span>
                    </>
                  )}
                </button>

                <button
                  type="button"
                  onClick={handleToggleDirect}
                  className={`px-4 py-2.5 rounded-full text-xs font-medium border transition-all active:scale-95 flex items-center gap-2 ${
                    isDirect
                      ? 'bg-apple-green/15 text-apple-green border-apple-green/30'
                      : 'bg-white/[0.08] text-white border-white/[0.08] hover:bg-white/[0.14]'
                  }`}
                >
                  <Zap className="w-3.5 h-3.5" />
                  <span>{isDirect ? '日本官方直连 (开启)' : '直连模式 (未开启)'}</span>
                </button>

                <button
                  type="button"
                  onClick={handleOpenCacheFolder}
                  className="px-4 py-2.5 rounded-full text-xs font-medium bg-white/[0.08] hover:bg-white/[0.14] text-white border border-white/[0.08] transition-all active:scale-95 flex items-center gap-2"
                >
                  <FolderOpen className="w-3.5 h-3.5 text-apple-blue" />
                  <span>打开缓存目录</span>
                </button>
              </div>
            </div>
          </div>

          {/* Curated Editorial Feature Cards (Telemetry & Status) */}
          <TelemetryStrip
            status={status}
            telemetry={telemetry}
            prefetch={prefetch}
          />

          {/* Inset Group 1: Storage & Cache */}
          <CacheDeck
            status={status}
            cacheStats={cacheStats}
            onRefresh={loadState}
            onToast={showToast}
          />

          {/* Inset Group 2: Upstream & Network */}
          <UpstreamDeck
            status={status}
            config={config}
            onConfigUpdated={loadState}
            onToast={showToast}
          />

          {/* Inset Group 3: Listen Port & Multi-Device */}
          <NetworkDeck
            status={status}
            config={config}
            onConfigUpdated={loadState}
            onOpenMobileGuide={() => setIsMobileModalOpen(true)}
            onToast={showToast}
          />

          {/* Inset Group 4: Advanced Tuning */}
          <AdvancedDisclosure
            status={status}
            config={config}
            onConfigUpdated={loadState}
            onToast={showToast}
          />

          {/* Apple Music Style Footer */}
          <UtilityDock
            onOpenClearModal={() => setIsClearModalOpen(true)}
            onToggleLogs={() => setIsLogDrawerOpen((prev) => !prev)}
            onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
            logCount={logs.length}
          />
        </main>
      </div>

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
