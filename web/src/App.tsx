import React, { useState, useEffect, useCallback, useRef } from 'react'
import {
  RuntimeStatus,
  CacheStats,
  LogItem,
  ProxyCandidate,
  UpstreamRuntimeStatus,
} from './types'
import {
  fetchStatus,
  fetchConfig,
  fetchCacheStats,
  fetchLogs,
  toggleProxy,
  applyConfig,
  applyConfigKeepAlive,
  openCacheFolder,
  browseDirectory,
  detectACGPower,
  detectUpstream,
  fetchUpstreamStatus,
  checkForUpdate,
  quitApp,
} from './api'

import { Modal } from './components/common/Modal'
import { LiveLogsWindow } from './components/logs/LiveLogsWindow'
import { MobileGuideModal } from './components/modals/MobileGuideModal'
import { ClearCacheModal } from './components/modals/ClearCacheModal'
import { ShortcutsModal } from './components/modals/ShortcutsModal'
import { RoutingGuideModal } from './components/modals/RoutingGuideModal'
import { LatencyTestModal } from './components/modals/LatencyTestModal'
import { AuditModal } from './components/modals/AuditModal'
import { BoostModal } from './components/modals/BoostModal'
import { CaCertModal } from './components/modals/CaCertModal'
import { UpdateModal } from './components/modals/UpdateModal'
import { UpstreamSelectModal } from './components/modals/UpstreamSelectModal'
import { ShimakazeSuggestModal } from './components/modals/ShimakazeSuggestModal'
import { HealthCheckModal } from './components/modals/HealthCheckModal'
import { AndroidPatchPanel } from './components/AndroidPatchPanel'
import { useI18n } from './i18n'

import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts'
import { isNewerVersion } from './utils/version'
import { shouldAutoOpenCaGuide } from './utils/caGuide'
import {
  CheckCircle2,
  Info,
  XCircle,
  AlertTriangle,
  Zap,
  Power,
  Network,
  Smartphone,
} from 'lucide-react'

const RuntimeUptime: React.FC<{ baseSeconds: number; running: boolean }> = ({ baseSeconds, running }) => {
  const [displaySeconds, setDisplaySeconds] = useState(() => Math.max(0, Math.floor(baseSeconds)))

  useEffect(() => {
    const base = Math.max(0, Math.floor(baseSeconds))
    const startedAt = Date.now()

    const refresh = () => {
      if (!running) {
        setDisplaySeconds(base)
        return
      }
      setDisplaySeconds(base + Math.max(0, Math.floor((Date.now() - startedAt) / 1000)))
    }

    refresh()
    if (!running) return

    const timer = window.setInterval(refresh, 1000)
    return () => window.clearInterval(timer)
  }, [baseSeconds, running])

  const hours = Math.floor(displaySeconds / 3600)
  const minutes = Math.floor((displaySeconds % 3600) / 60)
  const seconds = displaySeconds % 60

  return <>{hours}h {minutes}m {seconds}s</>
}

