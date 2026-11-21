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
  migrateToStandardCache,
  CacheMigrationMode,
} from './api'

import { Modal } from './components/common/Modal'
import { Button } from './components/common/Button'
import { InsetPanel } from './components/common/Card'
import { LiveLogsWindow } from './components/logs/LiveLogsWindow'
import { MobileGuideModal } from './components/modals/MobileGuideModal'
import { ClearCacheModal } from './components/modals/ClearCacheModal'
import { ShortcutsModal } from './components/modals/ShortcutsModal'
import { RoutingGuideModal } from './components/modals/RoutingGuideModal'
import { LatencyTestModal } from './components/modals/LatencyTestModal'
import { AuditModal } from './components/modals/AuditModal'
import { BoostModal } from './components/modals/BoostModal'
import { CaCertModal } from './components/modals/CaCertModal'
import { MigrateStandardModal } from './components/modals/MigrateStandardModal'
import { UpdateModal } from './components/modals/UpdateModal'
import { UpstreamSelectModal } from './components/modals/UpstreamSelectModal'
import { ShimakazeSuggestModal } from './components/modals/ShimakazeSuggestModal'
import { HealthCheckModal } from './components/modals/HealthCheckModal'
import { AndroidPatchPanel } from './components/AndroidPatchPanel'
import { HeaderCockpit } from './components/layout/HeaderCockpit'
import { CacheCard } from './components/dashboard/CacheCard'
import { UpstreamCard } from './components/dashboard/UpstreamCard'
import { NetworkCertCard } from './components/dashboard/NetworkCertCard'
import { OptimizationCard } from './components/dashboard/OptimizationCard'
import { TelemetryPanel } from './components/dashboard/TelemetryPanel'
import { useI18n } from './i18n'

import { useTheme } from './hooks/useTheme'
import { useKeyboardShortcuts } from './hooks/useKeyboardShortcuts'
import { isNewerVersion } from './utils/version'
import { shouldAutoOpenCaGuide } from './utils/caGuide'
import {
  CheckCircle2,
  Info,
  XCircle,
  AlertTriangle,
  Power,
  Network,
  AlertOctagon,
} from 'lucide-react'
import { Sidebar } from './components/navigation/Sidebar'
import { MobileNavDrawer } from './components/navigation/MobileNavDrawer'

export { RealtimeRequestHud } from './components/layout/RealtimeRequestHud'

