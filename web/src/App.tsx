import React, { useState, useEffect, useCallback } from 'react'
import {
  RuntimeStatus,
  CacheStats,
  LogItem,
  ProxyCandidate,
} from './types'
import {
  fetchStatus,
  fetchConfig,
  fetchCacheStats,
  fetchLogs,
  toggleProxy,
  applyConfig,
  openCacheFolder,
  browseDirectory,
  detectACGPower,
  detectUpstream,
  checkForUpdate,
} from './api'

import { LogTerminalDrawer } from './components/modals/LogTerminalDrawer'
import { MobileGuideModal } from './components/modals/MobileGuideModal'
import { ClearCacheModal } from './components/modals/ClearCacheModal'
import { ShortcutsModal } from './components/modals/ShortcutsModal'
import { RoutingGuideModal } from './components/modals/RoutingGuideModal'
import { LatencyTestModal } from './components/modals/LatencyTestModal'
import { AuditModal } from './components/modals/AuditModal'
import { SlimModal } from './components/modals/SlimModal'
import { CaCertModal } from './components/modals/CaCertModal'
import { UpdateModal } from './components/modals/UpdateModal'
import { UpstreamSelectModal } from './components/modals/UpstreamSelectModal'
import { ShimakazeSuggestModal } from './components/modals/ShimakazeSuggestModal'

import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts'
import { isNewerVersion } from './utils/version'
import {
  CheckCircle2,
  Info,
  XCircle,
  Zap,
} from 'lucide-react'