const RealtimeRequestHud: React.FC<{ status: RuntimeStatus | null; running: boolean }> = ({ status, running }) => {
  const { t } = useI18n()
  const [counts, setCounts] = useState(() => ({
    totalHits: running ? (status?.requests?.total_hits ?? 0) : 0,
    ramHits: running ? (status?.requests?.ram_hits ?? 0) : 0,
    downloads: running ? (status?.requests?.cache_misses ?? 0) : 0,
    apis: running ? (status?.requests?.total_apis ?? 0) : 0,
  }))

  useEffect(() => {
    if (!running) return
    setCounts({
      totalHits: status?.requests?.total_hits ?? 0,
      ramHits: status?.requests?.ram_hits ?? 0,
      downloads: status?.requests?.cache_misses ?? 0,
      apis: status?.requests?.total_apis ?? 0,
    })
  }, [running, status?.requests?.total_hits, status?.requests?.ram_hits, status?.requests?.cache_misses, status?.requests?.total_apis])

  useEffect(() => {
    const onMetrics = (event: Event) => {
      const data = (event as CustomEvent).detail
      const requests = data?.requests
      if (!requests || !running) return
      setCounts((prev) => ({
        totalHits: typeof requests.total_hits === 'number' ? requests.total_hits : prev.totalHits,
        ramHits: typeof requests.ram_hits === 'number' ? requests.ram_hits : prev.ramHits,
        downloads: typeof requests.cache_misses === 'number' ? requests.cache_misses : prev.downloads,
        apis: typeof requests.total_apis === 'number' ? requests.total_apis : prev.apis,
      }))
    }

    window.addEventListener('gbf-metrics', onMetrics)
    return () => window.removeEventListener('gbf-metrics', onMetrics)
  }, [running])

  return (
    <div className="grid grid-cols-3 gap-2.5 sm:gap-4 pt-1">
      <div className="bg-slate-50/80 border border-slate-200/90 rounded-xl px-4 sm:px-5 py-3 sm:py-3.5 flex items-center justify-between shadow-2xs hover:bg-slate-50 transition-colors">
        <div className="flex flex-col">
          <span className="text-xs sm:text-[13px] font-semibold text-slate-600 flex items-center gap-1.5">
            <span className="text-sm sm:text-base">⚡</span> {t('hud.totalHits')}
          </span>
          <div className="flex items-baseline gap-1.5 mt-1 flex-wrap">
            <span className="text-2xl sm:text-3xl font-extrabold text-emerald-600 font-mono tnum leading-tight">
              {counts.totalHits.toLocaleString()}
            </span>
            {counts.ramHits > 0 && (
              <span className="text-xs font-mono font-medium text-emerald-700 bg-emerald-50 px-1.5 py-0.5 rounded border border-emerald-200/60">
                {t('hud.ramHits', { count: counts.ramHits.toLocaleString() })}
              </span>
            )}
          </div>
        </div>
      </div>

      <div className="bg-slate-50/80 border border-slate-200/90 rounded-xl px-4 sm:px-5 py-3 sm:py-3.5 flex items-center justify-between shadow-2xs hover:bg-slate-50 transition-colors">
        <div className="flex flex-col">
          <span className="text-xs sm:text-[13px] font-semibold text-slate-600 flex items-center gap-1.5">
            <span className="text-sm sm:text-base">📥</span> {t('hud.remoteDownloads')}
          </span>
          <div className="flex items-baseline gap-1.5 mt-1">
            <span className="text-2xl sm:text-3xl font-extrabold text-sky-600 font-mono tnum leading-tight">
              {counts.downloads.toLocaleString()}
            </span>
          </div>
        </div>
      </div>

      <div className="bg-slate-50/80 border border-slate-200/90 rounded-xl px-4 sm:px-5 py-3 sm:py-3.5 flex items-center justify-between shadow-2xs hover:bg-slate-50 transition-colors">
        <div className="flex flex-col">
          <span className="text-xs sm:text-[13px] font-semibold text-slate-600 flex items-center gap-1.5">
            <span className="text-sm sm:text-base">🔄</span> {t('hud.apiForwarding')}
          </span>
          <div className="flex items-baseline gap-1.5 mt-1">
            <span className="text-2xl sm:text-3xl font-extrabold text-slate-800 font-mono tnum leading-tight">
              {counts.apis.toLocaleString()}
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

export const App: React.FC = () => {
  const { language, setLanguage, t } = useI18n()
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null)
  const [upstreamRuntime, setUpstreamRuntime] = useState<UpstreamRuntimeStatus | null>(null)
  const [config, setConfig] = useState<Record<string, any>>({})
  const [logs, setLogs] = useState<LogItem[]>([])
  const [loadingProxy, setLoadingProxy] = useState<boolean>(false)
  const [loadingBrowse, setLoadingBrowse] = useState<boolean>(false)
  const [loadingOpenFolder, setLoadingOpenFolder] = useState<boolean>(false)
  const [loadingAction, setLoadingAction] = useState<string | null>(null)
  const loadingActionRef = useRef<string | null>(null)

  // Editable Form Inputs
  const [cacheDirInput, setCacheDirInput] = useState<string>('cache/gbf/https')
  const [upstreamInput, setUpstreamInput] = useState<string>('auto')
  const [backupUpstreamInput, setBackupUpstreamInput] = useState<string>('')
  const [failoverEnabledInput, setFailoverEnabledInput] = useState<boolean>(false)
  const [failoverThresholdInput, setFailoverThresholdInput] = useState<string>('2000')
  const [failoverConsecutiveInput, setFailoverConsecutiveInput] = useState<string>('3')
  const [failoverCooldownInput, setFailoverCooldownInput] = useState<string>('60')
  const [failoverAutoRecoverInput, setFailoverAutoRecoverInput] = useState<boolean>(true)
  const [failoverNotificationInput, setFailoverNotificationInput] = useState<boolean>(true)
  const [portInput, setPortInput] = useState<string>('8124')
  const [ramMbInput, setRamMbInput] = useState<string>('256')

  // Standalone window detection and popup window opener
  const isStandaloneLogsPage =
    typeof window !== 'undefined' &&
    (window.location.pathname.startsWith('/logs') ||
      window.location.search.includes('view=logs'))

  const openLogsWindow = () => {
    const w = 1220
    const h = 740
    const left = Math.max(0, Math.floor((window.screen.width - w) / 2))
    const top = Math.max(0, Math.floor((window.screen.height - h) / 2))
    const features = `width=${w},height=${h},left=${left},top=${top},menubar=no,toolbar=no,location=no,status=no,resizable=yes,scrollbars=yes`
    const win = window.open('/logs', 'GBFLiveLogsWindow', features)
    if (win) {
      win.focus()
    } else {
      setIsLogDrawerOpen(true)
    }
  }

  // Modals and Drawers
  const [isLogDrawerOpen, setIsLogDrawerOpen] = useState<boolean>(false)
  const [isMobileModalOpen, setIsMobileModalOpen] = useState<boolean>(false)
  const [isClearModalOpen, setIsClearModalOpen] = useState<boolean>(false)
  const [isShortcutsModalOpen, setIsShortcutsModalOpen] = useState<boolean>(false)
  const [isRoutingModalOpen, setIsRoutingModalOpen] = useState<boolean>(false)
  const [isLatencyModalOpen, setIsLatencyModalOpen] = useState<boolean>(false)
  const [isHealthModalOpen, setIsHealthModalOpen] = useState<boolean>(false)
  const [isAuditModalOpen, setIsAuditModalOpen] = useState<boolean>(false)
  const [isBoostModalOpen, setIsBoostModalOpen] = useState<boolean>(false)
  const [isUpdateModalOpen, setIsUpdateModalOpen] = useState<boolean>(false)
  const [isUpstreamSelectModalOpen, setIsUpstreamSelectModalOpen] = useState<boolean>(false)
  const [upstreamCandidates, setUpstreamCandidates] = useState<ProxyCandidate[]>([])
  const [upstreamSelectTarget, setUpstreamSelectTarget] = useState<'primary' | 'backup'>('primary')
  const [isShimakazeSuggestOpen, setIsShimakazeSuggestOpen] = useState<boolean>(false)
  const [shimakazeTargetName, setShimakazeTargetName] = useState<string>('')
  const [caModalAction, setCaModalAction] = useState<'install' | 'uninstall' | null>(null)
  const [updateInfo, setUpdateInfo] = useState<{ available: boolean; version: string } | null>(null)
  const [isQuitModalOpen, setIsQuitModalOpen] = useState<boolean>(false)
  const [quitting, setQuitting] = useState<boolean>(false)
  const [isTerminated, setIsTerminated] = useState<boolean>(false)
  const [activeTab, setActiveTab] = useState<'core' | 'android'>('core')

  const isAndroid =
    status?.platform === 'android' ||
    (!status && typeof navigator !== 'undefined' && /android/i.test(navigator.userAgent))

  // Update page title dynamically when language changes
  useEffect(() => {
    if (!isStandaloneLogsPage) {
      document.title = t('header.documentTitle')
    }
  }, [language, t, isStandaloneLogsPage])

  // First-launch Root CA install guide (restores the v1.6 behavior): when the
  // runtime status first authoritatively reports the CA as not installed, auto
  // open the cert guide modal exactly once per app launch. An already-trusted
  // CA never triggers the prompt, and closing without installing does not
  // re-prompt within the same launch.
  const caGuidePromptedRef = useRef(false)
  useEffect(() => {
    if (!isAndroid && shouldAutoOpenCaGuide(status, caGuidePromptedRef.current)) {
      caGuidePromptedRef.current = true
      setCaModalAction('install')
    }
  }, [status, isAndroid])

  const handleQuitApp = async () => {
    setQuitting(true)
    try {
      await quitApp()
      setIsQuitModalOpen(false)
      setIsTerminated(true)
      showToast(t('header.quitSuccess'), 'success')
    } catch (err: any) {
      showToast(err?.message || t('header.quitFailed'), 'error')
    } finally {
      setQuitting(false)
    }
  }

  // Floating Toast
  const [toast, setToast] = useState<{
    msg: string
    type: 'success' | 'info' | 'error' | 'warning'
  } | null>(null)
  const toastTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  const showToast = useCallback(
    (msg: string, type: 'success' | 'info' | 'error' | 'warning' = 'info') => {
      if (toastTimerRef.current) {
        clearTimeout(toastTimerRef.current)
      }
      setToast({ msg, type })
      const duration = type === 'warning' || type === 'error' ? 4000 : 3000
      toastTimerRef.current = setTimeout(() => {
        setToast(null)
        toastTimerRef.current = null
      }, duration)
    },
    []
  )

  const isStandalone = typeof window !== 'undefined' && (
    window.matchMedia('(display-mode: standalone)').matches ||
    window.location.search.includes('standalone') ||
    Boolean((window.navigator as any).standalone)
  )

  // Android patch tooling is exposed only on the supported desktop hosts.
  const showAndroidPatch =
    !isAndroid &&
    (status?.platform === 'windows' ||
      (status?.platform === 'darwin' && status?.arch === 'arm64'))

  useEffect(() => {
    if ((!showAndroidPatch || isAndroid) && activeTab === 'android') {
      setActiveTab('core')
    }
  }, [showAndroidPatch, isAndroid, activeTab])

  // Refresh data from API
  const loadState = useCallback(async () => {
    try {
      const [s, c, cs, us, lg] = await Promise.allSettled([
        fetchStatus(),
        fetchConfig(),
        fetchCacheStats(),
        fetchUpstreamStatus(),
        fetchLogs(),
      ])
      if (s.status === 'fulfilled') setStatus(s.value)
      if (c.status === 'fulfilled') {
        setConfig(c.value)
        if (c.value.cache_dir) setCacheDirInput(c.value.cache_dir)
        if (c.value.upstream_proxy) setUpstreamInput(c.value.upstream_proxy)
        setBackupUpstreamInput(String(c.value.backup_upstream_proxy ?? ''))
        setFailoverEnabledInput(Boolean(c.value.enable_upstream_failover ?? false))
        setFailoverThresholdInput(String(c.value.upstream_failover_threshold_ms ?? 2000))
        setFailoverConsecutiveInput(String(c.value.upstream_failover_consecutive_failures ?? 3))
        setFailoverCooldownInput(String(c.value.upstream_failover_cooldown_seconds ?? 60))
        setFailoverAutoRecoverInput(Boolean(c.value.upstream_failover_auto_recover ?? true))
        setFailoverNotificationInput(Boolean(c.value.upstream_failover_notification ?? true))
        if (c.value.listen_port) setPortInput(String(c.value.listen_port))
        if (c.value.ram_cache_max_mb) setRamMbInput(String(c.value.ram_cache_max_mb))
        if (c.value.language) setLanguage(c.value.language, false)
      }
      if (cs.status === 'fulfilled') setCacheStats(cs.value)
      if (us.status === 'fulfilled') setUpstreamRuntime(us.value)
      if (lg.status === 'fulfilled') setLogs(lg.value)
    } catch (e) {
      console.error('Failed to fetch initial state', e)
    }
  }, [setLanguage])

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

      es.addEventListener('metrics', (e) => {
        try {
          const data = JSON.parse(e.data)
          if (data?.requests) {
            window.dispatchEvent(new CustomEvent('gbf-metrics', { detail: data }))
          }
        } catch {}
      })

      es.addEventListener('boost_progress', (e) => {
        fetchCacheStats().then(setCacheStats).catch(() => {})
        try {
          const data = JSON.parse(e.data)
          window.dispatchEvent(new CustomEvent('gbf-boost-progress', { detail: data }))
        } catch {}
      })

      es.addEventListener('boost_done', (e) => {
        fetchCacheStats().then(setCacheStats).catch(() => {})
        try {
          const data = JSON.parse(e.data)
          window.dispatchEvent(new CustomEvent('gbf-boost-done', { detail: data }))
          if (data?.state === 'completed') {
            showToast(t('toasts.boostCompleted'), 'success')
          } else if (data?.state === 'partial') {
            showToast(t('toasts.boostPartial'), 'info')
          }
        } catch {}
      })

      es.addEventListener('boost_disabled', () => {
        fetchCacheStats().then(setCacheStats).catch(() => {})
        window.dispatchEvent(new CustomEvent('gbf-boost-disabled'))
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
              const current = (cur?.version || '2.5.0').replace(/^v/, '').trim()
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
      fetchUpstreamStatus().then(setUpstreamRuntime).catch(() => {})
    }, 4000)

    const handleControlPortMigrating = () => {
      clearInterval(interval)
      if (es) {
        es.close()
        es = null
      }
    }
    window.addEventListener('gbf-control-port-migrating', handleControlPortMigrating)

    return () => {
      window.removeEventListener('gbf-control-port-migrating', handleControlPortMigrating)
      clearInterval(interval)
      if (es) es.close()
    }
  }, [loadState])

  // Save user-adjusted window dimensions and maximized state across restarts
  useEffect(() => {
    if (!isStandalone) return

    let timer: ReturnType<typeof setTimeout> | null = null
    let baselineTimer: ReturnType<typeof setTimeout> | null = null
    let isStabilized = false
    let persistedKey: string | null = null
    let unloadDispatched = false

    const readGeometry = () => {
      const w = Math.round(window.outerWidth || window.innerWidth)
      const h = Math.round(window.outerHeight || window.innerHeight)

      const isMaximized = Boolean(
        window.screen &&
        w >= window.screen.availWidth - 20 &&
        h >= window.screen.availHeight - 20
      )

      const patch: Record<string, any> = { window_maximized: isMaximized }
      if (!isMaximized && w >= 400 && h >= 400) {
        patch.window_width = w
        patch.window_height = h
      }

      return {
        patch,
        key: `${w}x${h}:${isMaximized ? 'max' : 'normal'}`,
        valid: isMaximized || (w >= 400 && h >= 400),
      }
    }

    const initial = readGeometry()
    const baselineKey = initial.key

    const saveGeometry = (useKeepAlive = false) => {
      const current = readGeometry()
      if (!current.valid || current.key === persistedKey) return

      if (useKeepAlive) {
        if (unloadDispatched) return
        unloadDispatched = true
        persistedKey = current.key
        applyConfigKeepAlive(current.patch)
      } else {
        persistedKey = current.key
        applyConfig(current.patch).catch(() => {})
      }
    }

    const scheduleSave = () => {
      if (!isStabilized) return
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        saveGeometry(false)
      }, 400)
    }

    // 1.5s stabilization window:
    // Once elapsed, re-sample current geometry to establish baseline.
    // If the user already adjusted during this window, save it.
    baselineTimer = setTimeout(() => {
      isStabilized = true
      const current = readGeometry()
      if (current.valid && current.key !== baselineKey) {
        saveGeometry(false)
      } else {
        persistedKey = current.key
      }
    }, 1500)

    const handleUnload = () => {
      saveGeometry(true)
    }

    window.addEventListener('resize', scheduleSave)
    window.addEventListener('pagehide', handleUnload)
    window.addEventListener('beforeunload', handleUnload)

    return () => {
      if (timer) clearTimeout(timer)
      if (baselineTimer) clearTimeout(baselineTimer)
      window.removeEventListener('resize', scheduleSave)
      window.removeEventListener('pagehide', handleUnload)
      window.removeEventListener('beforeunload', handleUnload)
    }
  }, [isStandalone])

  // Proxy Start / Stop
  const handleToggleProxy = async () => {
    if (!status) return
    setLoadingProxy(true)
    const targetRunning = !status.proxy_running
    try {
      const newStatus = await toggleProxy(targetRunning)
      setStatus(newStatus)
      showToast(targetRunning ? t('toasts.startSuccess') : t('toasts.stopSuccess'), 'success')
      loadState()
    } catch (e: any) {
      showToast(targetRunning ? t('toasts.startFailed', { error: e.message }) : t('toasts.stopFailed', { error: e.message }), 'error')
    } finally {
      setLoadingProxy(false)
    }
  }

  const runConfigAction = useCallback(async (id: string, task: () => Promise<void>) => {
    if (loadingActionRef.current) return
    loadingActionRef.current = id
    setLoadingAction(id)
    try {
      await task()
    } finally {
      loadingActionRef.current = null
      setLoadingAction(null)
    }
  }, [])

  const isActionLoading = (id: string) => loadingAction === id

  // Toggle Direct Mode
  const handleToggleDirect = async () => {
    const currentDirect = Boolean(config.direct_mode ?? status?.direct_mode)
    const nextDirect = !currentDirect
    await runConfigAction('direct', async () => {
      try {
        const patch: Record<string, any> = { direct_mode: nextDirect }
        if (nextDirect && failoverEnabledInput) {
          patch.enable_upstream_failover = false
          setFailoverEnabledInput(false)
        }
        await applyConfig(patch)
        setConfig((prev) => ({ ...prev, ...patch }))
        showToast(nextDirect ? t('toasts.directEnabled') : t('toasts.directDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.directFailed', { error: e.message }), 'error')
      }
    })
  }
  // Toggle Shimakaze Mode
  const handleToggleShimakaze = async () => {
    const current = Boolean(config.shimakaze_mode ?? false)
    const next = !current
    await runConfigAction('shimakaze', async () => {
      try {
        await applyConfig({ shimakaze_mode: next })
        setConfig((prev) => ({ ...prev, shimakaze_mode: next }))
        showToast(next ? t('toasts.shimakazeEnabled') : t('toasts.shimakazeDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.shimakazeFailed', { error: e.message }), 'error')
      }
    })
  }
  // Open Cache Folder
  const handleOpenCacheFolder = async () => {
    if (loadingOpenFolder) return
    setLoadingOpenFolder(true)
    try {
      await openCacheFolder()
      showToast(t('toasts.openingCacheDir'), 'info')
    } catch (e: any) {
      showToast(t('toasts.openCacheDirFailed', { error: e.message }), 'error')
    } finally {
      setTimeout(() => setLoadingOpenFolder(false), 800)
    }
  }

  // Browse Directory
  const handleBrowseDir = async () => {
    if (loadingBrowse) return

    setLoadingBrowse(true)
    try {
      const chosen = await browseDirectory()
      if (chosen) {
        await applyConfig({ cache_dir: chosen })
        setCacheDirInput(chosen)
        setConfig((prev) => ({ ...prev, cache_dir: chosen }))
        showToast(t('toasts.cacheDirUpdated', { path: chosen }), 'success')
        loadState()
      }
    } catch (e: any) {
      showToast(t('toasts.cacheDirSelectFailed', { error: e.message }), 'error')
    } finally {
      setLoadingBrowse(false)
    }
  }

  // Save manually typed Cache Directory
  const handleSaveCacheDir = async () => {
    const trimmed = cacheDirInput.trim()
    if (!trimmed) {
      showToast(t('toasts.cacheDirEmpty'), 'error')
      return
    }
    await runConfigAction('save-cache-dir', async () => {
      try {
        await applyConfig({ cache_dir: trimmed })
        setCacheDirInput(trimmed)
        setConfig((prev) => ({ ...prev, cache_dir: trimmed }))
        showToast(t('toasts.cacheDirUpdated', { path: trimmed }), 'success')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.cacheDirSelectFailed', { error: e.message }), 'error')
      }
    })
  }

  // Detect ACGPower Cache
  const handleDetectAcgp = async () => {
    await runConfigAction('detect-acgp', async () => {
      try {
        const data = await detectACGPower()
        if (data && data.found && data.path) {
          await applyConfig({ cache_dir: data.path })
          setCacheDirInput(data.path)
          showToast(t('toasts.acgpDetected', { path: data.path }), 'success')
        } else {
          showToast(data?.message || t('toasts.acgpNotFound'), 'info')
        }
        loadState()
      } catch (e: any) {
        showToast(t('toasts.acgpDetectFailed', { error: e.message }), 'error')
      }
    })
  }
  // Check and prompt if upstream is Shimakaze GO (port 8099) or ACGPower (port 8123)
  const checkShimakazeSuggest = (url: string) => {
    const is8099 = url.includes(':8099')
    const is8123 = url.includes(':8123')
    const currentShimakaze = Boolean(config.shimakaze_mode ?? false)
    if ((is8099 || is8123) && !currentShimakaze) {
      setShimakazeTargetName(is8123 ? 'ACGPower (8123)' : t('modals.shimakazeSuggest.defaultTargetShimakaze'))
      setIsShimakazeSuggestOpen(true)
    }
  }

  // Enable Shimakaze mode from suggestion modal
  const handleEnableShimakaze = async () => {
    try {
      await applyConfig({ shimakaze_mode: true })
      setConfig((prev) => ({ ...prev, shimakaze_mode: true }))
      const isAcgp = shimakazeTargetName.includes('8123')
      showToast(t('toasts.compatModeSet', { name: isAcgp ? 'ACGPower' : t('modals.shimakazeSuggest.clientShimakaze') }), 'success')
      loadState()
    } catch (e: any) {
      showToast(t('toasts.compatModeFailed', { error: e.message }), 'error')
    }
  }  // Save Upstream Proxy
  const handleSaveUpstream = async () => {
    const trimmed = upstreamInput.trim()
    if (!trimmed) {
      showToast(t('toasts.upstreamEmpty'), 'error')
      return
    }
    await runConfigAction('save-upstream', async () => {
      try {
        await applyConfig({ upstream_proxy: trimmed })
        setConfig((prev) => ({ ...prev, upstream_proxy: trimmed }))
        showToast(t('toasts.upstreamUpdated', { url: trimmed }), 'success')
        loadState()
        checkShimakazeSuggest(trimmed)
      } catch (e: any) {
        showToast(t('toasts.upstreamSaveFailed', { error: e.message }), 'error')
      }
    })
  }
  // Toggle Failover Enabled
  const handleToggleFailoverEnabled = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const checked = e.target.checked
    setFailoverEnabledInput(checked)
    if (!checked) {
      await runConfigAction('disable-failover', async () => {
        try {
          await applyConfig({ enable_upstream_failover: false })
          setConfig((prev) => ({ ...prev, enable_upstream_failover: false }))
          showToast(t('toasts.failoverDisabled'), 'info')
          loadState()
        } catch (e: any) {
          showToast(t('toasts.failoverDisableFailed', { error: e.message }), 'error')
        }
      })
    }
  }

  // Save and configure backup upstream failover
  const handleSaveFailover = async () => {
    const backup = backupUpstreamInput.trim()
    if (failoverEnabledInput && !backup) {
      showToast(t('toasts.failoverRequiresUrl'), 'error')
      return
    }

    const threshold = Number(failoverThresholdInput.trim())
    const consecutive = Number(failoverConsecutiveInput.trim())
    const cooldown = Number(failoverCooldownInput.trim())

    if (!Number.isInteger(threshold) || threshold < 0 || threshold > 60000) {
      showToast(t('toasts.thresholdRange'), 'error')
      return
    }
    if (!Number.isInteger(consecutive) || consecutive < 1 || consecutive > 10) {
      showToast(t('toasts.failuresRange'), 'error')
      return
    }
    if (!Number.isInteger(cooldown) || cooldown < 1 || cooldown > 3600) {
      showToast(t('toasts.cooldownRange'), 'error')
      return
    }

    if (backup && backup.toLowerCase() !== 'direct') {
      const lower = backup.toLowerCase()
      const supported = ['http://', 'https://', 'socks5://', 'socks5h://'].some((prefix) => lower.startsWith(prefix))
      if (!supported) {
        showToast(t('toasts.protocolSupported'), 'error')
        return
      }
    }

    await runConfigAction('save-failover', async () => {
      try {
        await applyConfig({
          backup_upstream_proxy: backup,
          enable_upstream_failover: failoverEnabledInput,
          upstream_failover_threshold_ms: threshold,
          upstream_failover_consecutive_failures: consecutive,
          upstream_failover_cooldown_seconds: cooldown,
          upstream_failover_auto_recover: failoverAutoRecoverInput,
          upstream_failover_notification: failoverNotificationInput,
        })
        setConfig((prev) => ({
          ...prev,
          backup_upstream_proxy: backup,
          enable_upstream_failover: failoverEnabledInput,
          upstream_failover_threshold_ms: threshold,
          upstream_failover_consecutive_failures: consecutive,
          upstream_failover_cooldown_seconds: cooldown,
          upstream_failover_auto_recover: failoverAutoRecoverInput,
          upstream_failover_notification: failoverNotificationInput,
        }))
        showToast(
          failoverEnabledInput
            ? t('toasts.failoverSaved')
            : t('toasts.failoverSavedDisabled'),
          'success'
        )
        loadState()
      } catch (e: any) {
        showToast(t('toasts.failoverSaveFailed', { error: e.message }), 'error')
      }
    })
  }
  // Auto Probe Upstream Proxy
  const handleProbeUpstream = async () => {
    await runConfigAction('probe-upstream', async () => {
      try {
        const data = await detectUpstream()
        const candidates: ProxyCandidate[] = Array.isArray(data?.candidates) ? data.candidates : []
        if (candidates.length > 1) {
          setUpstreamCandidates(candidates)
          setUpstreamSelectTarget('primary')
          setIsUpstreamSelectModalOpen(true)
        } else if (candidates.length === 1 || (data && data.found && data.primary)) {
          const chosen = candidates.length === 1 ? candidates[0].url : data.primary
          await applyConfig({ upstream_proxy: chosen })
          setUpstreamInput(chosen)
          showToast(t('toasts.backupProbed', { url: chosen }), 'success')
          loadState()
          checkShimakazeSuggest(chosen)
        } else {
          await applyConfig({ upstream_proxy: 'auto' })
          setUpstreamInput('auto')
          showToast(t('toasts.backupNotFound'), 'info')
          loadState()
        }
      } catch (e: any) {
        showToast(t('toasts.upstreamSaveFailed', { error: e.message }), 'error')
      }
    })
  }
  // Auto Probe Backup Upstream Proxy
  const handleProbeBackupUpstream = async () => {
    await runConfigAction('probe-backup-upstream', async () => {
      try {
        const data = await detectUpstream()
        const rawCandidates: ProxyCandidate[] = Array.isArray(data?.candidates) ? [...data.candidates] : []
        const candidates: ProxyCandidate[] = rawCandidates.filter((c) => c.url.toLowerCase() !== 'direct')
        candidates.push({
          name: t('cards.upstream.upstreamDirectFallback'),
          url: 'direct',
        })
        setUpstreamCandidates(candidates)
        setUpstreamSelectTarget('backup')
        setIsUpstreamSelectModalOpen(true)
      } catch (e: any) {
        showToast(t('toasts.backupProbeFailed', { error: e.message }), 'error')
      }
    })
  }
  // Handle selection from UpstreamSelectModal
  const handleSelectUpstreamCandidate = async (candidate: ProxyCandidate) => {
    if (upstreamSelectTarget === 'backup') {
      setBackupUpstreamInput(candidate.url)
      showToast(t('toasts.candidateSelectedBackup', { name: candidate.name, url: candidate.url }), 'success')
      return
    }
    await runConfigAction('upstream-select', async () => {
      try {
        await applyConfig({ upstream_proxy: candidate.url })
        setUpstreamInput(candidate.url)
        showToast(t('toasts.candidateSwitched', { name: candidate.name, url: candidate.url }), 'success')
        loadState()
        checkShimakazeSuggest(candidate.url)
      } catch (e: any) {
        showToast(t('toasts.candidateSwitchFailed', { error: e.message }), 'error')
      }
    })
  }
  // Save Listen Port
  const handleSavePort = async () => {
    const p = parseInt(portInput.trim(), 10)
    if (isNaN(p) || p < 1 || p > 65535) {
      showToast(t('toasts.invalidPort'), 'error')
      return
    }
    await runConfigAction('save-port', async () => {
      try {
        const resp = await applyConfig({ listen_port: p, port: p })
        if (resp.config) {
          setConfig(resp.config)
        } else {
          setConfig((prev) => ({ ...prev, listen_port: p }))
        }
        if (resp.warnings && resp.warnings.length > 0) {
          showToast(t('cards.network.portWarning', { port: p, warnings: resp.warnings.join('; ') }), 'warning')
        } else {
          showToast(isRunning ? t('cards.network.portHotReloaded', { port: p }) : t('cards.network.portSaved', { port: p }), 'success')
        }
        loadState()
      } catch (e: any) {
        showToast(t('cards.network.portSaveFailed', { error: e.message }), 'error')
      }
    })
  }
  // Reset Listen Port Default
  const handleResetPort = async () => {
    await runConfigAction('reset-port', async () => {
      try {
        const resp = await applyConfig({ listen_port: 8124, port: 8124 })
        setPortInput('8124')
        if (resp.config) {
          setConfig(resp.config)
        } else {
          setConfig((prev) => ({ ...prev, listen_port: 8124 }))
        }
        if (resp.warnings && resp.warnings.length > 0) {
          showToast(t('toasts.portResetWarning', { warnings: resp.warnings.join('; ') }), 'warning')
        } else {
          showToast(isRunning ? t('toasts.portResetRunning') : t('cards.network.portReset'), 'info')
        }
        loadState()
      } catch (e: any) {
        showToast(t('cards.network.portResetFailed', { error: e.message }), 'error')
      }
    })
  }
  // Toggle Allow LAN
  const handleToggleAllowLan = async () => {
    const current = Boolean(config.allow_lan ?? status?.allow_lan ?? false)
    const next = !current
    await runConfigAction('allow-lan', async () => {
      try {
        const resp = await applyConfig({ allow_lan: next })
        if (resp.config) {
          setConfig(resp.config)
        } else {
          setConfig((prev) => ({ ...prev, allow_lan: next }))
        }
        if (resp.warnings && resp.warnings.length > 0) {
          showToast(t('toasts.lanWarning', { warnings: resp.warnings.join('; ') }), 'warning')
        } else {
          showToast(next ? t('toasts.lanEnabled') : t('toasts.lanDisabled'), 'info')
        }
        loadState()
      } catch (e: any) {
        showToast(t('toasts.lanFailed', { error: e.message }), 'error')
      }
    })
  }
  // Toggle System Proxy PAC
  const handleToggleAutoPac = async () => {
    const current = Boolean(config.auto_system_proxy ?? config.auto_pac ?? false)
    const next = !current
    await runConfigAction('auto-pac', async () => {
      try {
        const resp = await applyConfig({ auto_system_proxy: next, auto_pac: next })
        if (resp.config) {
          setConfig(resp.config)
        } else {
          setConfig((prev) => ({ ...prev, auto_system_proxy: next, auto_pac: next }))
        }
        if (resp.warnings && resp.warnings.length > 0) {
          showToast(resp.warnings.join('; '), 'warning')
        } else {
          showToast(next ? t('cards.optim.autoSysproxyEnabled') : t('cards.optim.autoSysproxyDisabled'), 'info')
        }
        loadState()
      } catch (e: any) {
        showToast(t('toasts.autoPacFailed', { error: e.message }), 'error')
      }
    })
  }
  // Toggle Auto Start
  const handleToggleAutoStart = async () => {
    const current = Boolean(config.auto_start ?? false)
    const next = !current
    await runConfigAction('auto-start', async () => {
      try {
        const resp = await applyConfig({ auto_start: next })
        if (resp.config) {
          setConfig(resp.config)
        } else {
          setConfig((prev) => ({ ...prev, auto_start: next }))
        }
        if (resp.warnings && resp.warnings.length > 0) {
          showToast(resp.warnings.join('; '), 'warning')
        } else {
          showToast(next ? t('cards.optim.autoStartEnabled') : t('cards.optim.autoStartDisabled'), 'info')
        }
        loadState()
      } catch (e: any) {
        showToast(t('toasts.autoStartFailed', { error: e.message }), 'error')
      }
    })
  }
  // Toggle Auto Check Update
  const handleToggleAutoUpdate = async () => {
    const current = Boolean(config.auto_check_update ?? true)
    const next = !current
    await runConfigAction('auto-update', async () => {
      try {
        await applyConfig({ auto_check_update: next })
        setConfig((prev) => ({ ...prev, auto_check_update: next }))
        showToast(next ? t('cards.optim.autoCheckUpdateEnabled') : t('cards.optim.autoCheckUpdateDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.autoUpdateFailed', { error: e.message }), 'error')
      }
    })
  }
  // Performance Options
  const handleToggleRamCache = async () => {
    const current = Boolean(config.enable_ram_cache ?? true)
    const next = !current
    await runConfigAction('ram-cache', async () => {
      try {
        await applyConfig({ enable_ram_cache: next })
        setConfig((prev) => ({ ...prev, enable_ram_cache: next }))
        showToast(next ? t('cards.optim.ramCacheEnabled') : t('cards.optim.ramCacheDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.ramCacheFailed', { error: e.message }), 'error')
      }
    })
  }
  const handleApplyRamMb = async () => {
    const mb = parseInt(ramMbInput.trim(), 10)
    const maxAllowed = 65536
    if (isNaN(mb) || mb < 16 || mb > maxAllowed) {
      showToast(t('toasts.invalidRamMb', { max: maxAllowed }), 'error')
      return
    }
    await runConfigAction('ram-mb', async () => {
      try {
        await applyConfig({ ram_cache_max_mb: mb })
        setConfig((prev) => ({ ...prev, ram_cache_max_mb: mb }))
        showToast(t('toasts.ramMbUpdated', { mb, gb: Math.round((mb / 1024) * 10) / 10 }), 'success')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.ramMbFailed', { error: e.message }), 'error')
      }
    })
  }
  const handleToggleBrowserCache = async () => {
    const current = Boolean(config.enable_browser_cache ?? true)
    const next = !current
    await runConfigAction('browser-cache', async () => {
      try {
        await applyConfig({ enable_browser_cache: next })
        setConfig((prev) => ({ ...prev, enable_browser_cache: next }))
        showToast(next ? t('cards.optim.browserCacheEnabled') : t('cards.optim.browserCacheDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.browserCacheFailed', { error: e.message }), 'error')
      }
    })
  }
  const handleToggleAutoRepair = async () => {
    const current = Boolean(config.enable_auto_repair ?? true)
    const next = !current
    await runConfigAction('auto-repair', async () => {
      try {
        await applyConfig({ enable_auto_repair: next })
        setConfig((prev) => ({ ...prev, enable_auto_repair: next }))
        showToast(next ? t('cards.optim.autoRepairEnabled') : t('cards.optim.autoRepairDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.autoRepairFailed', { error: e.message }), 'error')
      }
    })
  }
  const handleTogglePrefetch = async () => {
    const current = Boolean(config.enable_prefetch ?? true)
    const next = !current
    await runConfigAction('prefetch', async () => {
      try {
        await applyConfig({ enable_prefetch: next })
        setConfig((prev) => ({ ...prev, enable_prefetch: next }))
        showToast(next ? t('cards.optim.prefetchEnabled') : t('cards.optim.prefetchDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.prefetchFailed', { error: e.message }), 'error')
      }
    })
  }
  const handleToggleRamWarmup = async () => {
    const current = Boolean(config.enable_ram_warmup ?? false)
    const next = !current
    await runConfigAction('ram-warmup', async () => {
      try {
        await applyConfig({ enable_ram_warmup: next })
        setConfig((prev) => ({ ...prev, enable_ram_warmup: next }))
        showToast(next ? t('cards.optim.ramWarmupEnabled') : t('cards.optim.ramWarmupDisabled'), 'info')
        loadState()
      } catch (e: any) {
        showToast(t('toasts.ramWarmupFailed', { error: e.message }), 'error')
      }
    })
  }
  // Close all modals
  const handleCloseAll = () => {
    setIsLogDrawerOpen(false)
    setIsMobileModalOpen(false)
    setIsClearModalOpen(false)
    setIsShortcutsModalOpen(false)
    setIsRoutingModalOpen(false)
    setIsLatencyModalOpen(false)
    setIsHealthModalOpen(false)
    setIsAuditModalOpen(false)
    setIsBoostModalOpen(false)
    setIsUpdateModalOpen(false)
    setIsUpstreamSelectModalOpen(false)
    setIsShimakazeSuggestOpen(false)
    if (!quitting) {
      setIsQuitModalOpen(false)
    }
    setCaModalAction(null)
  }

  const isAnyModalOpen =
    isLogDrawerOpen ||
    isMobileModalOpen ||
    isClearModalOpen ||
    isShortcutsModalOpen ||
    isRoutingModalOpen ||
    isLatencyModalOpen ||
    isHealthModalOpen ||
    isAuditModalOpen ||
    isBoostModalOpen ||
    isUpdateModalOpen ||
    isUpstreamSelectModalOpen ||
    isShimakazeSuggestOpen ||
    isQuitModalOpen ||
    caModalAction !== null ||
    isStandaloneLogsPage

  // Keyboard Shortcuts
  useKeyboardShortcuts({
    onToggleProxy: handleToggleProxy,
    onToggleLogs: () => {
      if (isLogDrawerOpen) {
        setIsLogDrawerOpen(false)
      } else {
        openLogsWindow()
      }
    },
    onToggleDirect: handleToggleDirect,
    onOpenClearModal: () => setIsClearModalOpen(true),
    onOpenShortcutsModal: () => setIsShortcutsModalOpen(true),
    onCloseAll: handleCloseAll,
    isModalOpen: isAnyModalOpen,
    disabled: isStandaloneLogsPage,
  })

  // If navigated to standalone logs page, render ONLY the full-screen live logs window!
  if (isStandaloneLogsPage) {
    return (
      <LiveLogsWindow
        isOpen={true}
        onClose={() => window.close()}
        logs={logs}
        onClearLogs={() => setLogs([])}
        telemetry={status?.telemetry}
        hitRatioPercent={cacheStats?.hit_ratio_percent ?? 100.0}
        isStandalone={true}
      />
    )
  }

  // State derivation
  const isRunning = Boolean(status?.proxy_running)
  const isDirect = Boolean(config.direct_mode ?? status?.direct_mode ?? false)
  const isShimakaze = Boolean(config.shimakaze_mode ?? false)
  const isAllowLan = Boolean(config.allow_lan ?? status?.allow_lan ?? false)
  const isAutoPac = Boolean(config.auto_system_proxy ?? config.auto_pac ?? false)
  const isAutoStart = Boolean(config.auto_start ?? false)
  const isAutoUpdate = Boolean(config.auto_check_update ?? true)
  const isRamCache = Boolean(config.enable_ram_cache ?? true)
  const isBrowserCache = Boolean(config.enable_browser_cache ?? true)
  const isAutoRepair = Boolean(config.enable_auto_repair ?? true)
  const isPrefetch = Boolean(config.enable_prefetch ?? true)
  const isRamWarmup = Boolean(config.enable_ram_warmup ?? false)

  const caStatusKnown = status !== null && typeof status?.ca_installed === 'boolean'
  const isCaInstalled = status?.ca_installed === true
  const caFingerprint = status?.ca_fingerprint || status?.ca_thumbprint || ''

  const currentListenPort = config.listen_port ?? status?.listen_port ?? 8124
  const ramUsageMb = Math.round(cacheStats?.ram_mb ?? (status?.cache?.ram_mb ?? 0))
  const ramMaxMb = config.ram_cache_max_mb ?? 256

  const uptimeSec = status?.uptime_seconds ?? 0
  const hitsCount = status?.requests?.total_hits ?? 0
  const ramHitsCount = status?.requests?.ram_hits ?? 0
  const downloadsCount = status?.requests?.cache_misses ?? 0
  const diskHitsCount = status?.requests?.disk_hits ?? 0
  const evaluatedAssetTotal = ramHitsCount + diskHitsCount + downloadsCount
  const ramHitPct = evaluatedAssetTotal > 0 ? Math.round((ramHitsCount / evaluatedAssetTotal) * 100) : 0
  const diskHitPct = evaluatedAssetTotal > 0 ? Math.round((diskHitsCount / evaluatedAssetTotal) * 100) : 0
  const missHitPct = evaluatedAssetTotal > 0 ? Math.max(0, 100 - ramHitPct - diskHitPct) : 0
  const totalHitRate = evaluatedAssetTotal > 0 ? Math.round(((ramHitsCount + diskHitsCount) / evaluatedAssetTotal) * 1000) / 10 : 0

  const telemetryData = status?.telemetry
  const reusedConnections = telemetryData?.reused_connections ?? 0
  const newConnections = telemetryData?.new_connections ?? 0
  const totalConnections = reusedConnections + newConnections
  const connReusePercent = telemetryData?.reuse_rate_percent ?? (totalConnections > 0 ? Math.round((reusedConnections / totalConnections) * 1000) / 10 : 0)
  const h2Count = telemetryData?.protocols?.['HTTP/2'] ?? 0
  const h1Count = telemetryData?.protocols?.['HTTP/1.1'] ?? 0
  const latencySamples = telemetryData?.percentiles?.samples ?? 0
  const p50Latency = telemetryData?.percentiles?.p50_ms ?? 0
  const p95Latency = telemetryData?.percentiles?.p95_ms ?? 0
  const activeApiCount = status?.active_api_count ?? 0
  const activeForeground = status?.active_foreground_assets ?? 0
  const prefetchReusedCount = status?.requests?.prefetch_reused ?? 0

  return (
    <div className="min-h-screen w-full flex flex-col bg-[#f8fafc] text-slate-800 font-sans antialiased selection:bg-sky-100">
      {/* Toast Notification */}
      {toast && (
        <div className="fixed top-5 left-1/2 -translate-x-1/2 z-[60] animate-in fade-in slide-in-from-top-3 duration-200">
          <div className="px-4 py-2 rounded-lg bg-slate-900/90 text-white shadow-xl border border-slate-700/60 flex items-center gap-2 text-xs backdrop-blur-md">
            {toast.type === 'success' && <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />}
            {toast.type === 'error' && <XCircle className="w-4 h-4 text-red-400 shrink-0" />}
            {toast.type === 'warning' && <AlertTriangle className="w-4 h-4 text-amber-400 shrink-0" />}
            {toast.type === 'info' && <Info className="w-4 h-4 text-sky-400 shrink-0" />}
            <span className="font-medium tracking-tight">{toast.msg}</span>
          </div>
        </div>
      )}

      {/* Termination Notice Banner when user explicitly quits backend */}
      {isTerminated && (
        <div className="bg-rose-50 border-b border-rose-200 px-4 py-2.5 text-center text-xs sm:text-sm text-rose-800 font-semibold flex items-center justify-center gap-2 select-none animate-in fade-in duration-200">
          <span>🛑</span>
          <span>{t('header.terminatedNotice')}</span>
        </div>
      )}

      {/* 1. Header & Live Telemetry HUD (Fixed top panel) */}
      <header className="shrink-0 bg-white border-b border-slate-200/90 px-4 sm:px-6 py-3.5 sm:py-4 shadow-2xs z-20">
        <div className="max-w-5xl mx-auto flex flex-col gap-3 sm:gap-3.5">
          {/* Top Row: Logo, Title, Running State & Master Switch */}
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3 min-w-0">
              <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 shadow-2xs ${isRunning ? 'bg-emerald-600 text-white shadow-emerald-500/20' : 'bg-slate-700 text-slate-300'}`}>
                <Zap className="w-5.5 h-5.5 fill-current" />
              </div>
              <div className="flex flex-col min-w-0">
                <div className="flex items-center gap-2.5 flex-wrap">
                  <span className="text-lg sm:text-xl font-extrabold text-slate-900 tracking-tight leading-none">
                    {t('header.title')}
                  </span>
                  <span className="text-xs sm:text-[13px] font-mono font-bold px-2.5 py-1 rounded-lg bg-slate-100/90 text-slate-700 border border-slate-200/90 shadow-2xs">
                    v{status?.version || '2.5.0'}
                  </span>
                  {updateInfo?.available && (
                    <button
                      type="button"
                      onClick={() => setIsUpdateModalOpen(true)}
                      className="px-3 py-1 rounded-lg bg-amber-50 hover:bg-amber-100 text-amber-950 border border-amber-300 text-xs sm:text-[13px] font-bold inline-flex items-center gap-1.5 cursor-pointer active:scale-[0.98] transition-all shadow-2xs"
                    >
                      <span className="text-sm">🔥</span>
                      <span>{t('header.updateFound', { version: updateInfo.version })}</span>
                    </button>
                  )}
                </div>
                <div className="flex items-center gap-2 mt-1.5 flex-wrap">
                  <span className="inline-flex items-center gap-1.5 text-xs sm:text-[13px]">
                    <span className={`w-2.5 h-2.5 rounded-full ${isRunning ? 'bg-emerald-500 animate-pulse' : 'bg-slate-400'}`} />
                    <span className={isRunning ? 'font-semibold text-emerald-700' : 'text-slate-500 font-medium'}>
                      {isRunning ? t('header.runningWithPort', { port: currentListenPort }) : t('header.stopped')}
                    </span>
                  </span>
                  {isRunning && (
                    <>
                      <span className="text-slate-300">·</span>
                      <span className="inline-flex items-center gap-1 text-xs sm:text-[13px] text-slate-500 font-mono">
                        <span className="text-slate-400">{t('header.uptime')}</span>
                        <span className="font-semibold text-slate-700">
                          <RuntimeUptime baseSeconds={uptimeSec} running={isRunning} />
                        </span>
                      </span>
                    </>
                  )}
                </div>
              </div>
            </div>

            {/* Top Right Master Start / Stop Action Button, Quit Button & Language Switcher */}
            <div className="flex items-center gap-2 sm:gap-2.5 flex-wrap justify-end">
              {/* Language Switcher Capsule */}
              <div className="inline-flex items-center rounded-xl bg-slate-100 p-0.5 border border-slate-200/90 text-xs font-semibold shadow-2xs">
                <button
                  type="button"
                  onClick={() => setLanguage('zh')}
                  className={`px-2 py-1 rounded-lg transition-all cursor-pointer ${
                    language === 'zh'
                      ? 'bg-white text-sky-700 shadow-2xs font-bold'
                      : 'text-slate-600 hover:text-slate-900'
                  }`}
                >
                  简中
                </button>
                <button
                  type="button"
                  onClick={() => setLanguage('en')}
                  className={`px-2 py-1 rounded-lg transition-all cursor-pointer ${
                    language === 'en'
                      ? 'bg-white text-sky-700 shadow-2xs font-bold'
                      : 'text-slate-600 hover:text-slate-900'
                  }`}
                >
                  EN
                </button>
                <button
                  type="button"
                  onClick={() => setLanguage('ja')}
                  className={`px-2 py-1 rounded-lg transition-all cursor-pointer ${
                    language === 'ja'
                      ? 'bg-white text-sky-700 shadow-2xs font-bold'
                      : 'text-slate-600 hover:text-slate-900'
                  }`}
                >
                  日本語
                </button>
              </div>

              <button
                type="button"
                disabled={loadingProxy || isTerminated}
                onClick={handleToggleProxy}
                className={`min-w-[85px] sm:min-w-[100px] px-3.5 sm:px-4 py-2 rounded-xl text-xs sm:text-sm font-bold text-white shadow-xs transition-all cursor-pointer select-none active:scale-[0.98] whitespace-nowrap ${
                  isRunning
                    ? 'bg-rose-600 hover:bg-rose-700 active:bg-rose-800'
                    : 'bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800'
                }`}
              >
                {loadingProxy ? t('header.processing') : isRunning ? t('header.stopProxy') : t('header.startProxy')}
              </button>
              <button
                type="button"
                disabled={isTerminated}
                onClick={() => setIsQuitModalOpen(true)}
                className="px-3 py-2 rounded-xl text-xs sm:text-sm font-semibold text-slate-600 hover:text-rose-600 bg-slate-100/90 hover:bg-rose-50 border border-slate-200/90 hover:border-rose-300 transition-all cursor-pointer select-none active:scale-[0.98] flex items-center gap-1.5 shadow-2xs shrink-0 whitespace-nowrap"
                title={t('header.quitAppTip')}
              >
                <Power className="w-4 h-4 text-rose-500" />
                <span className="hidden min-[400px]:inline">{t('header.quitApp')}</span>
              </button>
            </div>
          </div>

          {/* Realtime request counters: isolated from the root App render loop */}
          <RealtimeRequestHud status={status} running={isRunning} />
        </div>
      </header>

      {/* Navigation Tabs */}
      <div className="bg-white border-b border-slate-200/90 px-4 sm:px-6 shrink-0 shadow-2xs">
        <div className="max-w-5xl mx-auto flex items-center gap-1.5 pt-1.5">
          <button
            type="button"
            onClick={() => setActiveTab('core')}
            className={`px-4 py-2.5 text-xs sm:text-sm font-bold border-b-2 transition-all flex items-center gap-2 cursor-pointer select-none ${
              activeTab === 'core'
                ? 'border-sky-600 text-sky-700 bg-sky-50/60 rounded-t-lg'
                : 'border-transparent text-slate-600 hover:text-slate-900 hover:bg-slate-50 rounded-t-lg'
            }`}
          >
            <Zap className="w-4 h-4" />
            <span>{t('tabs.core')}</span>
          </button>
          {showAndroidPatch && (
            <button
              type="button"
              onClick={() => setActiveTab('android')}
              className={`px-4 py-2.5 text-xs sm:text-sm font-bold border-b-2 transition-all flex items-center gap-2 cursor-pointer select-none ${
                activeTab === 'android'
                  ? 'border-indigo-600 text-indigo-700 bg-indigo-50/60 rounded-t-lg'
                  : 'border-transparent text-slate-600 hover:text-slate-900 hover:bg-slate-50 rounded-t-lg'
              }`}
            >
              <Smartphone className="w-4 h-4" />
              <span>{t('tabs.android')}</span>
            </button>
          )}
        </div>
      </div>

      {/* 2. Main Work Area (Natural Content-Fit, 2-Column Responsive Card Grid) */}
      <main className="w-full max-w-5xl mx-auto px-4 sm:px-6 py-4 sm:py-5 pb-20 sm:pb-24 flex flex-col justify-start gap-4 sm:gap-5 flex-1">
        {activeTab === 'core' ? (
          <>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4 sm:gap-4.5">
          {/* Card 1: Local Cache & Storage */}
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">📁</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    {t('cards.cache.title')}
                  </h2>
                </div>
                <span className="text-xs text-emerald-700 font-semibold px-2.5 py-0.5 rounded-full bg-emerald-50 border border-emerald-200/60">
                  {t('cards.cache.badge')}
                </span>
              </div>

              {/* Local Cache Dir */}
              <div className="space-y-2">
                <label className="text-[13px] sm:text-sm text-slate-700 font-medium block">
                  {isAndroid ? t('cards.cache.dirLabelAndroid') : t('cards.cache.dirLabel')}
                </label>
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <input
                      type="text"
                      disabled={Boolean(loadingAction) || loadingBrowse}
                      value={cacheDirInput}
                      onChange={(e) => setCacheDirInput(e.target.value)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') {
                          e.preventDefault()
                          handleSaveCacheDir()
                        }
                      }}
                      className="flex-1 min-w-0 bg-slate-50/70 border border-slate-200 rounded-lg px-3 py-1.5 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs transition-all"
                    />
                    <button
                      type="button"
                      onClick={handleSaveCacheDir}
                      disabled={Boolean(loadingAction) || loadingBrowse}
                      aria-busy={isActionLoading('save-cache-dir')}
                      className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-white bg-sky-600 hover:bg-sky-700 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs disabled:opacity-60"
                    >
                      {isActionLoading('save-cache-dir') ? t('common.saving') : t('common.save')}
                    </button>
                    {!isAndroid && (
                      <button
                        type="button"
                        onClick={handleBrowseDir}
                        disabled={loadingBrowse}
                        aria-busy={loadingBrowse}
                        className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs disabled:opacity-60 disabled:cursor-wait disabled:hover:bg-slate-50"
                      >
                        {loadingBrowse ? t('common.loading') : t('common.browse')}
                      </button>
                    )}
                  </div>
                  {!isAndroid && (
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={handleOpenCacheFolder}
                        disabled={loadingOpenFolder}
                        className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs disabled:opacity-60"
                      >
                        {loadingOpenFolder ? t('common.loading') : t('common.openFolder')}
                      </button>
                      <button
                        type="button"
                        onClick={handleDetectAcgp}
                        disabled={Boolean(loadingAction)}
                        aria-busy={isActionLoading('detect-acgp')}
                        className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs"
                      >
                        {isActionLoading('detect-acgp') ? t('common.loading') : t('cards.cache.detectAcgpower')}
                      </button>
                    </div>
                  )}
                </div>
              </div>

              {/* RAM Cache Setting */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <label className="inline-flex items-start gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none leading-snug">
                  <input
                    type="checkbox"
                    checked={isRamCache}
                    onChange={handleToggleRamCache}
                    disabled={Boolean(loadingAction)}
                    className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                  />
                  <span>
                    {t('cards.cache.ramCacheLabel', {
                      maxMb: ramMaxMb,
                      gbText: ramMaxMb >= 1024 ? ` (${Math.round((ramMaxMb / 1024) * 10) / 10}GB)` : '',
                    })}
                  </span>
                </label>

                {isRamCache && (
                  <div className="ml-6 space-y-2">
                    <div className="flex items-center gap-2.5 flex-wrap text-xs sm:text-sm text-slate-700">
                      <span>{t('cards.cache.limitMbLabel')}</span>
                      <input
                        type="text"
                        disabled={Boolean(loadingAction)}
                        value={ramMbInput}
                        onChange={(e) => setRamMbInput(e.target.value)}
                        className="w-24 bg-slate-50/70 border border-slate-200 rounded-lg px-2.5 py-1 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs"
                      />
                      <button
                        type="button"
                        onClick={handleApplyRamMb}
                        disabled={Boolean(loadingAction)}
                        aria-busy={isActionLoading('ram-mb')}
                        className="px-2.5 sm:px-3 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs whitespace-nowrap"
                      >
                        {isActionLoading('ram-mb') ? t('cards.cache.applying') : t('cards.cache.apply')}
                      </button>
                      {(cacheStats?.resident_bytes ?? 0) > 0 ? (
                        <div className="flex items-center gap-2.5 text-xs font-mono text-slate-600 flex-wrap">
                          <span>{t('cards.cache.regularLabel', { mb: ramUsageMb })}</span>
                          <span className="text-amber-700 font-medium">{t('cards.cache.residentLabel', { mb: cacheStats?.resident_mb ?? 0 })}</span>
                          <span className="font-semibold text-slate-800">{t('cards.cache.usedLabel', { used: cacheStats?.total_ram_mb ?? (ramUsageMb + (cacheStats?.resident_mb ?? 0)), max: ramMaxMb })}</span>
                        </div>
                      ) : (
                        <span className="text-slate-500 font-mono text-xs">
                          {ramUsageMb} / {ramMaxMb} MB ({Math.min(100, Math.round((ramUsageMb / Math.max(1, ramMaxMb)) * 100))}%)
                        </span>
                      )}
                      <div className="w-28 h-2.5 bg-slate-100 rounded-full overflow-hidden border border-slate-200/80 shrink-0">
                        <div
                          className="h-full bg-emerald-500 rounded-full transition-all duration-300"
                          style={{
                            width: `${Math.min(100, Math.round(((cacheStats?.total_ram_mb ?? ramUsageMb) / Math.max(1, ramMaxMb)) * 100))}%`
                          }}
                        />
                      </div>
                      <button
                        type="button"
                        onClick={() => setIsBoostModalOpen(true)}
                        className={`px-2.5 py-1 rounded-lg text-xs font-medium border active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1.5 shadow-2xs shrink-0 ${
                          (cacheStats?.resident_bytes ?? 0) > 0
                            ? 'text-emerald-800 bg-emerald-50 hover:bg-emerald-100 border-emerald-300'
                            : cacheStats?.boost_state === 'loading'
                            ? 'text-sky-800 bg-sky-50 hover:bg-sky-100 border-sky-300 animate-pulse'
                            : 'text-amber-800 bg-amber-50 hover:bg-amber-100 border-amber-300'
                        }`}
                        title={t('cards.cache.boostTooltip')}
                      >
                        <span>⚡</span>
                        <span>
                          {cacheStats?.boost_state === 'loading'
                            ? t('cards.cache.boostLoading')
                            : (cacheStats?.resident_bytes ?? 0) > 0
                            ? t('cards.cache.boostActiveWithMb', { mb: cacheStats?.resident_mb ?? 0 })
                            : t('cards.cache.boostBtn')}
                        </span>
                      </button>
                      {(cacheStats?.system_total_mb ?? 0) > 0 && (
                        <span className="text-slate-400 text-xs font-mono ml-auto sm:ml-0">
                          {t('cards.cache.hostRam', { gb: Math.round((cacheStats?.system_total_mb ?? 0) / 1024) })}
                        </span>
                      )}
                    </div>

                    <label className="inline-flex items-start gap-2 text-xs sm:text-[13px] text-slate-700 cursor-pointer select-none leading-snug">
                      <input
                        type="checkbox"
                        checked={isRamWarmup}
                        onChange={handleToggleRamWarmup}
                        disabled={Boolean(loadingAction)}
                        className="w-3.5 h-3.5 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                      />
                      <span>{t('cards.cache.ramWarmupLabel')}</span>
                    </label>
                  </div>
                )}
              </div>


              {/* Cache Maintenance & Integrity */}
              <div className="space-y-2 pt-1.5 border-t border-slate-100">
                <label className="inline-flex items-start gap-2 text-xs sm:text-[13px] text-slate-800 cursor-pointer select-none leading-snug">
                  <input
                    type="checkbox"
                    checked={isAutoRepair}
                    onChange={handleToggleAutoRepair}
                    disabled={Boolean(loadingAction)}
                    className="w-3.5 h-3.5 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                  />
                  <span>{t('cards.cache.cleanZombiesLabel')}</span>
                </label>

                <div className="flex items-center gap-2 pt-0.5 flex-wrap">
                  <button
                    type="button"
                    onClick={() => setIsAuditModalOpen(true)}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1.5 shadow-2xs"
                  >
                    <span>🩺</span>
                    <span>{t('cards.cache.auditCache')}</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setIsClearModalOpen(true)}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1.5 shadow-2xs"
                  >
                    <span>🧹</span>
                    <span>{t('cards.cache.clearCache')}</span>
                  </button>
                </div>
              </div>
            </div>
          </div>

          {/* Card 2: Upstream Proxy & Network Routing */}
          {!isAndroid && (
            <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">🌐</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    {t('cards.upstream.title')}
                  </h2>
                </div>
                <span className="text-xs text-sky-700 font-semibold px-2.5 py-0.5 rounded-full bg-sky-50 border border-sky-200/60">
                  {t('cards.upstream.badge')}
                </span>
              </div>

              {/* Upstream Proxy */}
              <div className="space-y-1.5">
                <label className="text-[13px] sm:text-sm text-slate-700 font-medium block">
                  {t('cards.upstream.upstreamLabel')}
                </label>
                <div className="flex items-center gap-1.5 sm:gap-2">
                  <input
                    type="text"
                    disabled={isDirect || Boolean(loadingAction)}
                    value={upstreamInput}
                    onChange={(e) => setUpstreamInput(e.target.value)}
                    className="flex-1 min-w-0 bg-slate-50/70 border border-slate-200 rounded-lg px-3 py-1.5 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs transition-all disabled:bg-slate-100 disabled:text-slate-400"
                  />
                  <button
                    type="button"
                    disabled={isDirect || Boolean(loadingAction)}
                    onClick={handleSaveUpstream}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer disabled:opacity-50 shadow-2xs"
                  >
                    {isActionLoading('save-upstream') ? t('common.saving') : t('common.save')}
                  </button>
                  <button
                    type="button"
                    disabled={isDirect || Boolean(loadingAction)}
                    aria-busy={isActionLoading('probe-upstream')}
                    onClick={handleProbeUpstream}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer disabled:opacity-50 shadow-2xs"
                  >
                    {isActionLoading('probe-upstream') ? t('common.loading') : t('cards.upstream.autoDetect')}
                  </button>
                </div>
              </div>

              {/* Upstream Failover */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <div>
                    <div className="text-[13px] sm:text-sm text-slate-700 font-medium">
                      {t('cards.upstream.failoverTitle')}
                    </div>
                    <div className="text-xs text-slate-400 mt-0.5">
                      {t('cards.upstream.failoverSubtitle')}
                    </div>
                  </div>
                  <span
                    className={
                      isDirect || !failoverEnabledInput || !backupUpstreamInput.trim()
                        ? 'text-xs font-semibold px-2.5 py-0.5 rounded-full bg-slate-50 text-slate-500 border border-slate-200/60'
                        : upstreamRuntime?.active === 'backup'
                          ? 'text-xs font-semibold px-2.5 py-0.5 rounded-full bg-amber-50 text-amber-700 border border-amber-200/70'
                          : 'text-xs font-semibold px-2.5 py-0.5 rounded-full bg-emerald-50 text-emerald-700 border border-emerald-200/70'
                    }
                  >
                    {isDirect
                      ? t('cards.upstream.failoverDisabledDirect')
                      : !failoverEnabledInput || !backupUpstreamInput.trim()
                        ? t('cards.upstream.failoverNotEnabled')
                        : upstreamRuntime?.active === 'backup'
                          ? t('cards.upstream.activeBackup')
                          : t('cards.upstream.activePrimary')}
                  </span>
                </div>

                <label
                  className={`inline-flex items-start gap-2 text-[13px] sm:text-sm ${
                    isDirect ? 'text-slate-400 cursor-not-allowed' : 'text-slate-800 cursor-pointer'
                  } select-none leading-snug`}
                >
                  <input
                    type="checkbox"
                    checked={!isDirect && failoverEnabledInput}
                    onChange={handleToggleFailoverEnabled}
                    disabled={isDirect || Boolean(loadingAction)}
                    className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 disabled:opacity-50 accent-sky-600"
                  />
                  <span>
                    {t('cards.upstream.enableFailover')}
                    {isDirect && <span className="text-xs text-slate-400 ml-1">{t('cards.upstream.failoverDirectNote')}</span>}
                  </span>
                </label>

                {!isDirect && failoverEnabledInput && (
                  <div className="space-y-2.5 pt-0.5">
                    {/* Backup upstream input + probe button */}
                    <div className="space-y-1.5">
                      <div className="flex items-center gap-1.5 sm:gap-2">
                        <label className="text-xs sm:text-[13px] text-slate-600 shrink-0">
                          {t('cards.upstream.backupAddress')}
                        </label>
                        <input
                          type="text"
                          value={backupUpstreamInput}
                          onChange={(e) => setBackupUpstreamInput(e.target.value)}
                          disabled={Boolean(loadingAction)}
                          placeholder={t('cards.upstream.backupPlaceholder')}
                          className="flex-1 min-w-0 bg-slate-50/70 border border-slate-200 rounded-lg px-3 py-1.5 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs transition-all"
                        />
                        <button
                          type="button"
                          disabled={Boolean(loadingAction)}
                          aria-busy={isActionLoading('probe-backup-upstream')}
                          onClick={handleProbeBackupUpstream}
                          className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer disabled:opacity-50 shadow-2xs"
                        >
                          {isActionLoading('probe-backup-upstream') ? t('cards.upstream.backupProbing') : t('cards.upstream.backupAutoDetect')}
                        </button>
                      </div>
                      <div className="text-[11px] sm:text-xs text-slate-500 leading-relaxed">
                        {t('cards.upstream.backupTip')}
                      </div>
                    </div>

                    {/* 3 Parameter Inputs: Redesigned with label on top, input on bottom, NEVER vertical wrapping */}
                    <div className="grid grid-cols-1 sm:grid-cols-3 gap-2.5">
                      <div className="flex flex-col gap-1 p-2 sm:p-2.5 rounded-lg bg-slate-50/70 border border-slate-200/70">
                        <span className="text-[11px] font-medium text-slate-500 whitespace-nowrap" title={t('cards.upstream.thresholdTip')}>
                          {t('cards.upstream.thresholdLabel')}
                        </span>
                        <div className="flex items-center gap-1.5 mt-0.5">
                          <input
                            type="number"
                            min={0}
                            max={60000}
                            step={100}
                            value={failoverThresholdInput}
                            onChange={(e) => setFailoverThresholdInput(e.target.value)}
                            disabled={Boolean(loadingAction)}
                            className="w-full min-w-0 bg-white border border-slate-200 rounded-md px-2 py-1 text-right font-mono text-xs text-slate-800 focus:ring-1 focus:ring-sky-500 focus:outline-none"
                          />
                          <span className="text-xs text-slate-400 shrink-0 font-mono">ms</span>
                        </div>
                      </div>

                      <div className="flex flex-col gap-1 p-2 sm:p-2.5 rounded-lg bg-slate-50/70 border border-slate-200/70">
                        <span className="text-[11px] font-medium text-slate-500 whitespace-nowrap" title={t('cards.upstream.failuresTip')}>
                          {t('cards.upstream.failuresLabel')}
                        </span>
                        <div className="flex items-center gap-1.5 mt-0.5">
                          <input
                            type="number"
                            min={1}
                            max={10}
                            step={1}
                            value={failoverConsecutiveInput}
                            onChange={(e) => setFailoverConsecutiveInput(e.target.value)}
                            disabled={Boolean(loadingAction)}
                            className="w-full min-w-0 bg-white border border-slate-200 rounded-md px-2 py-1 text-right font-mono text-xs text-slate-800 focus:ring-1 focus:ring-sky-500 focus:outline-none"
                          />
                          <span className="text-xs text-slate-400 shrink-0">{t('cards.upstream.times')}</span>
                        </div>
                      </div>

                      <div className="flex flex-col gap-1 p-2 sm:p-2.5 rounded-lg bg-slate-50/70 border border-slate-200/70">
                        <span className="text-[11px] font-medium text-slate-500 whitespace-nowrap">
                          {t('cards.upstream.cooldownLabel')}
                        </span>
                        <div className="flex items-center gap-1.5 mt-0.5">
                          <input
                            type="number"
                            min={1}
                            max={3600}
                            step={10}
                            value={failoverCooldownInput}
                            onChange={(e) => setFailoverCooldownInput(e.target.value)}
                            disabled={Boolean(loadingAction)}
                            className="w-full min-w-0 bg-white border border-slate-200 rounded-md px-2 py-1 text-right font-mono text-xs text-slate-800 focus:ring-1 focus:ring-sky-500 focus:outline-none"
                          />
                          <span className="text-xs text-slate-400 shrink-0">{t('cards.upstream.seconds')}</span>
                        </div>
                      </div>
                    </div>

                    <div className="text-[11px] text-slate-400 leading-relaxed px-0.5">
                      {t('cards.upstream.failoverMechanismNotice')}
                    </div>

                    <div className="flex items-center justify-between gap-2 flex-wrap pt-0.5">
                      <div className="flex flex-col gap-1.5">
                        <label className="inline-flex items-center gap-2 text-xs sm:text-[13px] text-slate-700 cursor-pointer select-none">
                          <input
                            type="checkbox"
                            checked={failoverAutoRecoverInput}
                            onChange={(e) => setFailoverAutoRecoverInput(e.target.checked)}
                            disabled={Boolean(loadingAction)}
                            className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                          />
                          <span>{t('cards.upstream.autoRecoverCheckbox')}</span>
                        </label>

                        {status?.platform === 'windows' && (
                          <label className="inline-flex items-center gap-2 text-xs sm:text-[13px] text-slate-700 cursor-pointer select-none">
                            <input
                              type="checkbox"
                              checked={failoverNotificationInput}
                              onChange={(e) => setFailoverNotificationInput(e.target.checked)}
                              disabled={Boolean(loadingAction)}
                              className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                            />
                            <span>{t('cards.upstream.notificationCheckbox')}</span>
                          </label>
                        )}
                      </div>

                      <button
                        type="button"
                        onClick={handleSaveFailover}
                        disabled={Boolean(loadingAction)}
                        aria-busy={isActionLoading('save-failover')}
                        className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-white bg-sky-600 hover:bg-sky-700 active:bg-sky-800 border border-sky-600 active:scale-[0.98] transition-all cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed"
                      >
                        {isActionLoading('save-failover') ? t('common.saving') : t('cards.upstream.saveFailoverBtn')}
                      </button>
                    </div>

                    {backupUpstreamInput.trim() && !isDirect && (
                      <div className="p-2.5 rounded-lg bg-slate-50/80 border border-slate-200/70 text-xs text-slate-600">
                        <div className="flex items-center justify-between gap-2 flex-wrap">
                          <span>
                            {t('cards.upstream.currentRoute')}
                            <strong className={upstreamRuntime?.active === 'backup' ? 'text-amber-700' : 'text-emerald-700'}>
                              {upstreamRuntime?.active === 'backup' ? t('cards.upstream.backupRoute') : t('cards.upstream.primaryRoute')}
                            </strong>
                          </span>
                          <span className="font-mono text-slate-500">
                            {t('cards.upstream.consecutiveAbnormal')} {upstreamRuntime?.failure_count ?? 0} / {upstreamRuntime?.consecutive_failures ?? (Number(failoverConsecutiveInput) || 3)}
                          </span>
                        </div>
                        <div className="mt-1.5 flex items-center justify-between gap-2 flex-wrap">
                          <span className="text-slate-500">
                            {upstreamRuntime?.reason
                              ? ({
                                  timeout: t('cards.upstream.reasonTimeout'),
                                  connection_error: t('cards.upstream.reasonConnError'),
                                  latency_threshold: t('cards.upstream.reasonLatency'),
                                  primary_recovered: t('cards.upstream.reasonRecovered'),
                                  primary_recovery_failed: t('cards.upstream.reasonRecoveryFailed'),
                                } as Record<string, string>)[upstreamRuntime.reason] || upstreamRuntime.reason
                              : t('cards.upstream.waitingTelemetry')}
                          </span>
                          <span className="font-mono text-slate-400">
                            {upstreamRuntime?.last_switch_at
                              ? t('cards.upstream.lastSwitch', { time: new Date(upstreamRuntime.last_switch_at).toLocaleTimeString(language === 'zh' ? 'zh-CN' : language === 'ja' ? 'ja-JP' : 'en-US', { hour12: false }) })
                              : t('cards.upstream.neverSwitched')}
                          </span>
                        </div>
                      </div>
                    )}

                    {!backupUpstreamInput.trim() && (
                      <div className="text-xs text-amber-700 bg-amber-50/80 border border-amber-200/70 rounded-lg px-3 py-2">
                        {t('cards.upstream.missingBackupWarning')}
                      </div>
                    )}
                  </div>
                )}
              </div>

              {/* Mode Toggles */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <label className="inline-flex items-center gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={isDirect}
                    onChange={handleToggleDirect}
                    disabled={Boolean(loadingAction)}
                    className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                  />
                  <span>{t('cards.upstream.directModeCheckbox')}</span>
                </label>

                <div>
                  <label className="inline-flex items-start gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none leading-snug">
                    <input
                      type="checkbox"
                      disabled={Boolean(loadingAction)}
                      checked={isShimakaze}
                      onChange={handleToggleShimakaze}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 disabled:opacity-50 accent-sky-600"
                    />
                    <span className="text-slate-800">
                      {t('cards.upstream.shimakazeCheckbox')}
                    </span>
                  </label>

                  {isShimakaze && (
                    <div className="mt-2 p-3 bg-sky-50/80 border border-sky-200/70 rounded-lg text-xs text-sky-900 leading-relaxed shadow-2xs space-y-1">
                      <div>
                        {t('cards.upstream.shimakazeNotice')}
                      </div>
                      <div className="text-sky-800/95">
                        {t('cards.upstream.shimakazeRemoteCacheNotice')}
                      </div>
                    </div>
                  )}
                </div>
              </div>

              {/* Listen Port & Allow LAN */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <div className="flex items-center gap-1.5 sm:gap-2 flex-wrap">
                  <label className="text-[13px] sm:text-sm text-slate-700 font-medium">{t('cards.network.portLabel')}</label>
                  <input
                    type="text"
                    disabled={Boolean(loadingAction)}
                    value={portInput}
                    onChange={(e) => setPortInput(e.target.value)}
                    className="w-16 sm:w-18 bg-slate-50/70 border border-slate-200 rounded-lg px-2.5 py-1 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs"
                  />
                  <button
                    type="button"
                    onClick={handleSavePort}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('save-port')}
                    className="px-2.5 sm:px-3 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs whitespace-nowrap"
                  >
                    {isActionLoading('save-port') ? t('common.saving') : t('cards.network.savePort')}
                  </button>
                  <button
                    type="button"
                    onClick={handleResetPort}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('reset-port')}
                    className="px-2 sm:px-2.5 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-500 hover:text-slate-800 hover:bg-slate-100 border border-slate-200/80 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs whitespace-nowrap"
                  >
                    {isActionLoading('reset-port') ? t('common.loading') : t('cards.network.resetDefault')}
                  </button>
                </div>

                <div className="space-y-1.5">
                  <div className="flex items-center justify-between gap-2 flex-wrap">
                    <label className="inline-flex items-center gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none">
                      <input
                        type="checkbox"
                        checked={isAllowLan}
                        onChange={handleToggleAllowLan}
                        disabled={Boolean(loadingAction)}
                        className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                      />
                      <span>{t('cards.network.allowLan')}</span>
                    </label>
                    <button
                      type="button"
                      onClick={() => setIsMobileModalOpen(true)}
                      className="px-2.5 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1 shadow-2xs shrink-0 whitespace-nowrap"
                    >
                      <span>📱</span>
                      <span>{t('cards.network.mobileGuide')}</span>
                    </button>
                  </div>
                  {isAllowLan && (
                    <div className="text-xs text-sky-700 font-medium pl-6">
                      {t('cards.network.lanIpDesc', { ip: status?.lan_ip || '192.168.x.x', port: currentListenPort })}
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>
          )}

          {/* Card 3: HTTPS Root CA & System Integration */}
          {!isAndroid && (
            <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">🛡️</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    {t('cards.network.certTitle')}
                  </h2>
                </div>
                <span
                  className={`text-xs font-semibold px-2.5 py-0.5 rounded-full border ${
                    !caStatusKnown
                      ? 'bg-slate-50 text-slate-500 border-slate-200/60'
                      : isCaInstalled
                        ? 'bg-emerald-50 text-emerald-700 border-emerald-200/60'
                        : 'bg-rose-50 text-rose-700 border-rose-200/60'
                  }`}
                >
                  {!caStatusKnown ? t('common.loading') : isCaInstalled ? t('cards.network.certInstalled') : t('cards.network.certNotInstalled')}
                </span>
              </div>

              {/* CA Status & Action Buttons */}
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <span className="text-[13px] sm:text-sm text-slate-700 font-medium">
                    {t('cards.network.certStatusLabel')}
                    <strong className={!caStatusKnown ? 'text-slate-500 ml-1' : isCaInstalled ? 'text-emerald-700 ml-1' : 'text-rose-600 ml-1'}>
                      {!caStatusKnown ? t('common.loading') : isCaInstalled ? t('cards.network.certInstalled') : t('cards.network.certNotInstalled')}
                    </strong>
                  </span>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => setCaModalAction('install')}
                      className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                    >
                      {t('cards.network.certInstallBtn')}
                    </button>
                    <button
                      type="button"
                      onClick={() => setCaModalAction('uninstall')}
                      className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                    >
                      {t('cards.network.certUninstallBtn')}
                    </button>
                  </div>
                </div>
                <div className="text-xs font-mono text-slate-500 pt-0.5 select-all break-all leading-normal">
                  {t('cards.network.certFingerprintLabel')} {caFingerprint || t('common.loading')}
                </div>
              </div>

              {/* System Checkboxes */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <div>
                  <label className="inline-flex items-center gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={isAutoPac}
                      onChange={handleToggleAutoPac}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                    />
                    <span>
                      {t('cards.network.autoPacCheckbox')}
                    </span>
                  </label>
                  {isAutoPac && status?.system_proxy_conflict && (
                    <div className="mt-2 ml-6 p-2.5 rounded-lg bg-amber-50/90 border border-amber-200/80 text-xs text-amber-900 leading-relaxed shadow-2xs">
                      <span className="font-semibold">{t('cards.network.conflictNotice')}</span>
                      <span className="ml-1 break-all">{status.system_proxy_conflict}</span>
                    </div>
                  )}
                </div>

                <div>
                  <label className="inline-flex items-center gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={isAutoStart}
                      onChange={handleToggleAutoStart}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                    />
                    <span>{t('cards.network.autoStartCheckbox')}</span>
                  </label>
                </div>

                <div>
                  <label className="inline-flex items-center gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={isAutoUpdate}
                      onChange={handleToggleAutoUpdate}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                    />
                    <span>{t('cards.network.autoUpdateCheckbox')}</span>
                  </label>
                </div>
              </div>
            </div>
          </div>
          )}

          {/* Card 4: Performance Acceleration & Asset Scheduling */}
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">⚡</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    {t('cards.optim.title')}
                  </h2>
                </div>
                <span className="text-xs text-amber-800 font-semibold px-2.5 py-0.5 rounded-full bg-amber-50 border border-amber-200/60">
                  {t('cards.optim.badge')}
                </span>
              </div>

              <div className="space-y-3 text-[13px] sm:text-sm text-slate-800">
                <div>
                  <label className="inline-flex items-start gap-2 cursor-pointer select-none leading-snug">
                    <input
                      type="checkbox"
                      checked={isPrefetch}
                      onChange={handleTogglePrefetch}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                    />
                    <span>
                      {t('cards.optim.prefetch')}
                    </span>
                  </label>
                </div>

                <div>
                  <label className="inline-flex items-start gap-2 cursor-pointer select-none leading-snug">
                    <input
                      type="checkbox"
                      checked={isBrowserCache}
                      onChange={handleToggleBrowserCache}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                    />
                    <span>
                      {t('cards.optim.browserCache')}
                    </span>
                  </label>
                </div>

                <div className="p-3 bg-amber-50/70 border border-amber-200/60 rounded-lg text-xs text-amber-900 leading-relaxed shadow-2xs space-y-1">
                  <div className="font-semibold flex items-center gap-1.5 text-amber-800">
                    <span>🛡️</span>
                    <span>{t('cards.optim.subhead')}</span>
                  </div>
                  <p className="text-amber-800/90 text-[11px] sm:text-xs">
                    {t('cards.optim.pacingNotice')}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* 5. Accelerating Engine Telemetry Dashboard */}
        <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-5 transition-colors">
          <div className="flex items-center justify-between pb-2.5 mb-3.5 border-b border-slate-100">
            <div className="flex items-center gap-2.5">
              <span className="w-2.5 h-2.5 rounded-full bg-sky-500 animate-pulse shrink-0" />
              <h3 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                {t('telemetry.title')}
              </h3>
              <span className="text-xs text-slate-400 font-mono hidden sm:inline">
                ENGINE TELEMETRY
              </span>
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-3.5 sm:gap-4">
            {/* Col 1: Asset Routing & Cache Ratio */}
            <div className="bg-slate-50/70 rounded-xl p-3.5 sm:p-4 border border-slate-200/70 flex flex-col justify-between gap-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] sm:text-sm font-semibold text-slate-700 flex items-center gap-1.5">
                  <span className="text-emerald-500 text-base">⚡</span>
                  <span>{t('telemetry.ratioTitle')}</span>
                </span>
                <span className="text-sm sm:text-base font-bold font-mono text-emerald-600">
                  {totalHitRate > 0 ? `${totalHitRate}%` : '--'}
                </span>
              </div>

              {/* Multi-segment visual bar */}
              <div className="space-y-1.5">
                <div className="w-full h-2.5 bg-slate-200/80 rounded-full overflow-hidden flex shadow-inner">
                  <div
                    className="h-full bg-emerald-500 transition-all duration-300"
                    style={{ width: `${ramHitPct}%` }}
                    title={t('telemetry.ramHitsTip', { count: ramHitsCount, pct: ramHitPct })}
                  />
                  <div
                    className="h-full bg-sky-500 transition-all duration-300"
                    style={{ width: `${diskHitPct}%` }}
                    title={t('telemetry.diskHitsTip', { count: diskHitsCount, pct: diskHitPct })}
                  />
                  <div
                    className="h-full bg-amber-400/90 transition-all duration-300"
                    style={{ width: `${missHitPct}%` }}
                    title={t('telemetry.downloadsTip', { count: downloadsCount, pct: missHitPct })}
                  />
                </div>
                <div className="flex items-center justify-between text-xs text-slate-500 font-mono">
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0" />
                    RAM: {ramHitsCount}
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-sky-500 shrink-0" />
                    {t('telemetry.diskCount', { count: diskHitsCount })}
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-amber-400 shrink-0" />
                    {t('telemetry.downloadsCount', { count: downloadsCount })}
                  </span>
                </div>
              </div>

              <div className="text-xs sm:text-[13px] text-slate-500 flex items-center justify-between pt-1.5 border-t border-slate-200/60">
                <span>{t('telemetry.savedRequests')}</span>
                <span className="font-mono font-semibold text-slate-800">{t('telemetry.savedTimes', { count: hitsCount })}</span>
              </div>
            </div>

            {/* Col 2: HTTP/2 Multiplexing & Handshake Savings */}
            <div className="bg-slate-50/70 rounded-xl p-3.5 sm:p-4 border border-slate-200/70 flex flex-col justify-between gap-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] sm:text-sm font-semibold text-slate-700 flex items-center gap-1.5">
                  <span className="text-sky-500 text-base">🌐</span>
                  <span>{t('telemetry.h2Title')}</span>
                </span>
                <span className="text-sm sm:text-base font-bold font-mono text-sky-600">
                  {connReusePercent > 0 ? `${connReusePercent}%` : '--'}
                </span>
              </div>

              <div className="space-y-2 text-xs sm:text-[13px] text-slate-600">
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">{t('telemetry.avoidHandshakes')}</span>
                  <span className="font-mono font-semibold text-emerald-600">
                    {t('telemetry.timesPlus', { count: reusedConnections })}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">{t('telemetry.newVsActive')}</span>
                  <span className="font-mono text-slate-700">
                    {newConnections} / {activeForeground}
                  </span>
                </div>
              </div>

              <div className="text-xs sm:text-[13px] text-slate-500 flex items-center justify-between pt-1.5 border-t border-slate-200/60">
                <span>{t('telemetry.protocolMultiplex')}</span>
                <span className="font-mono font-medium text-sky-700">
                  H2: {h2Count} | H1: {h1Count}
                </span>
              </div>
            </div>

            {/* Col 3: Upstream Latency & Yielding Guardrail */}
            <div className="bg-slate-50/70 rounded-xl p-3.5 sm:p-4 border border-slate-200/70 flex flex-col justify-between gap-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] sm:text-sm font-semibold text-slate-700 flex items-center gap-1.5">
                  <span className="text-indigo-500 text-base">📶</span>
                  <span>{t('telemetry.upstreamLatencyTitle')}</span>
                </span>
                <span className="text-xs font-mono text-slate-500">
                  {t('telemetry.sampleCount', { count: latencySamples })}
                </span>
              </div>

              <div className="space-y-2 text-xs sm:text-[13px] text-slate-600">
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">{t('telemetry.latencyMetrics')}</span>
                  <span className="font-mono font-semibold text-slate-800">
                    {p50Latency > 0 ? `${p50Latency}ms` : '--'} / {p95Latency > 0 ? `${p95Latency}ms` : '--'}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">{t('telemetry.apiAvoidance')}</span>
                  <span className={`font-medium ${activeApiCount > 0 ? 'text-amber-600 font-semibold' : 'text-emerald-600'}`}>
                    {activeApiCount > 0 ? t('telemetry.avoidingActive', { count: activeApiCount }) : t('telemetry.avoidingReady')}
                  </span>
                </div>
              </div>

              <div className="text-xs sm:text-[13px] text-slate-500 flex items-center justify-between pt-1.5 border-t border-slate-200/60">
                <span>{t('telemetry.prefetchHitsTitle')}</span>
                {!isPrefetch ? (
                  <span className="text-slate-400 font-medium">{t('cards.upstream.failoverNotEnabled')}</span>
                ) : prefetchReusedCount > 0 ? (
                  <span className="font-mono font-semibold text-indigo-600">
                    {t('telemetry.prefetchHitItems', { count: prefetchReusedCount })}
                  </span>
                ) : diskHitsCount + ramHitsCount > 0 ? (
                  <span className="font-medium text-emerald-600">
                    {t('telemetry.prefetchReady')}
                  </span>
                ) : (
                  <span className="font-medium text-slate-400">
                    {t('telemetry.prefetchPending')}
                  </span>
                )}
              </div>
            </div>
          </div>
        </div>
      </>
    ) : showAndroidPatch ? (
      <AndroidPatchPanel showToast={showToast} />
    ) : null}
  </main>

      {/* 3. Fixed Bottom Action Dock (Footer) */}
      <footer className="sticky bottom-0 shrink-0 bg-white/95 backdrop-blur-md border-t border-slate-200/90 px-2.5 sm:px-3 py-2 shadow-[0_-2px_10px_rgba(0,0,0,0.03)] z-20">
        <div className="max-w-5xl mx-auto grid grid-cols-2 min-[480px]:grid-cols-4 min-[768px]:grid-cols-8 gap-1 sm:gap-1.5">
          <button
            type="button"
            onClick={handleOpenCacheFolder}
            disabled={loadingOpenFolder}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('common.openFolder')}
          >
            <span className="text-sm leading-none shrink-0">📁</span>
            <span className="truncate">{loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}</span>
          </button>

          <button
            type="button"
            onClick={() => setIsClearModalOpen(true)}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('cards.cache.clearCache')}
          >
            <span className="text-sm leading-none shrink-0">🗑</span>
            <span className="truncate">{t('footer.clearCache')}</span>
          </button>

          <button
            type="button"
            onClick={() => setIsRoutingModalOpen(true)}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('footer.routingTips')}
          >
            <span className="text-sm leading-none shrink-0">🌐</span>
            <span className="truncate">{t('footer.routingTips')}</span>
          </button>

          <a
            href="https://github.com/Sagisawa/GBF-Accelerator"
            target="_blank"
            rel="noreferrer"
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title="GitHub"
          >
            <span className="text-sm leading-none shrink-0">⭐</span>
            <span className="truncate">GitHub</span>
          </a>

          <button
            type="button"
            onClick={() => setIsUpdateModalOpen(true)}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('footer.checkUpdate')}
          >
            <span className="text-sm leading-none shrink-0">🔄</span>
            <span className="truncate">{t('footer.checkUpdate')}</span>
          </button>

          <button
            type="button"
            onClick={() => setIsLatencyModalOpen(true)}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('cards.upstream.latencyTest')}
          >
            <span className="text-sm leading-none shrink-0">📶</span>
            <span className="truncate">{t('footer.latencyTest')}</span>
          </button>

          <button
            type="button"
            onClick={() => setIsHealthModalOpen(true)}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('footer.diagnostics')}
          >
            <span className="text-sm leading-none shrink-0">🩺</span>
            <span className="truncate">{t('footer.diagnostics')}</span>
          </button>

          <button
            type="button"
            onClick={openLogsWindow}
            className="min-w-0 px-1 sm:px-1.5 py-1.5 sm:py-2 rounded-xl text-[11px] font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center justify-center gap-1 shadow-2xs whitespace-nowrap overflow-hidden"
            title={t('footer.liveLogs')}
          >
            <span className="text-sm leading-none shrink-0">📜</span>
            <span className="truncate">{t('footer.liveLogs')}</span>
          </button>
        </div>
      </footer>

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

      <BoostModal
        isOpen={isBoostModalOpen}
        onClose={() => setIsBoostModalOpen(false)}
        onRefresh={loadState}
        onToast={showToast}
        cacheStats={cacheStats}
      />

      {!isAndroid && (
        <CaCertModal
          isOpen={caModalAction !== null}
          onClose={() => setCaModalAction(null)}
          isInstalled={isCaInstalled}
          fingerprint={caFingerprint}
          actionType={caModalAction || 'install'}
          onRefresh={loadState}
          onToast={showToast}
        />
      )}

      <HealthCheckModal
        isOpen={isHealthModalOpen}
        onClose={() => setIsHealthModalOpen(false)}
      />

      <UpstreamSelectModal
        isOpen={isUpstreamSelectModalOpen}
        onClose={() => setIsUpstreamSelectModalOpen(false)}
        candidates={upstreamCandidates}
        currentProxy={
          upstreamSelectTarget === 'backup'
            ? backupUpstreamInput
            : (upstreamInput || config.upstream_proxy || status?.upstream_proxy)
        }
        title={
          upstreamSelectTarget === 'backup' ? (
            <div className="flex items-center gap-2">
              <Network className="w-4 h-4 text-sky-600" />
              <span>{t('modals.upstreamSelect.titleBackup')}</span>
            </div>
          ) : undefined
        }
        subtitle={
          upstreamSelectTarget === 'backup'
            ? t('modals.upstreamSelect.subtitleApp')
            : undefined
        }
        onSelect={handleSelectUpstreamCandidate}
      />

      <ShimakazeSuggestModal
        isOpen={isShimakazeSuggestOpen}
        onClose={() => setIsShimakazeSuggestOpen(false)}
        onEnable={handleEnableShimakaze}
        targetName={shimakazeTargetName}
      />

      <UpdateModal
        isOpen={isUpdateModalOpen}
        onClose={() => setIsUpdateModalOpen(false)}
        currentVersion={status?.version || '2.5.0'}
      />

      <LiveLogsWindow
        isOpen={isLogDrawerOpen}
        onClose={() => setIsLogDrawerOpen(false)}
        logs={logs}
        onClearLogs={() => setLogs([])}
        telemetry={status?.telemetry}
        hitRatioPercent={cacheStats?.hit_ratio_percent ?? 100.0}
      />

      {/* Quit Application Confirmation Modal */}
      <Modal
        isOpen={isQuitModalOpen}
        onClose={() => !quitting && setIsQuitModalOpen(false)}
        title={
          <div className="flex items-center gap-2 text-rose-600 font-bold text-base">
            <Power className="w-5 h-5" />
            <span>{t('quitModal.title')}</span>
          </div>
        }
        subtitle={t('quitModal.desc')}
        maxWidth="max-w-md"
      >
        <div className="space-y-4 text-xs sm:text-sm text-slate-600">
          <div className="p-3.5 bg-rose-50/80 border border-rose-200/80 rounded-xl space-y-2 text-rose-900 leading-relaxed">
            <p className="text-xs sm:text-sm">
              {t('quitModal.warning')}
            </p>
            <p className="font-semibold text-xs pt-1">
              {t('quitModal.warningLead')}
            </p>
            <ul className="list-disc list-inside text-xs space-y-0.5 pl-1 text-rose-800">
              <li>{t('quitModal.releaseProxyPort', { port: currentListenPort })}</li>
              <li>{t('quitModal.releaseControlPort', { port: config.control_port || 8125 })}</li>
              <li>{t('quitModal.safeFlush')}</li>
            </ul>
            <p className="text-[11px] text-rose-700/80 pt-1">
              {t('quitModal.restartTip')}
            </p>
          </div>
          <div className="pt-2 flex justify-end gap-2.5 border-t border-slate-100">
            <button
              type="button"
              disabled={quitting}
              onClick={() => setIsQuitModalOpen(false)}
              className="px-4 py-2 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-100 hover:bg-slate-200 transition-all cursor-pointer"
            >
              {t('quitModal.cancel')}
            </button>
            <button
              type="button"
              disabled={quitting}
              onClick={handleQuitApp}
              className="px-5 py-2 rounded-xl text-xs sm:text-sm font-bold text-white bg-rose-600 hover:bg-rose-700 active:bg-rose-800 transition-all cursor-pointer shadow-xs flex items-center gap-1.5"
            >
              <Power className="w-4 h-4" />
              <span>{quitting ? t('common.processing') : t('quitModal.confirm')}</span>
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}

export default App