export const App: React.FC = () => {
  const { language, setLanguage, t } = useI18n()
  const { theme, toggleTheme, setTheme } = useTheme()
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
  const [isMigrateStandardModalOpen, setIsMigrateStandardModalOpen] = useState<boolean>(false)
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
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState<boolean>(() => {
    try {
      return localStorage.getItem('gbf_sidebar_collapsed') === 'true'
    } catch {
      return false
    }
  })

  const handleToggleSidebar = useCallback(() => {
    setSidebarCollapsed((prev) => {
      const next = !prev
      try {
        localStorage.setItem('gbf_sidebar_collapsed', String(next))
      } catch {}
      return next
    })
  }, [])

  // Header auto-shrink state when user scrolls down
  const [isHeaderScrolled, setIsHeaderScrolled] = useState(false)

  const handleMainScroll = useCallback((e: React.UIEvent<HTMLDivElement>) => {
    const top = e.currentTarget.scrollTop
    if (top > 28) {
      setIsHeaderScrolled(true)
    } else if (top <= 14) {
      setIsHeaderScrolled(false)
    }
  }, [])

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

  // Proactive suggestion for macOS standard cache directory migration:
  // If running on macOS with cache outside the standard directory,
  // suggest migration to eliminate OS permission prompts. Respects "Don't show again".
  const darwinMigratePromptedRef = useRef(false)
  useEffect(() => {
    if (
      status?.platform === 'darwin' &&
      status.cache &&
      status.cache.is_standard_dir === false &&
      !darwinMigratePromptedRef.current
    ) {
      darwinMigratePromptedRef.current = true
      const dismissed =
        typeof localStorage !== 'undefined' &&
        localStorage.getItem('gbf_dismiss_darwin_cache_migrate') === 'true'
      if (!dismissed) {
        setIsMigrateStandardModalOpen(true)
      }
    }
  }, [status])

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
        if (c.value.theme) setTheme(c.value.theme, false)
      }
      if (cs.status === 'fulfilled') setCacheStats(cs.value)
      if (us.status === 'fulfilled') setUpstreamRuntime(us.value)
      if (lg.status === 'fulfilled') setLogs(lg.value)
    } catch (e) {
      console.error('Failed to fetch initial state', e)
    }
  }, [setLanguage, setTheme])

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
              const current = (cur?.version || '2.6.0').replace(/^v/, '').trim()
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

  // Migrate Cache to macOS Standard Application Support Directory
  const handleMigrateStandardCache = async (mode: CacheMigrationMode = 'migrate') => {
    let result: any
    await runConfigAction('migrate-standard', async () => {
      try {
        const res = await migrateToStandardCache({ mode })
        result = res
        if (res && res.new_dir) {
          setCacheDirInput(res.new_dir)
          setConfig((prev) => ({ ...prev, cache_dir: res.new_dir }))
          showToast(t('cards.cache.migrateSuccess', { path: res.new_dir }), 'success')
        }
        await loadState()
      } catch (e: any) {
        if (e.code === 'CACHE_MIGRATION_TARGET_NOT_EMPTY') {
          // Re-throw so MigrateStandardModal can catch and display the conflict resolution view
          throw e
        }
        showToast(t('cards.cache.migrateFailed', { error: e.message }), 'error')
        throw e
      }
    })
    return result
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
  const handleToggleFailoverEnabled = async () => {
    if (isDirect || Boolean(loadingAction)) return
    const next = !failoverEnabledInput
    setFailoverEnabledInput(next)
    if (!next) {
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
    setIsMobileMenuOpen(false)
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
    setIsMigrateStandardModalOpen(false)
    setIsUpstreamSelectModalOpen(false)
    setIsShimakazeSuggestOpen(false)
    if (!quitting) {
      setIsQuitModalOpen(false)
    }
    setCaModalAction(null)
  }

  const isAnyModalOpen =
    isMobileMenuOpen ||
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
    isMigrateStandardModalOpen ||
    isUpstreamSelectModalOpen ||
    isShimakazeSuggestOpen ||
    isQuitModalOpen ||
    caModalAction !== null ||
    isStandaloneLogsPage

  // Keyboard Shortcuts
  useKeyboardShortcuts({
    onToggleProxy: handleToggleProxy,
    onToggleTheme: toggleTheme,
    onToggleSidebar: handleToggleSidebar,
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
    <div className="min-h-screen w-full flex app-canvas text-label-primary font-sans antialiased selection:bg-sky-200 selection:text-slate-900">
      {/* Left Navigation Rail (Desktop: md:flex, Mobile: hidden) */}
      <Sidebar
        activeTab={activeTab}
        onTabChange={setActiveTab}
        showAndroidPatch={Boolean(showAndroidPatch)}
        isRunning={isRunning}
        hasUpdate={Boolean(updateInfo?.available)}
        loadingOpenFolder={loadingOpenFolder}
        language={language}
        onLanguageSelect={setLanguage}
        theme={theme}
        onToggleTheme={toggleTheme}
        onOpenLogs={openLogsWindow}
        onOpenHealth={() => setIsHealthModalOpen(true)}
        onOpenLatency={() => setIsLatencyModalOpen(true)}
        onOpenFolder={handleOpenCacheFolder}
        onOpenClearCache={() => setIsClearModalOpen(true)}
        onOpenRoutingGuide={() => setIsRoutingModalOpen(true)}
        onCheckUpdate={() => setIsUpdateModalOpen(true)}
        onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
        collapsed={sidebarCollapsed}
        onToggleCollapse={handleToggleSidebar}
      />

      {/* Mobile Navigation Drawer (Mobile: drawer overlay, Desktop: hidden) */}
      <MobileNavDrawer
        isOpen={isMobileMenuOpen}
        onClose={() => setIsMobileMenuOpen(false)}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        showAndroidPatch={Boolean(showAndroidPatch)}
        isRunning={isRunning}
        hasUpdate={Boolean(updateInfo?.available)}
        loadingOpenFolder={loadingOpenFolder}
        language={language}
        onLanguageSelect={setLanguage}
        theme={theme}
        onToggleTheme={toggleTheme}
        onOpenLogs={openLogsWindow}
        onOpenHealth={() => setIsHealthModalOpen(true)}
        onOpenLatency={() => setIsLatencyModalOpen(true)}
        onOpenFolder={handleOpenCacheFolder}
        onOpenClearCache={() => setIsClearModalOpen(true)}
        onOpenRoutingGuide={() => setIsRoutingModalOpen(true)}
        onCheckUpdate={() => setIsUpdateModalOpen(true)}
        onOpenShortcuts={() => setIsShortcutsModalOpen(true)}
      />

      <div className="flex-1 min-w-0 flex flex-col h-screen overflow-hidden">
        {/* Liquid Glass Toast Notification */}
        {toast && (
          <div className="fixed top-4 left-1/2 -translate-x-1/2 z-[60] animate-toast-in pointer-events-none">
            <div className="liquid-glass px-4 py-2 rounded-full flex items-center gap-2 text-xs text-label-primary">
              {toast.type === 'success' && <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />}
              {toast.type === 'error' && <XCircle className="w-4 h-4 text-rose-500 shrink-0" />}
              {toast.type === 'warning' && <AlertTriangle className="w-4 h-4 text-amber-500 shrink-0" />}
              {toast.type === 'info' && <Info className="w-4 h-4 text-sky-500 shrink-0" />}
              <span className="font-medium tracking-tight">{toast.msg}</span>
            </div>
          </div>
        )}

        {/* Termination Notice Banner when user explicitly quits backend */}
        {isTerminated && (
          <div className="bg-rose-50/90 border-b border-rose-200/80 px-4 py-2.5 text-center text-xs sm:text-sm text-rose-800 font-semibold flex items-center justify-center gap-2 select-none animate-fade-soft shrink-0">
            <AlertOctagon className="w-4 h-4 text-rose-600 shrink-0" />
            <span>{t('header.terminatedNotice')}</span>
          </div>
        )}

        {/* Scrollable Main Work Area (contains sticky floating HeaderCockpit for live glass refraction) */}
        <div className="flex-1 overflow-y-auto min-h-0" onScroll={handleMainScroll}>
          {activeTab === 'core' && (
            <HeaderCockpit
              status={status}
              isRunning={isRunning}
              isScrolled={isHeaderScrolled}
              isTerminated={isTerminated}
              loadingProxy={loadingProxy}
              currentListenPort={currentListenPort}
              uptimeSec={uptimeSec}
              updateInfo={updateInfo}
              onToggleProxy={handleToggleProxy}
              onOpenUpdate={() => setIsUpdateModalOpen(true)}
              onOpenQuit={() => setIsQuitModalOpen(true)}
              onOpenMobileMenu={() => setIsMobileMenuOpen(true)}
            />
          )}

          <main
            className={`w-full max-w-5xl mx-auto px-4 sm:px-6 pb-8 sm:pb-10 flex flex-col justify-start gap-4 ${
              activeTab === 'android' ? 'pt-2.5 sm:pt-3' : 'pt-3 sm:pt-4'
            }`}
          >
            {activeTab === 'core' ? (
              <>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <CacheCard
                    isAndroid={isAndroid}
                    status={status}
                    cacheStats={cacheStats}
                    loadingAction={loadingAction}
                    loadingBrowse={loadingBrowse}
                    loadingOpenFolder={loadingOpenFolder}
                    cacheDirInput={cacheDirInput}
                    setCacheDirInput={setCacheDirInput}
                    ramMbInput={ramMbInput}
                    setRamMbInput={setRamMbInput}
                    ramUsageMb={ramUsageMb}
                    ramMaxMb={ramMaxMb}
                    isRamCache={isRamCache}
                    isRamWarmup={isRamWarmup}
                    isAutoRepair={isAutoRepair}
                    onSaveCacheDir={handleSaveCacheDir}
                    onBrowseDir={handleBrowseDir}
                    onOpenCacheFolder={handleOpenCacheFolder}
                    onDetectAcgp={handleDetectAcgp}
                    onOpenMigrateStandard={() => setIsMigrateStandardModalOpen(true)}
                    onToggleRamCache={handleToggleRamCache}
                    onApplyRamMb={handleApplyRamMb}
                    onOpenBoost={() => setIsBoostModalOpen(true)}
                    onToggleRamWarmup={handleToggleRamWarmup}
                    onToggleAutoRepair={handleToggleAutoRepair}
                    onOpenAudit={() => setIsAuditModalOpen(true)}
                    onOpenClearCache={() => setIsClearModalOpen(true)}
                  />

                  {!isAndroid && (
                    <UpstreamCard
                      status={status}
                      upstreamRuntime={upstreamRuntime}
                      language={language}
                      loadingAction={loadingAction}
                      isDirect={isDirect}
                      isShimakaze={isShimakaze}
                      isAllowLan={isAllowLan}
                      currentListenPort={currentListenPort}
                      upstreamInput={upstreamInput}
                      setUpstreamInput={setUpstreamInput}
                      backupUpstreamInput={backupUpstreamInput}
                      setBackupUpstreamInput={setBackupUpstreamInput}
                      failoverEnabledInput={failoverEnabledInput}
                      failoverThresholdInput={failoverThresholdInput}
                      setFailoverThresholdInput={setFailoverThresholdInput}
                      failoverConsecutiveInput={failoverConsecutiveInput}
                      setFailoverConsecutiveInput={setFailoverConsecutiveInput}
                      failoverCooldownInput={failoverCooldownInput}
                      setFailoverCooldownInput={setFailoverCooldownInput}
                      failoverAutoRecoverInput={failoverAutoRecoverInput}
                      setFailoverAutoRecoverInput={setFailoverAutoRecoverInput}
                      failoverNotificationInput={failoverNotificationInput}
                      setFailoverNotificationInput={setFailoverNotificationInput}
                      portInput={portInput}
                      setPortInput={setPortInput}
                      onSaveUpstream={handleSaveUpstream}
                      onProbeUpstream={handleProbeUpstream}
                      onToggleFailoverEnabled={handleToggleFailoverEnabled}
                      onProbeBackupUpstream={handleProbeBackupUpstream}
                      onSaveFailover={handleSaveFailover}
                      onToggleDirect={handleToggleDirect}
                      onToggleShimakaze={handleToggleShimakaze}
                      onSavePort={handleSavePort}
                      onResetPort={handleResetPort}
                      onToggleAllowLan={handleToggleAllowLan}
                      onOpenMobileGuide={() => setIsMobileModalOpen(true)}
                    />
                  )}

                  {!isAndroid && (
                    <NetworkCertCard
                      status={status}
                      loadingAction={loadingAction}
                      caStatusKnown={caStatusKnown}
                      isCaInstalled={isCaInstalled}
                      caFingerprint={caFingerprint}
                      isAutoPac={isAutoPac}
                      isAutoStart={isAutoStart}
                      isAutoUpdate={isAutoUpdate}
                      onOpenCaInstall={() => setCaModalAction('install')}
                      onOpenCaUninstall={() => setCaModalAction('uninstall')}
                      onToggleAutoPac={handleToggleAutoPac}
                      onToggleAutoStart={handleToggleAutoStart}
                      onToggleAutoUpdate={handleToggleAutoUpdate}
                    />
                  )}

                  <OptimizationCard
                    loadingAction={loadingAction}
                    isPrefetch={isPrefetch}
                    isBrowserCache={isBrowserCache}
                    onTogglePrefetch={handleTogglePrefetch}
                    onToggleBrowserCache={handleToggleBrowserCache}
                  />
                </div>

                <TelemetryPanel
                  totalHitRate={totalHitRate}
                  ramHitPct={ramHitPct}
                  diskHitPct={diskHitPct}
                  missHitPct={missHitPct}
                  ramHitsCount={ramHitsCount}
                  diskHitsCount={diskHitsCount}
                  downloadsCount={downloadsCount}
                  hitsCount={hitsCount}
                  connReusePercent={connReusePercent}
                  reusedConnections={reusedConnections}
                  newConnections={newConnections}
                  activeForeground={activeForeground}
                  h2Count={h2Count}
                  h1Count={h1Count}
                  latencySamples={latencySamples}
                  p50Latency={p50Latency}
                  p95Latency={p95Latency}
                  activeApiCount={activeApiCount}
                  isPrefetch={isPrefetch}
                  prefetchReusedCount={prefetchReusedCount}
                />
              </>
            ) : showAndroidPatch ? (
              <AndroidPatchPanel showToast={showToast} onOpenMobileMenu={() => setIsMobileMenuOpen(true)} />
            ) : null}
          </main>
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

      {status?.platform === 'darwin' && (
        <MigrateStandardModal
          isOpen={isMigrateStandardModalOpen}
          onClose={() => setIsMigrateStandardModalOpen(false)}
          currentDir={status?.cache?.cache_dir || cacheDirInput}
          targetDir={status?.cache?.standard_cache_dir || '~/Library/Application Support/GBF-Accelerator/cache'}
          onMigrate={handleMigrateStandardCache}
        />
      )}

      <UpdateModal
        isOpen={isUpdateModalOpen}
        onClose={() => setIsUpdateModalOpen(false)}
        currentVersion={status?.version || '2.6.0'}
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
        <div className="space-y-4 text-xs sm:text-sm text-label-secondary">
          <InsetPanel tone="rose" className="p-3.5 space-y-2 leading-relaxed">
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
          </InsetPanel>
          <div className="pt-2.5 flex justify-end gap-2.5 hairline-t">
            <Button
              type="button"
              variant="desktop"
              size="md"
              disabled={quitting}
              onClick={() => setIsQuitModalOpen(false)}
            >
              {t('quitModal.cancel')}
            </Button>
            <Button
              type="button"
              variant="danger"
              size="md"
              icon={<Power className="w-4 h-4" />}
              loading={quitting}
              onClick={handleQuitApp}
            >
              {quitting ? t('common.quitting') : t('quitModal.confirm')}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  )
}

export default App