export const App: React.FC = () => {
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null)
  const [config, setConfig] = useState<Record<string, any>>({})
  const [logs, setLogs] = useState<LogItem[]>([])
  const [loadingProxy, setLoadingProxy] = useState<boolean>(false)

  // Editable Form Inputs
  const [cacheDirInput, setCacheDirInput] = useState<string>('D:\\acgpower\\cache\\gbf\\https')
  const [upstreamInput, setUpstreamInput] = useState<string>('http://127.0.0.1:8099')
  const [portInput, setPortInput] = useState<string>('8124')
  const [ramMbInput, setRamMbInput] = useState<string>('256')

  // Modals and Drawers
  const [isLogDrawerOpen, setIsLogDrawerOpen] = useState<boolean>(false)
  const [isMobileModalOpen, setIsMobileModalOpen] = useState<boolean>(false)
  const [isClearModalOpen, setIsClearModalOpen] = useState<boolean>(false)
  const [isShortcutsModalOpen, setIsShortcutsModalOpen] = useState<boolean>(false)
  const [isRoutingModalOpen, setIsRoutingModalOpen] = useState<boolean>(false)
  const [isLatencyModalOpen, setIsLatencyModalOpen] = useState<boolean>(false)
  const [isAuditModalOpen, setIsAuditModalOpen] = useState<boolean>(false)
  const [isSlimModalOpen, setIsSlimModalOpen] = useState<boolean>(false)
  const [isUpdateModalOpen, setIsUpdateModalOpen] = useState<boolean>(false)
  const [isUpstreamSelectModalOpen, setIsUpstreamSelectModalOpen] = useState<boolean>(false)
  const [upstreamCandidates, setUpstreamCandidates] = useState<ProxyCandidate[]>([])
  const [isShimakazeSuggestOpen, setIsShimakazeSuggestOpen] = useState<boolean>(false)
  const [caModalAction, setCaModalAction] = useState<'install' | 'uninstall' | null>(null)
  const [updateInfo, setUpdateInfo] = useState<{ available: boolean; version: string } | null>(null)

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

  // Refresh data from API
  const loadState = useCallback(async () => {
    try {
      const [s, c, cs, lg] = await Promise.allSettled([
        fetchStatus(),
        fetchConfig(),
        fetchCacheStats(),
        fetchLogs(),
      ])
      if (s.status === 'fulfilled') setStatus(s.value)
      if (c.status === 'fulfilled') {
        setConfig(c.value)
        if (c.value.cache_dir) setCacheDirInput(c.value.cache_dir)
        if (c.value.upstream_proxy) setUpstreamInput(c.value.upstream_proxy)
        if (c.value.listen_port) setPortInput(String(c.value.listen_port))
        if (c.value.ram_cache_max_mb) setRamMbInput(String(c.value.ram_cache_max_mb))
      }
      if (cs.status === 'fulfilled') setCacheStats(cs.value)
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

      es.addEventListener('log', (e) => {
        try {
          const item: LogItem = JSON.parse(e.data)
          setLogs((prev) => [...prev.slice(-300), item])
        } catch {}
      })
    } catch (e) {
      console.warn('SSE connection failed, falling back to polling', e)
    }

    // Background check for newer version on startup
    checkForUpdate()
      .then((data) => {
        if (data?.error) {
          throw new Error(data.error)
        }
        if (data?.has_update && data?.latest_version) {
          setUpdateInfo({ available: true, version: data.latest_version })
        }
      })
      .catch(() => {
        fetch('https://api.github.com/repos/Sagisawa/GBF-Accelerator/releases/latest')
          .then((r) => (r.ok ? r.json() : null))
          .then((data) => {
            if (!data?.tag_name) return
            const latest = data.tag_name.replace(/^v/, '').trim()
            fetchStatus().then((cur) => {
              const current = (cur?.version || '1.8.0').replace(/^v/, '').trim()
              if (latest && isNewerVersion(latest, current)) {
                setUpdateInfo({ available: true, version: latest })
              }
            }).catch(() => {})
          })
          .catch(() => {})
      })

    const interval = setInterval(() => {
      fetchStatus().then(setStatus).catch(() => {})
      fetchCacheStats().then(setCacheStats).catch(() => {})
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
      showToast(targetRunning ? '加速代理服务已成功启动' : '加速代理服务已停止', 'success')
      loadState()
    } catch (e: any) {
      showToast(`代理操作失败: ${e.message}`, 'error')
    } finally {
      setLoadingProxy(false)
    }
  }

  // Toggle Direct Mode
  const handleToggleDirect = async () => {
    const currentDirect = Boolean(config.direct_mode ?? status?.direct_mode)
    const nextDirect = !currentDirect
    try {
      await applyConfig({ direct_mode: nextDirect })
      setConfig((prev) => ({ ...prev, direct_mode: nextDirect }))
      showToast(
        nextDirect ? '已开启直连模式 (使用本机网络，不经过上游代理)' : '已恢复上游代理转发链路',
        'info'
      )
      loadState()
    } catch (e: any) {
      showToast(`切换直连模式失败: ${e.message}`, 'error')
    }
  }

  // Toggle Shimakaze Mode
  const handleToggleShimakaze = async () => {
    const current = Boolean(config.shimakaze_mode ?? false)
    const next = !current
    try {
      await applyConfig({ shimakaze_mode: next })
      setConfig((prev) => ({ ...prev, shimakaze_mode: next }))
      showToast(next ? '已开启岛风GO 兼容优化模式' : '已关闭岛风GO 兼容优化模式', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置岛风GO模式失败: ${e.message}`, 'error')
    }
  }

  // Open Cache Folder
  const handleOpenCacheFolder = async () => {
    try {
      await openCacheFolder()
      showToast('已在系统文件管理器中打开缓存目录', 'info')
    } catch (e: any) {
      showToast(`打开目录失败: ${e.message}`, 'error')
    }
  }

  // Browse Directory
  const handleBrowseDir = async () => {
    try {
      const chosen = await browseDirectory()
      if (chosen) {
        setCacheDirInput(chosen)
        await applyConfig({ cache_dir: chosen })
        setConfig((prev) => ({ ...prev, cache_dir: chosen }))
        showToast(`缓存目录已成功更改为：${chosen}`, 'success')
        loadState()
      }
    } catch (e: any) {
      showToast(`选择目录失败: ${e.message}`, 'error')
    }
  }

  // Detect ACGPower Cache
  const handleDetectAcgp = async () => {
    try {
      const data = await detectACGPower()
      if (data && data.found && data.path) {
        setCacheDirInput(data.path)
        await applyConfig({ cache_dir: data.path })
        showToast(`已检测并关联 ACGPower 缓存目录: ${data.path}`, 'success')
      } else {
        showToast(data?.message || '未检测到正在运行的 ACGPower 或默认缓存目录', 'info')
      }
      loadState()
    } catch (e: any) {
      showToast(`检测 ACGP 失败: ${e.message}`, 'error')
    }
  }

  // Check and prompt if upstream is Shimakaze GO (port 8099)
  const checkShimakazeSuggest = (url: string) => {
    const is8099 = url.includes(':8099')
    const currentShimakaze = Boolean(config.shimakaze_mode ?? false)
    if (is8099 && !currentShimakaze) {
      setIsShimakazeSuggestOpen(true)
    }
  }

  // Enable Shimakaze mode from suggestion modal
  const handleEnableShimakaze = async () => {
    try {
      await applyConfig({ shimakaze_mode: true })
      setConfig((prev) => ({ ...prev, shimakaze_mode: true }))
      showToast('已成功启用【岛风GO 兼容优化模式】', 'success')
      loadState()
    } catch (e: any) {
      showToast(`启用岛风GO兼容模式失败: ${e.message}`, 'error')
    }
  }

  // Save Upstream Proxy
  const handleSaveUpstream = async () => {
    const trimmed = upstreamInput.trim()
    if (!trimmed) {
      showToast('上游代理地址不能为空', 'error')
      return
    }
    try {
      await applyConfig({ upstream_proxy: trimmed })
      setConfig((prev) => ({ ...prev, upstream_proxy: trimmed }))
      showToast(`上游代理地址已保存并切换至：${trimmed}`, 'success')
      loadState()
      checkShimakazeSuggest(trimmed)
    } catch (e: any) {
      showToast(`保存上游代理失败: ${e.message}`, 'error')
    }
  }

  // Auto Probe Upstream Proxy
  const handleProbeUpstream = async () => {
    try {
      const data = await detectUpstream()
      const candidates: ProxyCandidate[] = Array.isArray(data?.candidates) ? data.candidates : []

      if (candidates.length > 1) {
        setUpstreamCandidates(candidates)
        setIsUpstreamSelectModalOpen(true)
      } else if (candidates.length === 1 || (data && data.found && data.primary)) {
        const chosen = candidates.length === 1 ? candidates[0].url : data.primary
        setUpstreamInput(chosen)
        await applyConfig({ upstream_proxy: chosen })
        showToast(`已探测并应用上游代理: ${chosen}`, 'success')
        loadState()
        checkShimakazeSuggest(chosen)
      } else {
        await applyConfig({ upstream_proxy: 'auto' })
        showToast('未检测到活跃上游代理端口，已重置为 auto 模式', 'info')
        loadState()
      }
    } catch (e: any) {
      showToast(`探测上游代理失败: ${e.message}`, 'error')
    }
  }

  // Handle selection from UpstreamSelectModal
  const handleSelectUpstreamCandidate = async (candidate: ProxyCandidate) => {
    try {
      setUpstreamInput(candidate.url)
      await applyConfig({ upstream_proxy: candidate.url })
      showToast(`已切换至上游代理: ${candidate.name} (${candidate.url})`, 'success')
      loadState()
      checkShimakazeSuggest(candidate.url)
    } catch (e: any) {
      showToast(`切换上游代理失败: ${e.message}`, 'error')
    }
  }

  // Save Listen Port
  const handleSavePort = async () => {
    const p = parseInt(portInput.trim(), 10)
    if (isNaN(p) || p < 1 || p > 65535) {
      showToast('请输入 1 到 65535 之间的有效端口号', 'error')
      return
    }
    try {
      await applyConfig({ listen_port: p, port: p })
      setConfig((prev) => ({ ...prev, listen_port: p }))
      showToast(`本地监听端口已更新为 ${p} (重启代理生效)`, 'success')
      loadState()
    } catch (e: any) {
      showToast(`保存端口失败: ${e.message}`, 'error')
    }
  }

  // Reset Listen Port Default
  const handleResetPort = async () => {
    setPortInput('8124')
    try {
      await applyConfig({ listen_port: 8124, port: 8124 })
      setConfig((prev) => ({ ...prev, listen_port: 8124 }))
      showToast('监听端口已恢复默认 (8124)', 'info')
      loadState()
    } catch (e: any) {
      showToast(`恢复端口失败: ${e.message}`, 'error')
    }
  }

  // Toggle Allow LAN
  const handleToggleAllowLan = async () => {
    const current = Boolean(config.allow_lan ?? status?.allow_lan ?? false)
    const next = !current
    try {
      await applyConfig({ allow_lan: next })
      setConfig((prev) => ({ ...prev, allow_lan: next }))
      showToast(next ? '允许局域网连接已开启 (绑定 0.0.0.0)' : '局域网连接已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置局域网共享失败: ${e.message}`, 'error')
    }
  }

  // Toggle System Proxy PAC
  const handleToggleAutoPac = async () => {
    const current = Boolean(config.auto_system_proxy ?? config.auto_pac ?? true)
    const next = !current
    try {
      await applyConfig({ auto_system_proxy: next, auto_pac: next })
      setConfig((prev) => ({ ...prev, auto_system_proxy: next, auto_pac: next }))
      showToast(next ? '自动配置 Windows 系统 PAC 代理已开启' : '系统 PAC 代理已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置系统 PAC 代理失败: ${e.message}`, 'error')
    }
  }

  // Toggle Auto Start
  const handleToggleAutoStart = async () => {
    const current = Boolean(config.auto_start ?? false)
    const next = !current
    try {
      await applyConfig({ auto_start: next })
      setConfig((prev) => ({ ...prev, auto_start: next }))
      showToast(next ? '开机自启已开启' : '开机自启已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置开机自启失败: ${e.message}`, 'error')
    }
  }

  // Toggle Auto Check Update
  const handleToggleAutoUpdate = async () => {
    const current = Boolean(config.auto_check_update ?? true)
    const next = !current
    try {
      await applyConfig({ auto_check_update: next })
      setConfig((prev) => ({ ...prev, auto_check_update: next }))
      showToast(next ? '启动时自动检测新版本已开启' : '自动检测更新已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置自动更新失败: ${e.message}`, 'error')
    }
  }

  // Performance Options
  const handleToggleRamCache = async () => {
    const current = Boolean(config.enable_ram_cache ?? true)
    const next = !current
    try {
      await applyConfig({ enable_ram_cache: next })
      setConfig((prev) => ({ ...prev, enable_ram_cache: next }))
      showToast(next ? '启用内存热点缓存 (RAM Cache)' : '已关闭内存热点缓存', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置内存缓存失败: ${e.message}`, 'error')
    }
  }

  const handleApplyRamMb = async () => {
    const mb = parseInt(ramMbInput.trim(), 10)
    if (isNaN(mb) || mb < 64 || mb > 2048) {
      showToast('请输入 64 到 2048 MB 之间的有效内存上限', 'error')
      return
    }
    try {
      await applyConfig({ ram_cache_max_mb: mb })
      setConfig((prev) => ({ ...prev, ram_cache_max_mb: mb }))
      showToast(`内存缓存上限已设置为 ${mb} MB`, 'success')
      loadState()
    } catch (e: any) {
      showToast(`设置内存上限失败: ${e.message}`, 'error')
    }
  }

  const handleToggleBrowserCache = async () => {
    const current = Boolean(config.enable_browser_cache ?? false)
    const next = !current
    try {
      await applyConfig({ enable_browser_cache: next })
      setConfig((prev) => ({ ...prev, enable_browser_cache: next }))
      showToast(next ? '浏览器强缓存与渲染留存已开启' : '浏览器强缓存已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置浏览器强缓存失败: ${e.message}`, 'error')
    }
  }

  const handleToggleAutoRepair = async () => {
    const current = Boolean(config.enable_auto_repair ?? true)
    const next = !current
    try {
      await applyConfig({ enable_auto_repair: next })
      setConfig((prev) => ({ ...prev, enable_auto_repair: next }))
      showToast(next ? '自动检测并修复损坏/空缓存已开启' : '自动修复已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置自动修复失败: ${e.message}`, 'error')
    }
  }

  const handleTogglePrefetch = async () => {
    const current = Boolean(config.enable_prefetch ?? true)
    const next = !current
    try {
      await applyConfig({ enable_prefetch: next })
      setConfig((prev) => ({ ...prev, enable_prefetch: next }))
      showToast(next ? '场景素材智能预加载已开启' : '素材预加载已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置预加载失败: ${e.message}`, 'error')
    }
  }

  const handleToggleRamWarmup = async () => {
    const current = Boolean(config.enable_ram_warmup ?? true)
    const next = !current
    try {
      await applyConfig({ enable_ram_warmup: next })
      setConfig((prev) => ({ ...prev, enable_ram_warmup: next }))
      showToast(next ? '启动时预热内存缓存已开启' : '预热内存已关闭', 'info')
      loadState()
    } catch (e: any) {
      showToast(`设置预热内存失败: ${e.message}`, 'error')
    }
  }

  // Close all modals
  const handleCloseAll = () => {
    setIsLogDrawerOpen(false)
    setIsMobileModalOpen(false)
    setIsClearModalOpen(false)
    setIsShortcutsModalOpen(false)
    setIsRoutingModalOpen(false)
    setIsLatencyModalOpen(false)
    setIsAuditModalOpen(false)
    setIsSlimModalOpen(false)
    setIsUpdateModalOpen(false)
    setCaModalAction(null)
  }

  const isAnyModalOpen =
    isLogDrawerOpen ||
    isMobileModalOpen ||
    isClearModalOpen ||
    isShortcutsModalOpen ||
    isRoutingModalOpen ||
    isLatencyModalOpen ||
    isAuditModalOpen ||
    isSlimModalOpen ||
    isUpdateModalOpen ||
    caModalAction !== null

  // Keyboard Shortcuts
  useKeyboardShortcuts({
    onToggleProxy: handleToggleProxy,
    onToggleLogs: () => setIsLogDrawerOpen((prev) => !prev),
    onToggleDirect: handleToggleDirect,
    onOpenClearModal: () => setIsClearModalOpen(true),
    onOpenShortcutsModal: () => setIsShortcutsModalOpen(true),
    onCloseAll: handleCloseAll,
    isModalOpen: isAnyModalOpen,
  })

  // State derivation
  const isRunning = Boolean(status?.proxy_running)
  const isDirect = Boolean(config.direct_mode ?? status?.direct_mode ?? false)
  const isShimakaze = Boolean(config.shimakaze_mode ?? false)
  const isAllowLan = Boolean(config.allow_lan ?? status?.allow_lan ?? false)
  const isAutoPac = Boolean(config.auto_system_proxy ?? config.auto_pac ?? true)
  const isAutoStart = Boolean(config.auto_start ?? false)
  const isAutoUpdate = Boolean(config.auto_check_update ?? true)
  const isRamCache = Boolean(config.enable_ram_cache ?? true)
  const isBrowserCache = Boolean(config.enable_browser_cache ?? false)
  const isAutoRepair = Boolean(config.enable_auto_repair ?? true)
  const isPrefetch = Boolean(config.enable_prefetch ?? true)
  const isRamWarmup = Boolean(config.enable_ram_warmup ?? true)

  const isCaInstalled = Boolean(status?.ca_installed ?? true)
  const caFingerprint =
    status?.ca_fingerprint ||
    status?.ca_thumbprint ||
    '8D:7A:35:CA:F9:19:9B:C5:EE:3B:B4:0D:F9:41:15:F4:F3:57:12:65:94:3D:8B:14:D4:0A:43:19:20:B7:5E:75'

  const currentListenPort = config.listen_port ?? status?.listen_port ?? 8124
  const hitsCount = status?.requests?.total_hits ?? 0
  const ramHitsCount = status?.requests?.ram_hits ?? 0
  const downloadsCount = status?.requests?.cache_misses ?? 0
  const apisCount = status?.requests?.total_apis ?? 0
  const ramUsageMb = Math.round(cacheStats?.ram_mb ?? (status?.cache?.ram_mb ?? 0))
  const ramMaxMb = config.ram_cache_max_mb ?? 256

  const isStandalone = typeof window !== 'undefined' && (
    window.matchMedia('(display-mode: standalone)').matches ||
    window.location.search.includes('standalone') ||
    Boolean((window.navigator as any).standalone)
  )

  return (
    <div className={`min-h-screen ${isStandalone ? 'bg-white py-0 px-0' : 'bg-[#f1f5f9] py-6 px-3 sm:px-6'} flex flex-col items-center justify-start font-sans antialiased text-slate-800 selection:bg-blue-200`}>
      {/* Toast Notification */}
      {toast && (
        <div className="fixed top-5 left-1/2 -translate-x-1/2 z-50 animate-in fade-in slide-in-from-top-3 duration-200">
          <div className="px-4 py-2 rounded-lg bg-slate-900/90 text-white shadow-xl border border-slate-700/60 flex items-center gap-2 text-xs backdrop-blur-md">
            {toast.type === 'success' && <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />}
            {toast.type === 'error' && <XCircle className="w-4 h-4 text-red-400 shrink-0" />}
            {toast.type === 'info' && <Info className="w-4 h-4 text-blue-400 shrink-0" />}
            <span className="font-medium tracking-tight">{toast.msg}</span>
          </div>
        </div>
      )}

      {/* Main Desktop Window Frame */}
      <div className={`w-full ${isStandalone ? 'max-w-[820px] rounded-none shadow-none border-0' : 'max-w-[800px] rounded-lg shadow-xl border border-slate-300'} bg-white overflow-hidden flex flex-col`}>
        {/* Windows Simulated Title Bar */}
        {!isStandalone && (
          <div className="bg-white border-b border-slate-200 h-8 pl-3 pr-0 flex items-center justify-between select-none">
            <div className="flex items-center gap-2">
              <div className="w-4 h-4 rounded-full bg-emerald-600 text-white flex items-center justify-center shrink-0">
                <Zap className="w-2.5 h-2.5 fill-current" />
              </div>
              <span className="text-xs font-normal text-slate-700">GBF 加速器</span>
            </div>

            <div className="flex items-center h-full">
              <button
                type="button"
                onClick={() => showToast('GBF 加速代理正在后台运行', 'info')}
                className="w-11 h-8 flex items-center justify-center text-slate-600 hover:bg-slate-200 text-xs transition-colors"
                title="最小化"
              >
                —
              </button>
              <button
                type="button"
                onClick={() => showToast('窗口已处于最大适配尺寸', 'info')}
                className="w-11 h-8 flex items-center justify-center text-slate-600 hover:bg-slate-200 text-xs transition-colors"
                title="最大化"
              >
                □
              </button>
              <button
                type="button"
                onClick={() => showToast('如需退出请关闭浏览器标签或使用托盘菜单', 'info')}
                className="w-11 h-8 flex items-center justify-center text-slate-600 hover:bg-[#e81123] hover:text-white text-xs transition-colors"
                title="关闭"
              >
                ✕
              </button>
            </div>
          </div>
        )}

        {/* Window Client Area */}
        <div className="p-4 sm:p-5 flex flex-col space-y-3 bg-white">
          {/* 1. Header Card */}
          <div className="flex items-center justify-between gap-4 pb-1">
            <div className="flex flex-col space-y-1">
              <div className="flex items-baseline gap-2">
                <h1 className="text-lg sm:text-[19px] font-bold text-slate-900 tracking-tight">
                  碧蓝幻想 GBF 加速器
                </h1>
                <span className="text-xs font-mono text-slate-500">
                  v{status?.version || '1.8.0'}
                </span>
                {updateInfo?.available && (
                  <button
                    type="button"
                    onClick={() => setIsUpdateModalOpen(true)}
                    className="ml-1 px-1.5 py-0.5 rounded-[3px] bg-[#ffc107] hover:bg-[#e0a800] text-[#212529] text-[11px] font-bold inline-flex items-center gap-1 cursor-pointer transition-colors shadow-2xs"
                  >
                    <span>🔥</span>
                    <span>发现新版 v{updateInfo.version}</span>
                  </button>
                )}
              </div>

              <div className="flex items-center gap-2">
                <span
                  className={`text-xs font-medium ${
                    isRunning ? 'text-[#28a745]' : 'text-[#6c757d]'
                  }`}
                >
                  {isRunning
                    ? `● 运行中 (监听端口 ${currentListenPort})`
                    : '● 已停止'}
                </span>
              </div>
            </div>

            {/* Top Right Start / Stop Button */}
            <button
              type="button"
              disabled={loadingProxy}
              onClick={handleToggleProxy}
              className={`min-w-[96px] px-5 py-2 rounded-[3px] text-sm font-bold text-white shadow-xs transition-colors cursor-pointer select-none active:scale-[0.98] ${
                isRunning
                  ? 'bg-[#dc3545] hover:bg-[#c82333] active:bg-[#bd2130]'
                  : 'bg-[#28a745] hover:bg-[#218838] active:bg-[#1e7e34]'
              }`}
            >
              {loadingProxy ? '处理中...' : isRunning ? '停止加速' : '启动加速'}
            </button>
          </div>

          {/* 2. Real-time Telemetry Stats (3-Column Strip) */}
          <div className="border-y border-slate-200 py-3.5 px-2 grid grid-cols-3 text-center">
            {/* Stat 1: Cache Hits */}
            <div className="flex flex-col items-center justify-center">
              <div className="text-2xl sm:text-3xl font-bold font-sans text-[#28a745] tracking-tight tnum">
                {hitsCount.toLocaleString()}
                {ramHitsCount > 0 && (
                  <span className="text-xs font-normal text-slate-500 ml-1">
                    (内存 {ramHitsCount.toLocaleString()})
                  </span>
                )}
              </div>
              <div className="text-xs text-slate-500 mt-1 flex items-center gap-1">
                <span>⚡</span>
                <span>本地缓存命中</span>
              </div>
            </div>

            {/* Stat 2: Remote Downloads */}
            <div className="flex flex-col items-center justify-center">
              <div className="text-2xl sm:text-3xl font-bold font-sans text-[#007bff] tracking-tight tnum">
                {downloadsCount.toLocaleString()}
              </div>
              <div className="text-xs text-slate-500 mt-1 flex items-center gap-1">
                <span>📥</span>
                <span>远程下载缓存</span>
              </div>
            </div>

            {/* Stat 3: API Passthrough */}
            <div className="flex flex-col items-center justify-center">
              <div className="text-2xl sm:text-3xl font-bold font-sans text-[#6c757d] tracking-tight tnum">
                {apisCount.toLocaleString()}
              </div>
              <div className="text-xs text-slate-500 mt-1 flex items-center gap-1">
                <span>🔄</span>
                <span>游戏 API 转发</span>
              </div>
            </div>
          </div>

          {/* 3. Settings Card ("配置选项") */}
          <div className="space-y-3 pt-1">
            <h2 className="text-sm sm:text-base font-bold text-slate-900">
              配置选项
            </h2>

            {/* Field 1: Local Cache Dir */}
            <div className="space-y-1">
              <label className="text-xs text-slate-700 block">
                本地缓存目录（支持无缝复用 ACGPower 缓存）：
              </label>
              <div className="flex items-center gap-1.5 flex-wrap sm:flex-nowrap">
                <input
                  type="text"
                  value={cacheDirInput}
                  onChange={(e) => setCacheDirInput(e.target.value)}
                  className="flex-1 min-w-[200px] border border-slate-300 rounded px-2.5 py-1 text-xs font-mono bg-white text-slate-800 focus:outline-none focus:border-blue-500 shadow-2xs"
                />
                <button
                  type="button"
                  onClick={handleBrowseDir}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-3 py-1 rounded transition-colors shrink-0 cursor-pointer"
                >
                  浏览...
                </button>
                <button
                  type="button"
                  onClick={handleDetectAcgp}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded transition-colors shrink-0 cursor-pointer"
                >
                  检测 ACGP
                </button>
                <button
                  type="button"
                  onClick={() => setIsAuditModalOpen(true)}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded transition-colors shrink-0 cursor-pointer flex items-center gap-1"
                >
                  <span>🩺</span>
                  <span>一键体检缓存</span>
                </button>
                <button
                  type="button"
                  onClick={() => setIsSlimModalOpen(true)}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded transition-colors shrink-0 cursor-pointer flex items-center gap-1"
                >
                  <span>🧹</span>
                  <span>缓存安全瘦身</span>
                </button>
              </div>
            </div>

            {/* Field 2: Upstream Proxy */}
            <div className="space-y-1">
              <label className="text-xs text-slate-700 block">
                上游网络代理（Clash Verge / Clash / V2ray / 岛风GO 等）：
              </label>
              <div className="flex items-center gap-1.5 flex-wrap sm:flex-nowrap">
                <input
                  type="text"
                  disabled={isDirect}
                  value={upstreamInput}
                  onChange={(e) => setUpstreamInput(e.target.value)}
                  className="flex-1 min-w-[200px] border border-slate-300 rounded px-2.5 py-1 text-xs font-mono bg-white text-slate-800 focus:outline-none focus:border-blue-500 shadow-2xs disabled:bg-slate-100 disabled:text-slate-400"
                />
                <button
                  type="button"
                  disabled={isDirect}
                  onClick={handleSaveUpstream}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-3 py-1 rounded transition-colors shrink-0 cursor-pointer disabled:opacity-50"
                >
                  确认
                </button>
                <button
                  type="button"
                  disabled={isDirect}
                  onClick={handleProbeUpstream}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded transition-colors shrink-0 cursor-pointer disabled:opacity-50"
                >
                  自动探测
                </button>
              </div>
            </div>

            {/* Checkbox 1: Direct Mode */}
            <div className="pt-0.5">
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isDirect}
                  onChange={handleToggleDirect}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                />
                <span>直连模式（使用本机网络，不经过上游代理；仍使用本地缓存）</span>
              </label>
            </div>

            {/* Checkbox 2: Shimakaze Mode */}
            <div className="space-y-1">
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  disabled={isDirect}
                  checked={isShimakaze}
                  onChange={handleToggleShimakaze}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer disabled:opacity-50"
                />
                <span className={isDirect ? 'text-slate-400' : 'text-slate-800'}>
                  岛风GO 兼容优化模式（放宽超时、自愈重试、适配自签证书；默认关闭）
                </span>
              </label>

              {/* Shimakaze Tip Box (Shown in screenshot) */}
              {isShimakaze && !isDirect && (
                <div className="ml-6 p-2 bg-[#f0f7ff] border border-[#bae0ff] rounded text-xs text-[#1971c2] leading-relaxed">
                  提示：本软件架构升级后，日常使用可按需开启岛风GO【使用远端缓存】（可显著加快初次冷启动下载速度）；若遇游戏维护更新后新素材显示异常，在主界面点击【清理缓存】或临时关闭远端缓存即可。
                </div>
              )}
            </div>

            {/* Field 3: Local Listen Port */}
            <div className="space-y-1">
              <label className="text-xs text-slate-700 block">
                本地监听端口（默认 8124，支持自定义）：
              </label>
              <div className="flex items-center gap-2">
                <input
                  type="text"
                  value={portInput}
                  onChange={(e) => setPortInput(e.target.value)}
                  className="w-20 border border-slate-300 rounded px-2.5 py-1 text-xs font-mono bg-white text-slate-800 focus:outline-none focus:border-blue-500 shadow-2xs"
                />
                <button
                  type="button"
                  onClick={handleResetPort}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-3 py-1 rounded transition-colors cursor-pointer"
                >
                  恢复默认 (8124)
                </button>
                <button
                  type="button"
                  onClick={handleSavePort}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-3 py-1 rounded transition-colors cursor-pointer"
                >
                  保存配置
                </button>
              </div>
            </div>

            {/* Field 4: Allow LAN & Mobile Guide */}
            <div className="space-y-1">
              <div className="flex items-center gap-2 flex-wrap">
                <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={isAllowLan}
                    onChange={handleToggleAllowLan}
                    className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                  />
                  <span>
                    允许局域网连接 (Allow LAN) - 允许其他设备（iOS/iPad/安卓等）连接本代理（默认关闭）
                  </span>
                </label>
                <button
                  type="button"
                  onClick={() => setIsMobileModalOpen(true)}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2 py-0.5 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
                >
                  <span>📱</span>
                  <span>移动端/iOS 连接指引...</span>
                </button>
              </div>
              {isAllowLan && (
                <div className="ml-6 text-[11px] text-blue-600 font-medium">
                  本机局域网 IP: {status?.lan_ip || '192.168.x.x'} (端口 {currentListenPort}) | 移动设备请配置 Wi-Fi 代理为此 IP 与端口
                </div>
              )}
            </div>

            {/* Field 5: HTTPS Root CA Status */}
            <div className="space-y-1">
              <label className="text-xs text-slate-700 block">
                HTTPS 根证书状态（游戏静态资源本地解析必需）：
              </label>
              <div className="flex items-center gap-2 flex-wrap">
                <span
                  className={`text-xs font-bold ${
                    isCaInstalled ? 'text-[#28a745]' : 'text-[#dc3545]'
                  }`}
                >
                  {isCaInstalled ? '已信任 (正常工作)' : '未安装信任'}
                </span>
                <button
                  type="button"
                  onClick={() => setCaModalAction('install')}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded transition-colors cursor-pointer"
                >
                  一键安装/修复根证书
                </button>
                <button
                  type="button"
                  onClick={() => setCaModalAction('uninstall')}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded transition-colors cursor-pointer"
                >
                  一键注销/卸载根证书
                </button>
              </div>
              <div className="text-[11px] font-mono text-slate-500 pt-0.5 select-all">
                SHA-256 指纹： {caFingerprint}
              </div>
            </div>

            {/* System Checkboxes */}
            <div className="space-y-1.5 pt-1">
              <div>
                <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={isAutoPac}
                    onChange={handleToggleAutoPac}
                    className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                  />
                  <span>
                    自动配置 Windows 系统 PAC 代理（开启后浏览器无需插件，仅分流 GBF 流量）
                  </span>
                </label>
              </div>

              <div>
                <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={isAutoStart}
                    onChange={handleToggleAutoStart}
                    className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                  />
                  <span>开机自启（启动后自动缩小到系统托盘，默认关闭）</span>
                </label>
              </div>

              <div>
                <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={isAutoUpdate}
                    onChange={handleToggleAutoUpdate}
                    className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                  />
                  <span>启动时自动检测新版本（发现新版时右上角提醒，默认开启）</span>
                </label>
              </div>
            </div>
          </div>

          <hr className="border-slate-200" />

          {/* 4. Performance & System Resource Options */}
          <div className="space-y-2">
            <h3 className="text-xs text-slate-700 font-normal">
              性能与系统资源选项（默认开启；若需降低内存/显存占用可取消对应勾选）：
            </h3>

            {/* Perf 1: RAM Cache */}
            <div className="space-y-1">
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isRamCache}
                  onChange={handleToggleRamCache}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                />
                <span>
                  启用内存热点缓存 (RAM Cache) - 占用约 256MB 内存，高频静态资源 0 磁盘 I/O 直接响应
                </span>
              </label>

              <div className="ml-6 flex items-center gap-2 text-xs text-slate-700">
                <span>内存缓存上限 (MB):</span>
                <input
                  type="text"
                  value={ramMbInput}
                  onChange={(e) => setRamMbInput(e.target.value)}
                  className="w-16 border border-slate-300 rounded px-2 py-0.5 text-xs font-mono bg-white text-slate-800 focus:outline-none focus:border-blue-500 shadow-2xs"
                />
                <button
                  type="button"
                  onClick={handleApplyRamMb}
                  className="bg-[#f8f9fa] hover:bg-[#e2e6ea] border border-slate-300 text-xs text-slate-700 px-2.5 py-0.5 rounded transition-colors cursor-pointer"
                >
                  应用
                </button>
                <span className="text-slate-500 ml-2">
                  使用中 {ramUsageMb} MB / {ramMaxMb} MB
                </span>
              </div>
            </div>

            {/* Perf 2: Browser Cache */}
            <div>
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isBrowserCache}
                  onChange={handleToggleBrowserCache}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                />
                <span>
                  启用浏览器强缓存与渲染留存（仅对版本化静态资源注入 immutable，默认关闭）
                </span>
              </label>
            </div>

            {/* Perf 3: Auto Repair */}
            <div>
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isAutoRepair}
                  onChange={handleToggleAutoRepair}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                />
                <span>
                  自动检测并修复损坏/空缓存 - 自动识别并重下 0 字节损坏文件，防止黑屏卡死
                </span>
              </label>
            </div>

            {/* Perf 4: Prefetch */}
            <div>
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isPrefetch}
                  onChange={handleTogglePrefetch}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                />
                <span>
                  启用资源预加载 - 解析场景 JS 引用的素材并后台预热，首次进新副本/活动更流畅
                </span>
              </label>
            </div>

            {/* Perf 5: RAM Warmup */}
            <div>
              <label className="inline-flex items-center gap-2 text-xs text-slate-800 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={isRamWarmup}
                  onChange={handleToggleRamWarmup}
                  className="w-4 h-4 rounded text-blue-600 border-slate-300 focus:ring-0 cursor-pointer"
                />
                <span>
                  启动时预热内存缓存 - 把高频小文件预先载入 RAM，消除会话首读的磁盘延迟
                </span>
              </label>
            </div>
          </div>
        </div>

        {/* 5. Bottom Action Dock */}
        <div className="bg-[#f8fafc] border-t border-slate-200 px-3.5 py-2.5 flex items-center justify-between flex-wrap gap-1 select-none">
          <div className="flex items-center gap-1 flex-wrap">
            <button
              type="button"
              onClick={handleOpenCacheFolder}
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>📁</span>
              <span>缓存目录</span>
            </button>

            <button
              type="button"
              onClick={() => setIsClearModalOpen(true)}
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>🗑</span>
              <span>清空缓存</span>
            </button>

            <button
              type="button"
              onClick={() => setIsRoutingModalOpen(true)}
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>🌐</span>
              <span>分流说明</span>
            </button>

            <a
              href="https://github.com/Sagisawa/GBF-Accelerator"
              target="_blank"
              rel="noreferrer"
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>⭐</span>
              <span>GitHub</span>
            </a>

            <button
              type="button"
              onClick={() => setIsUpdateModalOpen(true)}
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>🔄</span>
              <span>检查更新</span>
            </button>

            <button
              type="button"
              onClick={() => setIsLatencyModalOpen(true)}
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>📶</span>
              <span>延迟测试</span>
            </button>

            <button
              type="button"
              onClick={() => setIsLogDrawerOpen((prev) => !prev)}
              className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
            >
              <span>📜</span>
              <span>实时日志</span>
            </button>
          </div>

          <button
            type="button"
            onClick={() => showToast('GBF 加速代理已在后台持续运行', 'info')}
            className="bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] border border-slate-300 text-xs text-slate-700 px-2.5 py-1 rounded-[3px] transition-colors cursor-pointer flex items-center gap-1"
          >
            <span>⬇</span>
            <span>最小化到托盘</span>
          </button>
        </div>
      </div>

      {/* Floating Modal Windows */}
      <ClearCacheModal
        isOpen={isClearModalOpen}
        onClose={() => setIsClearModalOpen(false)}
        onRefresh={loadState}
        onToast={showToast}
      />

      <MobileGuideModal
        isOpen={isMobileModalOpen}
        onClose={() => setIsMobileModalOpen(false)}
        status={status}
        onConfigUpdated={loadState}
        onToast={showToast}
      />

      <ShortcutsModal
        isOpen={isShortcutsModalOpen}
        onClose={() => setIsShortcutsModalOpen(false)}
      />

      <RoutingGuideModal
        isOpen={isRoutingModalOpen}
        onClose={() => setIsRoutingModalOpen(false)}
        listenPort={currentListenPort}
      />

      <LatencyTestModal
        isOpen={isLatencyModalOpen}
        onClose={() => setIsLatencyModalOpen(false)}
        isDirect={isDirect}
        upstreamProxy={config.upstream_proxy || status?.upstream_proxy}
      />

      <AuditModal
        isOpen={isAuditModalOpen}
        onClose={() => setIsAuditModalOpen(false)}
        onRefresh={loadState}
        onToast={showToast}
      />

      <SlimModal
        isOpen={isSlimModalOpen}
        onClose={() => setIsSlimModalOpen(false)}
        onRefresh={loadState}
        onToast={showToast}
      />

      <CaCertModal
        isOpen={caModalAction !== null}
        onClose={() => setCaModalAction(null)}
        isInstalled={isCaInstalled}
        fingerprint={caFingerprint}
        actionType={caModalAction || 'install'}
        onRefresh={loadState}
        onToast={showToast}
      />

      <UpstreamSelectModal
        isOpen={isUpstreamSelectModalOpen}
        onClose={() => setIsUpstreamSelectModalOpen(false)}
        candidates={upstreamCandidates}
        currentProxy={upstreamInput || config.upstream_proxy || status?.upstream_proxy}
        onSelect={handleSelectUpstreamCandidate}
      />

      <ShimakazeSuggestModal
        isOpen={isShimakazeSuggestOpen}
        onClose={() => setIsShimakazeSuggestOpen(false)}
        onEnable={handleEnableShimakaze}
      />

      <UpdateModal
        isOpen={isUpdateModalOpen}
        onClose={() => setIsUpdateModalOpen(false)}
        currentVersion={status?.version || '1.8.0'}
      />

      <LogTerminalDrawer
        isOpen={isLogDrawerOpen}
        onClose={() => setIsLogDrawerOpen(false)}
        logs={logs}
        onClearLogs={() => setLogs([])}
      />
    </div>
  )
}

export default App
