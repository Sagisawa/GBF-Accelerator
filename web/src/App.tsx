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
  Power,
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
            <span className="text-sm sm:text-base">⚡</span> 本地缓存命中
          </span>
          <div className="flex items-baseline gap-1.5 mt-1 flex-wrap">
            <span className="text-2xl sm:text-3xl font-extrabold text-emerald-600 font-mono tnum leading-tight">
              {counts.totalHits.toLocaleString()}
            </span>
            {counts.ramHits > 0 && (
              <span className="text-xs font-mono font-medium text-emerald-700 bg-emerald-50 px-1.5 py-0.5 rounded border border-emerald-200/60">
                RAM {counts.ramHits.toLocaleString()}
              </span>
            )}
          </div>
        </div>
      </div>

      <div className="bg-slate-50/80 border border-slate-200/90 rounded-xl px-4 sm:px-5 py-3 sm:py-3.5 flex items-center justify-between shadow-2xs hover:bg-slate-50 transition-colors">
        <div className="flex flex-col">
          <span className="text-xs sm:text-[13px] font-semibold text-slate-600 flex items-center gap-1.5">
            <span className="text-sm sm:text-base">📥</span> 远程下载缓存
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
            <span className="text-sm sm:text-base">🔄</span> 游戏 API 转发
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
  const [isAuditModalOpen, setIsAuditModalOpen] = useState<boolean>(false)
  const [isSlimModalOpen, setIsSlimModalOpen] = useState<boolean>(false)
  const [isUpdateModalOpen, setIsUpdateModalOpen] = useState<boolean>(false)
  const [isUpstreamSelectModalOpen, setIsUpstreamSelectModalOpen] = useState<boolean>(false)
  const [upstreamCandidates, setUpstreamCandidates] = useState<ProxyCandidate[]>([])
  const [isShimakazeSuggestOpen, setIsShimakazeSuggestOpen] = useState<boolean>(false)
  const [shimakazeTargetName, setShimakazeTargetName] = useState<string>('岛风 GO (8099)')
  const [caModalAction, setCaModalAction] = useState<'install' | 'uninstall' | null>(null)
  const [updateInfo, setUpdateInfo] = useState<{ available: boolean; version: string } | null>(null)
  const [isQuitModalOpen, setIsQuitModalOpen] = useState<boolean>(false)
  const [quitting, setQuitting] = useState<boolean>(false)
  const [isTerminated, setIsTerminated] = useState<boolean>(false)

  const handleQuitApp = async () => {
    setQuitting(true)
    try {
      await quitApp()
      setIsQuitModalOpen(false)
      setIsTerminated(true)
      showToast('加速器后台已安全退出，端口已释放。您可以直接关闭此标签页。', 'success')
    } catch (err: any) {
      showToast(err?.message || '退出失败', 'error')
    } finally {
      setQuitting(false)
    }
  }

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

  const isStandalone = typeof window !== 'undefined' && (
    window.matchMedia('(display-mode: standalone)').matches ||
    window.location.search.includes('standalone') ||
    Boolean((window.navigator as any).standalone)
  )

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
        if (c.value.listen_port) setPortInput(String(c.value.listen_port))
        if (c.value.ram_cache_max_mb) setRamMbInput(String(c.value.ram_cache_max_mb))
      }
      if (cs.status === 'fulfilled') setCacheStats(cs.value)
      if (us.status === 'fulfilled') setUpstreamRuntime(us.value)
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

      es.addEventListener('metrics', (e) => {
        try {
          const data = JSON.parse(e.data)
          if (data?.requests) {
            window.dispatchEvent(new CustomEvent('gbf-metrics', { detail: data }))
          }
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
              const current = (cur?.version || '2.0.0').replace(/^v/, '').trim()
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

    return () => {
      clearInterval(interval)
      if (es) es.close()
    }
  }, [loadState])

  // Save user-adjusted window dimensions and maximized state across restarts
  useEffect(() => {
    if (!isStandalone) return

    let timer: ReturnType<typeof setTimeout> | null = null

    const saveGeometry = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return

      const w = window.outerWidth || window.innerWidth
      const h = window.outerHeight || window.innerHeight

      const isMaximized = Boolean(
        window.screen &&
        w >= window.screen.availWidth - 20 &&
        h >= window.screen.availHeight - 20
      )

      const patch: Record<string, any> = { window_maximized: isMaximized }
      if (!isMaximized && w >= 400 && h >= 400) {
        patch.window_width = Math.round(w)
        patch.window_height = Math.round(h)
      }

      applyConfig(patch).catch(() => {})
    }

    const handleResize = () => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(saveGeometry, 800)
    }

    window.addEventListener('resize', handleResize)
    window.addEventListener('beforeunload', saveGeometry)

    return () => {
      if (timer) clearTimeout(timer)
      window.removeEventListener('resize', handleResize)
      window.removeEventListener('beforeunload', saveGeometry)
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
      showToast(targetRunning ? '加速代理服务已成功启动' : '加速代理服务已停止', 'success')
      loadState()
    } catch (e: any) {
      showToast(`代理操作失败: ${e.message}`, 'error')
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
        await applyConfig({ direct_mode: nextDirect })
        setConfig((prev) => ({ ...prev, direct_mode: nextDirect }))
        showToast(nextDirect ? '已开启直连模式 (使用本机网络，不经过上游代理)' : '已恢复上游代理转发链路', 'info')
        loadState()
      } catch (e: any) {
        showToast(`切换直连模式失败: ${e.message}`, 'error')
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
        showToast(next ? '已开启岛风GO 兼容优化模式' : '已关闭岛风GO 兼容优化模式', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置岛风GO模式失败: ${e.message}`, 'error')
      }
    })
  }
  // Open Cache Folder
  const handleOpenCacheFolder = async () => {
    if (loadingOpenFolder) return
    setLoadingOpenFolder(true)
    try {
      await openCacheFolder()
      showToast('已在系统文件管理器中打开缓存目录', 'info')
    } catch (e: any) {
      showToast(`打开目录失败: ${e.message}`, 'error')
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
        showToast(`缓存目录已成功更改为：${chosen}`, 'success')
        loadState()
      }
    } catch (e: any) {
      showToast(`选择目录失败: ${e.message}`, 'error')
    } finally {
      setLoadingBrowse(false)
    }
  }

  // Detect ACGPower Cache
  const handleDetectAcgp = async () => {
    await runConfigAction('detect-acgp', async () => {
      try {
        const data = await detectACGPower()
        if (data && data.found && data.path) {
          await applyConfig({ cache_dir: data.path })
          setCacheDirInput(data.path)
          showToast(`已检测并关联 ACGPower 缓存目录: ${data.path}`, 'success')
        } else {
          showToast(data?.message || '未检测到正在运行的 ACGPower 或默认缓存目录', 'info')
        }
        loadState()
      } catch (e: any) {
        showToast(`检测 ACGP 失败: ${e.message}`, 'error')
      }
    })
  }
  // Check and prompt if upstream is Shimakaze GO (port 8099) or ACGPower (port 8123)
  const checkShimakazeSuggest = (url: string) => {
    const is8099 = url.includes(':8099')
    const is8123 = url.includes(':8123')
    const currentShimakaze = Boolean(config.shimakaze_mode ?? false)
    if ((is8099 || is8123) && !currentShimakaze) {
      setShimakazeTargetName(is8123 ? 'ACGPower (8123)' : '岛风 GO (8099)')
      setIsShimakazeSuggestOpen(true)
    }
  }

  // Enable Shimakaze mode from suggestion modal
  const handleEnableShimakaze = async () => {
    try {
      await applyConfig({ shimakaze_mode: true })
      setConfig((prev) => ({ ...prev, shimakaze_mode: true }))
      const isAcgp = shimakazeTargetName.includes('8123')
      showToast(`已成功启用【${isAcgp ? 'ACGPower' : '岛风GO'} 兼容优化模式（放行证书）】`, 'success')
      loadState()
    } catch (e: any) {
      showToast(`启用兼容模式失败: ${e.message}`, 'error')
    }
  }  // Save Upstream Proxy
  const handleSaveUpstream = async () => {
    const trimmed = upstreamInput.trim()
    if (!trimmed) {
      showToast('上游代理地址不能为空', 'error')
      return
    }
    await runConfigAction('save-upstream', async () => {
      try {
        await applyConfig({ upstream_proxy: trimmed })
        setConfig((prev) => ({ ...prev, upstream_proxy: trimmed }))
        showToast(`上游代理地址已保存并切换至：${trimmed}`, 'success')
        loadState()
        checkShimakazeSuggest(trimmed)
      } catch (e: any) {
        showToast(`保存上游代理失败: ${e.message}`, 'error')
      }
    })
  }
  // Save and configure backup upstream failover
  const handleSaveFailover = async () => {
    const backup = backupUpstreamInput.trim()
    if (failoverEnabledInput && !backup) {
      showToast('启用备用上游前，请先填写备用上游地址', 'error')
      return
    }

    const threshold = Number.parseInt(failoverThresholdInput.trim(), 10)
    const consecutive = Number.parseInt(failoverConsecutiveInput.trim(), 10)
    const cooldown = Number.parseInt(failoverCooldownInput.trim(), 10)

    if (!Number.isInteger(threshold) || threshold < 0 || threshold > 60000) {
      showToast('延迟阈值须为 0 到 60000 ms', 'error')
      return
    }
    if (!Number.isInteger(consecutive) || consecutive < 1 || consecutive > 10) {
      showToast('连续异常次数须为 1 到 10 次', 'error')
      return
    }
    if (!Number.isInteger(cooldown) || cooldown < 60 || cooldown > 3600) {
      showToast('切回等待时间须为 60 到 3600 秒', 'error')
      return
    }

    if (backup && backup.toLowerCase() !== 'direct') {
      const lower = backup.toLowerCase()
      const supported = ['http://', 'https://', 'socks5://', 'socks5h://'].some((prefix) => lower.startsWith(prefix))
      if (!supported) {
        showToast('备用上游仅支持 http://、https://、socks5://、socks5h:// 或 direct', 'error')
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
        })
        setConfig((prev) => ({
          ...prev,
          backup_upstream_proxy: backup,
          enable_upstream_failover: failoverEnabledInput,
          upstream_failover_threshold_ms: threshold,
          upstream_failover_consecutive_failures: consecutive,
          upstream_failover_cooldown_seconds: cooldown,
          upstream_failover_auto_recover: failoverAutoRecoverInput,
        }))
        showToast(
          failoverEnabledInput
            ? '备用上游故障转移配置已保存'
            : '备用上游配置已保存，自动切换保持关闭',
          'success'
        )
        loadState()
      } catch (e: any) {
        showToast(`保存备用上游配置失败: ${e.message}`, 'error')
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
          setIsUpstreamSelectModalOpen(true)
        } else if (candidates.length === 1 || (data && data.found && data.primary)) {
          const chosen = candidates.length === 1 ? candidates[0].url : data.primary
          await applyConfig({ upstream_proxy: chosen })
          setUpstreamInput(chosen)
          showToast(`已探测并应用上游代理: ${chosen}`, 'success')
          loadState()
          checkShimakazeSuggest(chosen)
        } else {
          await applyConfig({ upstream_proxy: 'auto' })
          setUpstreamInput('auto')
          showToast('未检测到活跃上游代理端口，已重置为 auto 模式', 'info')
          loadState()
        }
      } catch (e: any) {
        showToast(`探测上游代理失败: ${e.message}`, 'error')
      }
    })
  }
  // Handle selection from UpstreamSelectModal
  const handleSelectUpstreamCandidate = async (candidate: ProxyCandidate) => {
    await runConfigAction('upstream-select', async () => {
      try {
        await applyConfig({ upstream_proxy: candidate.url })
        setUpstreamInput(candidate.url)
        showToast(`已切换至上游代理: ${candidate.name} (${candidate.url})`, 'success')
        loadState()
        checkShimakazeSuggest(candidate.url)
      } catch (e: any) {
        showToast(`切换上游代理失败: ${e.message}`, 'error')
      }
    })
  }
  // Save Listen Port
  const handleSavePort = async () => {
    const p = parseInt(portInput.trim(), 10)
    if (isNaN(p) || p < 1 || p > 65535) {
      showToast('请输入 1 到 65535 之间的有效端口号', 'error')
      return
    }
    await runConfigAction('save-port', async () => {
      try {
        await applyConfig({ listen_port: p, port: p })
        setConfig((prev) => ({ ...prev, listen_port: p }))
        showToast(isRunning ? `本地监听端口已切换为 ${p}，运行中的代理已热重载` : `本地监听端口已保存为 ${p}，下次启动代理时生效`, 'success')
        loadState()
      } catch (e: any) {
        showToast(`保存端口失败: ${e.message}`, 'error')
      }
    })
  }
  // Reset Listen Port Default
  const handleResetPort = async () => {
    await runConfigAction('reset-port', async () => {
      try {
        await applyConfig({ listen_port: 8124, port: 8124 })
        setPortInput('8124')
        setConfig((prev) => ({ ...prev, listen_port: 8124 }))
        showToast(isRunning ? '监听端口已恢复默认 (8124)，运行中的代理已热重载' : '监听端口已恢复默认 (8124)', 'info')
        loadState()
      } catch (e: any) {
        showToast(`恢复端口失败: ${e.message}`, 'error')
      }
    })
  }
  // Toggle Allow LAN
  const handleToggleAllowLan = async () => {
    const current = Boolean(config.allow_lan ?? status?.allow_lan ?? false)
    const next = !current
    await runConfigAction('allow-lan', async () => {
      try {
        await applyConfig({ allow_lan: next })
        setConfig((prev) => ({ ...prev, allow_lan: next }))
        showToast(next ? '允许局域网连接已开启 (绑定 0.0.0.0)' : '局域网连接已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置局域网共享失败: ${e.message}`, 'error')
      }
    })
  }
  // Toggle System Proxy PAC
  const handleToggleAutoPac = async () => {
    const current = Boolean(config.auto_system_proxy ?? config.auto_pac ?? false)
    const next = !current
    await runConfigAction('auto-pac', async () => {
      try {
        await applyConfig({ auto_system_proxy: next, auto_pac: next })
        setConfig((prev) => ({ ...prev, auto_system_proxy: next, auto_pac: next }))
        showToast(next ? '自动配置系统 PAC 代理已开启' : '系统 PAC 代理已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置系统 PAC 代理失败: ${e.message}`, 'error')
      }
    })
  }
  // Toggle Auto Start
  const handleToggleAutoStart = async () => {
    const current = Boolean(config.auto_start ?? false)
    const next = !current
    await runConfigAction('auto-start', async () => {
      try {
        await applyConfig({ auto_start: next })
        setConfig((prev) => ({ ...prev, auto_start: next }))
        showToast(next ? '开机自启已开启' : '开机自启已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置开机自启失败: ${e.message}`, 'error')
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
        showToast(next ? '启动时自动检测新版本已开启' : '自动检测更新已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置自动更新失败: ${e.message}`, 'error')
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
        showToast(next ? '启用内存热点缓存 (RAM Cache)' : '已关闭内存热点缓存', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置内存缓存失败: ${e.message}`, 'error')
      }
    })
  }
  const handleApplyRamMb = async () => {
    const mb = parseInt(ramMbInput.trim(), 10)
    if (isNaN(mb) || mb < 64 || mb > 2048) {
      showToast('请输入 64 到 2048 MB 之间的有效内存上限', 'error')
      return
    }
    await runConfigAction('ram-mb', async () => {
      try {
        await applyConfig({ ram_cache_max_mb: mb })
        setConfig((prev) => ({ ...prev, ram_cache_max_mb: mb }))
        showToast(`内存缓存上限已设置为 ${mb} MB`, 'success')
        loadState()
      } catch (e: any) {
        showToast(`设置内存上限失败: ${e.message}`, 'error')
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
        showToast(next ? '浏览器强缓存与渲染留存已开启' : '浏览器强缓存已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置浏览器强缓存失败: ${e.message}`, 'error')
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
        showToast(next ? '自动检测并修复损坏/空缓存已开启' : '自动修复已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置自动修复失败: ${e.message}`, 'error')
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
        showToast(next ? '场景素材智能预加载已开启' : '素材预加载已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置预加载失败: ${e.message}`, 'error')
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
        showToast(next ? '启动时预热内存缓存已开启' : '预热内存已关闭', 'info')
        loadState()
      } catch (e: any) {
        showToast(`设置预热内存失败: ${e.message}`, 'error')
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
    onToggleLogs: openLogsWindow,
    onToggleDirect: handleToggleDirect,
    onOpenClearModal: () => setIsClearModalOpen(true),
    onOpenShortcutsModal: () => setIsShortcutsModalOpen(true),
    onCloseAll: handleCloseAll,
    isModalOpen: isAnyModalOpen,
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
  const currentControlPort = config.control_port ?? status?.control_port ?? 8125
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
            {toast.type === 'info' && <Info className="w-4 h-4 text-sky-400 shrink-0" />}
            <span className="font-medium tracking-tight">{toast.msg}</span>
          </div>
        </div>
      )}

      {/* Termination Notice Banner when user explicitly quits backend */}
      {isTerminated && (
        <div className="bg-rose-50 border-b border-rose-200 px-4 py-2.5 text-center text-xs sm:text-sm text-rose-800 font-semibold flex items-center justify-center gap-2 select-none animate-in fade-in duration-200">
          <span>🛑</span>
          <span>GBF-Accelerator 后台服务已安全退出，端口 8124 与 8125 已释放。您可以随时关闭此网页标签。</span>
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
                    碧蓝幻想 GBF 加速器
                  </span>
                  <span className="text-xs sm:text-[13px] font-mono font-bold px-2.5 py-1 rounded-lg bg-slate-100/90 text-slate-700 border border-slate-200/90 shadow-2xs">
                    v{status?.version || '2.0.0'}
                  </span>
                  {updateInfo?.available && (
                    <button
                      type="button"
                      onClick={() => setIsUpdateModalOpen(true)}
                      className="px-3 py-1 rounded-lg bg-amber-50 hover:bg-amber-100 text-amber-950 border border-amber-300 text-xs sm:text-[13px] font-bold inline-flex items-center gap-1.5 cursor-pointer active:scale-[0.98] transition-all shadow-2xs"
                    >
                      <span className="text-sm">🔥</span>
                      <span>发现新版 v{updateInfo.version}</span>
                    </button>
                  )}
                </div>
                <div className="flex items-center gap-2 mt-1.5">
                  <span className="inline-flex items-center gap-1.5 text-xs sm:text-[13px]">
                    <span className={`w-2.5 h-2.5 rounded-full ${isRunning ? 'bg-emerald-500 animate-pulse' : 'bg-slate-400'}`} />
                    <span className={isRunning ? 'font-semibold text-emerald-700' : 'text-slate-500 font-medium'}>
                      {isRunning ? `运行中 (监听端口 ${currentListenPort})` : '已停止'}
                    </span>
                  </span>
                </div>
              </div>
            </div>

            {/* Top Right Master Start / Stop Action Button & Quit Button */}
            <div className="flex items-center gap-2 sm:gap-2.5">
              <button
                type="button"
                disabled={loadingProxy || isTerminated}
                onClick={handleToggleProxy}
                className={`min-w-[110px] px-5 py-2.5 rounded-xl text-sm sm:text-base font-bold text-white shadow-xs transition-all cursor-pointer select-none active:scale-[0.98] ${
                  isRunning
                    ? 'bg-rose-600 hover:bg-rose-700 active:bg-rose-800'
                    : 'bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800'
                }`}
              >
                {loadingProxy ? '处理中...' : isRunning ? '停止加速' : '启动加速'}
              </button>
              <button
                type="button"
                disabled={isTerminated}
                onClick={() => setIsQuitModalOpen(true)}
                className="px-3.5 py-2.5 rounded-xl text-xs sm:text-sm font-semibold text-slate-600 hover:text-rose-600 bg-slate-100/90 hover:bg-rose-50 border border-slate-200/90 hover:border-rose-300 transition-all cursor-pointer select-none active:scale-[0.98] flex items-center gap-1.5 shadow-2xs"
                title="彻底退出 GBF 加速器后台服务 (释放 8124 与 8125 端口)"
              >
                <Power className="w-4 h-4 text-rose-500" />
                <span>彻底退出</span>
              </button>
            </div>
          </div>

          {/* Realtime request counters: isolated from the root App render loop */}
          <RealtimeRequestHud status={status} running={isRunning} />
        </div>
      </header>

      {/* 2. Main Work Area (Natural Content-Fit, 2-Column Responsive Card Grid) */}
      <main className="w-full max-w-5xl mx-auto px-4 sm:px-6 py-4 sm:py-5 flex flex-col justify-start gap-4 sm:gap-5 flex-1">
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 sm:gap-4.5">
          {/* Card 1: Local Cache & Storage */}
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">📁</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    本地缓存与存储管理
                  </h2>
                </div>
                <span className="text-xs text-emerald-700 font-semibold px-2.5 py-0.5 rounded-full bg-emerald-50 border border-emerald-200/60">
                  RAM + 磁盘双层
                </span>
              </div>

              {/* Local Cache Dir */}
              <div className="space-y-2">
                <label className="text-[13px] sm:text-sm text-slate-700 font-medium block">
                  本地缓存目录（支持无缝复用 ACGPower 缓存）：
                </label>
                <div className="flex items-center gap-2 flex-wrap sm:flex-nowrap">
                  <input
                    type="text"
                    disabled={Boolean(loadingAction) || loadingBrowse}
                    value={cacheDirInput}
                    onChange={(e) => setCacheDirInput(e.target.value)}
                    className="flex-1 min-w-[160px] bg-slate-50/70 border border-slate-200 rounded-lg px-3 py-1.5 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs transition-all"
                  />
                  <button
                    type="button"
                    onClick={handleBrowseDir}
                    disabled={loadingBrowse}
                    aria-busy={loadingBrowse}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs disabled:opacity-60 disabled:cursor-wait disabled:hover:bg-slate-50"
                  >
                    {loadingBrowse ? '正在打开...' : '浏览...'}
                  </button>
                  <button
                    type="button"
                    onClick={handleDetectAcgp}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('detect-acgp')}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer shadow-2xs"
                  >
                    {isActionLoading('detect-acgp') ? '检测中...' : '检测 ACGP'}
                  </button>
                </div>
                {/* Health & Slim buttons */}
                <div className="flex items-center gap-2 pt-1">
                  <button
                    type="button"
                    onClick={() => setIsAuditModalOpen(true)}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1.5 shadow-2xs"
                  >
                    <span>🩺</span>
                    <span>一键体检缓存</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setIsSlimModalOpen(true)}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1.5 shadow-2xs"
                  >
                    <span>🧹</span>
                    <span>缓存安全瘦身</span>
                  </button>
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
                    启用内存热点缓存 (RAM Cache) - 占用上限约 {ramMaxMb}MB，高频静态资源 0 磁盘 I/O 直接响应
                  </span>
                </label>

                <div className="ml-6 flex items-center gap-2.5 flex-wrap text-xs sm:text-sm text-slate-700">
                  <span>上限 (MB):</span>
                  <input
                    type="text"
                    disabled={Boolean(loadingAction)}
                    value={ramMbInput}
                    onChange={(e) => setRamMbInput(e.target.value)}
                    className="w-18 bg-slate-50/70 border border-slate-200 rounded-lg px-2.5 py-1 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs"
                  />
                  <button
                    type="button"
                    onClick={handleApplyRamMb}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('ram-mb')}
                    className="px-3 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                  >
                    {isActionLoading('ram-mb') ? '应用中...' : '应用'}
                  </button>
                  <span className="text-slate-500 font-mono text-xs">
                    {ramUsageMb} / {ramMaxMb} MB ({Math.min(100, Math.round((ramUsageMb / Math.max(1, ramMaxMb)) * 100))}%)
                  </span>
                  <div className="w-28 h-2.5 bg-slate-100 rounded-full overflow-hidden border border-slate-200/80 shrink-0">
                    <div
                      className="h-full bg-emerald-500 rounded-full transition-all duration-300"
                      style={{ width: `${Math.min(100, Math.round((ramUsageMb / Math.max(1, ramMaxMb)) * 100))}%` }}
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>

          {/* Card 2: Upstream Proxy & Network Routing */}
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">🌐</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    上游网络代理与分流
                  </h2>
                </div>
                <span className="text-xs text-sky-700 font-semibold px-2.5 py-0.5 rounded-full bg-sky-50 border border-sky-200/60">
                  Clash / 岛风GO / 直连
                </span>
              </div>

              {/* Upstream Proxy */}
              <div className="space-y-1.5">
                <label className="text-[13px] sm:text-sm text-slate-700 font-medium block">
                  上游网络代理（Clash Verge / Clash / V2ray / 岛风GO 等）：
                </label>
                <div className="flex items-center gap-2 flex-wrap sm:flex-nowrap">
                  <input
                    type="text"
                    disabled={isDirect || Boolean(loadingAction)}
                    value={upstreamInput}
                    onChange={(e) => setUpstreamInput(e.target.value)}
                    className="flex-1 min-w-[160px] bg-slate-50/70 border border-slate-200 rounded-lg px-3 py-1.5 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs transition-all disabled:bg-slate-100 disabled:text-slate-400"
                  />
                  <button
                    type="button"
                    disabled={isDirect || Boolean(loadingAction)}
                    onClick={handleSaveUpstream}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer disabled:opacity-50 shadow-2xs"
                  >
                    {isActionLoading('save-upstream') ? '保存中...' : '确认'}
                  </button>
                  <button
                    type="button"
                    disabled={isDirect || Boolean(loadingAction)}
                    aria-busy={isActionLoading('probe-upstream')}
                    onClick={handleProbeUpstream}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all shrink-0 cursor-pointer disabled:opacity-50 shadow-2xs"
                  >
                    {isActionLoading('probe-upstream') ? '探测中...' : '自动探测'}
                  </button>
                </div>
              </div>

              {/* Upstream Failover */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <div>
                    <div className="text-[13px] sm:text-sm text-slate-700 font-medium">
                      备用上游（自动故障转移）
                    </div>
                    <div className="text-xs text-slate-400 mt-0.5">
                      主上游连续异常后切换；不会在同一请求内重放业务请求
                    </div>
                  </div>
                  <span
                    className={
                      !failoverEnabledInput || !backupUpstreamInput.trim()
                        ? 'text-xs font-semibold px-2.5 py-0.5 rounded-full bg-slate-50 text-slate-500 border border-slate-200/60'
                        : upstreamRuntime?.active === 'backup'
                          ? 'text-xs font-semibold px-2.5 py-0.5 rounded-full bg-amber-50 text-amber-700 border border-amber-200/70'
                          : 'text-xs font-semibold px-2.5 py-0.5 rounded-full bg-emerald-50 text-emerald-700 border border-emerald-200/70'
                    }
                  >
                    {!failoverEnabledInput || !backupUpstreamInput.trim()
                      ? '未启用'
                      : isDirect
                        ? '直连模式不参与切换'
                        : upstreamRuntime?.active === 'backup'
                          ? '当前：备用'
                          : '当前：主上游'}
                  </span>
                </div>

                <label className="inline-flex items-start gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none leading-snug">
                  <input
                    type="checkbox"
                    checked={failoverEnabledInput}
                    onChange={(e) => setFailoverEnabledInput(e.target.checked)}
                    disabled={isDirect || Boolean(loadingAction)}
                    className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 disabled:opacity-50 accent-sky-600"
                  />
                  <span>
                    启用备用上游自动切换
                  </span>
                </label>

                <div className="flex items-center gap-2 flex-wrap sm:flex-nowrap">
                  <label className="text-xs sm:text-[13px] text-slate-600 shrink-0">
                    备用地址
                  </label>
                  <input
                    type="text"
                    value={backupUpstreamInput}
                    onChange={(e) => setBackupUpstreamInput(e.target.value)}
                    disabled={Boolean(loadingAction)}
                    placeholder="http://127.0.0.1:8080 / socks5://127.0.0.1:1080 / direct"
                    className="flex-1 min-w-[180px] bg-slate-50/70 border border-slate-200 rounded-lg px-3 py-1.5 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs transition-all"
                  />
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-3 gap-2.5">
                  <label className="flex items-center justify-between gap-2 p-2.5 rounded-lg bg-slate-50/70 border border-slate-200/70 text-xs">
                    <span className="text-slate-600">延迟阈值</span>
                    <span className="flex items-center gap-1">
                      <input
                        type="number"
                        min={0}
                        max={60000}
                        step={100}
                        value={failoverThresholdInput}
                        onChange={(e) => setFailoverThresholdInput(e.target.value)}
                        disabled={Boolean(loadingAction)}
                        className="w-20 bg-white border border-slate-200 rounded-md px-2 py-1 text-right font-mono text-slate-800"
                      />
                      <span className="text-slate-400">ms</span>
                    </span>
                  </label>
                  <label className="flex items-center justify-between gap-2 p-2.5 rounded-lg bg-slate-50/70 border border-slate-200/70 text-xs">
                    <span className="text-slate-600">连续异常</span>
                    <span className="flex items-center gap-1">
                      <input
                        type="number"
                        min={1}
                        max={10}
                        step={1}
                        value={failoverConsecutiveInput}
                        onChange={(e) => setFailoverConsecutiveInput(e.target.value)}
                        disabled={Boolean(loadingAction)}
                        className="w-16 bg-white border border-slate-200 rounded-md px-2 py-1 text-right font-mono text-slate-800"
                      />
                      <span className="text-slate-400">次</span>
                    </span>
                  </label>
                  <label className="flex items-center justify-between gap-2 p-2.5 rounded-lg bg-slate-50/70 border border-slate-200/70 text-xs">
                    <span className="text-slate-600">切回等待</span>
                    <span className="flex items-center gap-1">
                      <input
                        type="number"
                        min={60}
                        max={3600}
                        step={10}
                        value={failoverCooldownInput}
                        onChange={(e) => setFailoverCooldownInput(e.target.value)}
                        disabled={Boolean(loadingAction)}
                        className="w-16 bg-white border border-slate-200 rounded-md px-2 py-1 text-right font-mono text-slate-800"
                      />
                      <span className="text-slate-400">秒</span>
                    </span>
                  </label>
                </div>

                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <label className="inline-flex items-center gap-2 text-xs sm:text-[13px] text-slate-700 cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={failoverAutoRecoverInput}
                      onChange={(e) => setFailoverAutoRecoverInput(e.target.checked)}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                    />
                    <span>冷却后自动尝试切回主上游</span>
                  </label>

                  <button
                    type="button"
                    onClick={handleSaveFailover}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('save-failover')}
                    className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-white bg-sky-600 hover:bg-sky-700 active:bg-sky-800 border border-sky-600 active:scale-[0.98] transition-all cursor-pointer shadow-2xs disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    {isActionLoading('save-failover') ? '保存中...' : '保存备用上游'}
                  </button>
                </div>

                {failoverEnabledInput && backupUpstreamInput.trim() && !isDirect && (
                  <div className="p-2.5 rounded-lg bg-slate-50/80 border border-slate-200/70 text-xs text-slate-600">
                    <div className="flex items-center justify-between gap-2 flex-wrap">
                      <span>
                        当前链路：
                        <strong className={upstreamRuntime?.active === 'backup' ? 'text-amber-700' : 'text-emerald-700'}>
                          {upstreamRuntime?.active === 'backup' ? '备用上游' : '主上游'}
                        </strong>
                      </span>
                      <span className="font-mono text-slate-500">
                        连续异常 {upstreamRuntime?.failure_count ?? 0} / {upstreamRuntime?.consecutive_failures ?? Number.parseInt(failoverConsecutiveInput, 10) || 3}
                      </span>
                    </div>
                    <div className="mt-1.5 flex items-center justify-between gap-2 flex-wrap">
                      <span className="text-slate-500">
                        {upstreamRuntime?.reason
                          ? ({
                              timeout: '响应超时',
                              connection_error: '连接异常',
                              latency_threshold: '超过延迟阈值',
                              primary_recovered: '主上游已恢复',
                              primary_recovery_failed: '主上游恢复尝试未成功',
                            } as Record<string, string>)[upstreamRuntime.reason] || upstreamRuntime.reason
                          : '等待运行数据'}
                      </span>
                      <span className="font-mono text-slate-400">
                        {upstreamRuntime?.last_switch_at
                          ? `上次切换 ${new Date(upstreamRuntime.last_switch_at).toLocaleTimeString('zh-CN', { hour12: false })}`
                          : '尚未发生切换'}
                      </span>
                    </div>
                  </div>
                )}

                {failoverEnabledInput && !backupUpstreamInput.trim() && (
                  <div className="text-xs text-amber-700 bg-amber-50/80 border border-amber-200/70 rounded-lg px-3 py-2">
                    已勾选自动切换，但尚未填写备用上游地址；保存时会要求补全。
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
                  <span>直连模式（使用本机网络，不经过上游代理；仍使用本地缓存）</span>
                </label>

                <div>
                  <label className="inline-flex items-start gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none leading-snug">
                    <input
                      type="checkbox"
                      disabled={isDirect || Boolean(loadingAction)}
                      checked={isShimakaze}
                      onChange={handleToggleShimakaze}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 disabled:opacity-50 accent-sky-600"
                    />
                    <span className={isDirect ? 'text-slate-400' : 'text-slate-800'}>
                      岛风GO / ACGPower 兼容优化模式（放宽超时、自愈重试、放行自签证书；默认关闭）
                    </span>
                  </label>

                  {isShimakaze && !isDirect && (
                    <div className="mt-2 p-3 bg-sky-50/80 border border-sky-200/70 rounded-lg text-xs text-sky-900 leading-relaxed shadow-2xs">
                      提示：已开启兼容优化模式，放行岛风GO / ACGPower 等本地自签证书并优化网络超时；若遇游戏维护更新后新素材显示异常，在主界面点击【清理缓存】即可。
                    </div>
                  )}
                </div>
              </div>

              {/* Listen Port & Allow LAN */}
              <div className="space-y-2.5 pt-1.5 border-t border-slate-100">
                <div className="flex items-center gap-2 flex-wrap">
                  <label className="text-[13px] sm:text-sm text-slate-700 font-medium">本地监听端口：</label>
                  <input
                    type="text"
                    disabled={Boolean(loadingAction)}
                    value={portInput}
                    onChange={(e) => setPortInput(e.target.value)}
                    className="w-18 bg-slate-50/70 border border-slate-200 rounded-lg px-2.5 py-1 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20 focus:border-sky-500 shadow-2xs"
                  />
                  <button
                    type="button"
                    onClick={handleSavePort}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('save-port')}
                    className="px-3 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                  >
                    {isActionLoading('save-port') ? '保存中...' : '保存配置'}
                  </button>
                  <button
                    type="button"
                    onClick={handleResetPort}
                    disabled={Boolean(loadingAction)}
                    aria-busy={isActionLoading('reset-port')}
                    className="px-2.5 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-500 hover:text-slate-800 hover:bg-slate-100 border border-slate-200/80 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                  >
                    {isActionLoading('reset-port') ? '恢复中...' : '恢复默认 (8124)'}
                  </button>
                </div>

                <div className="space-y-1.5">
                  <div className="flex items-center gap-2.5 flex-wrap">
                    <label className="inline-flex items-center gap-2 text-[13px] sm:text-sm text-slate-800 cursor-pointer select-none">
                      <input
                        type="checkbox"
                        checked={isAllowLan}
                        onChange={handleToggleAllowLan}
                        disabled={Boolean(loadingAction)}
                        className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer accent-sky-600"
                      />
                      <span>允许局域网连接 (Allow LAN)</span>
                    </label>
                    <button
                      type="button"
                      onClick={() => setIsMobileModalOpen(true)}
                      className="px-2.5 py-1 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-1 shadow-2xs"
                    >
                      <span>📱</span>
                      <span>移动端/iOS 连接指引...</span>
                    </button>
                  </div>
                  {isAllowLan && (
                    <div className="text-xs text-sky-700 font-medium pl-6">
                      本机局域网 IP: {status?.lan_ip || '192.168.x.x'} (端口 {currentListenPort}) | 移动设备配置 Wi-Fi 代理为此地址
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>

          {/* Card 3: HTTPS Root CA & System Integration */}
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">🛡️</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    HTTPS 根证书与系统集成
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
                  {!caStatusKnown ? '检查中' : isCaInstalled ? '已信任' : '未安装'}
                </span>
              </div>

              {/* CA Status & Action Buttons */}
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-2 flex-wrap">
                  <span className="text-[13px] sm:text-sm text-slate-700 font-medium">
                    证书状态：
                    <strong className={!caStatusKnown ? 'text-slate-500 ml-1' : isCaInstalled ? 'text-emerald-700 ml-1' : 'text-rose-600 ml-1'}>
                      {!caStatusKnown ? '检查中...' : isCaInstalled ? '已信任 (正常解析)' : '未安装信任'}
                    </strong>
                  </span>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => setCaModalAction('install')}
                      className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                    >
                      一键安装/修复根证书
                    </button>
                    <button
                      type="button"
                      onClick={() => setCaModalAction('uninstall')}
                      className="px-3 py-1.5 rounded-lg text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
                    >
                      一键注销/卸载根证书
                    </button>
                  </div>
                </div>
                <div className="text-xs font-mono text-slate-500 pt-0.5 select-all break-all leading-normal">
                  SHA-256 指纹： {caFingerprint || '读取中...'}
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
                      自动配置系统 PAC 代理（开启后浏览器无需插件，仅分流 GBF 流量）
                    </span>
                  </label>
                  {status?.system_proxy_conflict && (
                    <div className="mt-2 ml-6 p-2.5 rounded-lg bg-amber-50/90 border border-amber-200/80 text-xs text-amber-900 leading-relaxed shadow-2xs">
                      <span className="font-semibold">⚠️ 检测到系统代理冲突：</span>
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
                    <span>开机自启（启动后自动缩小到系统托盘，默认关闭）</span>
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
                    <span>启动时自动检测新版本（发现新版时右上角提醒，默认开启）</span>
                  </label>
                </div>
              </div>
            </div>
          </div>

          {/* Card 4: Performance Acceleration & Asset Scheduling */}
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-4.5 flex flex-col justify-between gap-3.5 hover:border-slate-300/80 transition-colors">
            <div className="space-y-3.5">
              <div className="flex items-center justify-between pb-2 border-b border-slate-100">
                <div className="flex items-center gap-2">
                  <span className="text-lg">⚡</span>
                  <h2 className="text-sm sm:text-base font-bold text-slate-800 tracking-tight">
                    性能加速与调度优化
                  </h2>
                </div>
                <span className="text-xs text-amber-800 font-semibold px-2.5 py-0.5 rounded-full bg-amber-50 border border-amber-200/60">
                  削峰填谷 · 防黑屏
                </span>
              </div>

              <div className="space-y-3 text-[13px] sm:text-sm text-slate-800">
                <div>
                  <label className="inline-flex items-start gap-2 cursor-pointer select-none leading-snug">
                    <input
                      type="checkbox"
                      checked={isAutoRepair}
                      onChange={handleToggleAutoRepair}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                    />
                    <span>
                      自动检测并修复损坏/空缓存 - 自动识别并重下 0 字节损坏文件，防止黑屏卡死
                    </span>
                  </label>
                </div>

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
                      启用资源预加载 - 解析场景 JS 引用的素材并后台预热，首次进新副本/活动更流畅
                    </span>
                  </label>
                </div>

                <div>
                  <label className="inline-flex items-start gap-2 cursor-pointer select-none leading-snug">
                    <input
                      type="checkbox"
                      checked={isRamWarmup}
                      onChange={handleToggleRamWarmup}
                      disabled={Boolean(loadingAction)}
                      className="w-4 h-4 rounded text-sky-600 border-slate-300 focus:ring-sky-500/20 cursor-pointer mt-0.5 shrink-0 accent-sky-600"
                    />
                    <span>
                      启动时预热内存缓存 - 把高频小文件预先载入 RAM，消除会话首读的磁盘延迟
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
                      启用浏览器强缓存与渲染留存（仅对版本化静态资源注入 immutable，默认开启）
                    </span>
                  </label>
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
                加速效能与网络态势
              </h3>
              <span className="text-xs text-slate-400 font-mono hidden sm:inline">
                ENGINE TELEMETRY
              </span>
            </div>
            <div className="flex items-center gap-2 text-xs sm:text-[13px] text-slate-500 font-mono">
              <span className="text-slate-400">运行:</span>
              <span className="text-slate-700 font-semibold">
                <RuntimeUptime baseSeconds={uptimeSec} running={isRunning} />
              </span>
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-3.5 sm:gap-4">
            {/* Col 1: Asset Routing & Cache Ratio */}
            <div className="bg-slate-50/70 rounded-xl p-3.5 sm:p-4 border border-slate-200/70 flex flex-col justify-between gap-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] sm:text-sm font-semibold text-slate-700 flex items-center gap-1.5">
                  <span className="text-emerald-500 text-base">⚡</span>
                  <span>分流与命中占比</span>
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
                    title={`RAM 即时命中: ${ramHitsCount} (${ramHitPct}%)`}
                  />
                  <div
                    className="h-full bg-sky-500 transition-all duration-300"
                    style={{ width: `${diskHitPct}%` }}
                    title={`磁盘命中: ${diskHitsCount} (${diskHitPct}%)`}
                  />
                  <div
                    className="h-full bg-amber-400/90 transition-all duration-300"
                    style={{ width: `${missHitPct}%` }}
                    title={`CDN 上游下载: ${downloadsCount} (${missHitPct}%)`}
                  />
                </div>
                <div className="flex items-center justify-between text-xs text-slate-500 font-mono">
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0" />
                    RAM: {ramHitsCount}
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-sky-500 shrink-0" />
                    磁盘: {diskHitsCount}
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-2 rounded-full bg-amber-400 shrink-0" />
                    下载: {downloadsCount}
                  </span>
                </div>
              </div>

              <div className="text-xs sm:text-[13px] text-slate-500 flex items-center justify-between pt-1.5 border-t border-slate-200/60">
                <span>累计节省外网请求</span>
                <span className="font-mono font-semibold text-slate-800">{hitsCount} 次</span>
              </div>
            </div>

            {/* Col 2: HTTP/2 Multiplexing & Handshake Savings */}
            <div className="bg-slate-50/70 rounded-xl p-3.5 sm:p-4 border border-slate-200/70 flex flex-col justify-between gap-3">
              <div className="flex items-center justify-between">
                <span className="text-[13px] sm:text-sm font-semibold text-slate-700 flex items-center gap-1.5">
                  <span className="text-sky-500 text-base">🌐</span>
                  <span>HTTP/2 连接复用态势</span>
                </span>
                <span className="text-sm sm:text-base font-bold font-mono text-sky-600">
                  {connReusePercent > 0 ? `${connReusePercent}%` : '--'}
                </span>
              </div>

              <div className="space-y-2 text-xs sm:text-[13px] text-slate-600">
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">避免重复 TCP 握手:</span>
                  <span className="font-mono font-semibold text-emerald-600">
                    +{reusedConnections} 次
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">新建连接 / 活跃素材:</span>
                  <span className="font-mono text-slate-700">
                    {newConnections} / {activeForeground}
                  </span>
                </div>
              </div>

              <div className="text-xs sm:text-[13px] text-slate-500 flex items-center justify-between pt-1.5 border-t border-slate-200/60">
                <span>协议多路复用</span>
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
                  <span>上游延迟与调度水线</span>
                </span>
                <span className="text-xs font-mono text-slate-500">
                  样本: {latencySamples}
                </span>
              </div>

              <div className="space-y-2 text-xs sm:text-[13px] text-slate-600">
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">P50 延迟 / P95 尾延迟:</span>
                  <span className="font-mono font-semibold text-slate-800">
                    {p50Latency > 0 ? `${p50Latency}ms` : '--'} / {p95Latency > 0 ? `${p95Latency}ms` : '--'}
                  </span>
                </div>
                <div className="flex items-center justify-between">
                  <span className="text-slate-500">前台战斗 API 避让:</span>
                  <span className={`font-medium ${activeApiCount > 0 ? 'text-amber-600 font-semibold' : 'text-emerald-600'}`}>
                    {activeApiCount > 0 ? `避让中 (${activeApiCount} 活动)` : '待命就绪 (0 活动)'}
                  </span>
                </div>
              </div>

              <div className="text-xs sm:text-[13px] text-slate-500 flex items-center justify-between pt-1.5 border-t border-slate-200/60">
                <span>预加载命中复用</span>
                {!isPrefetch ? (
                  <span className="text-slate-400 font-medium">未启用</span>
                ) : prefetchReusedCount > 0 ? (
                  <span className="font-mono font-semibold text-indigo-600">
                    {prefetchReusedCount} 项 · 已命中
                  </span>
                ) : diskHitsCount + ramHitsCount > 0 ? (
                  <span className="font-medium text-emerald-600">
                    0 项 (缓存已完备)
                  </span>
                ) : (
                  <span className="font-medium text-slate-400">
                    待命中 (0 项)
                  </span>
                )}
              </div>
            </div>
          </div>
        </div>

      </main>

      {/* 3. Fixed Bottom Action Dock (Footer) */}
      <footer className="sticky bottom-0 shrink-0 bg-white/95 backdrop-blur-md border-t border-slate-200/90 px-4 sm:px-6 py-3 sm:py-3.5 shadow-[0_-2px_10px_rgba(0,0,0,0.03)] z-20">
        <div className="max-w-5xl mx-auto flex items-center justify-center flex-wrap gap-2.5 sm:gap-3">
          <button
            type="button"
            onClick={handleOpenCacheFolder}
            disabled={loadingOpenFolder}
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">📁</span>
            <span>{loadingOpenFolder ? '正在打开...' : '缓存目录'}</span>
          </button>

          <button
            type="button"
            onClick={() => setIsClearModalOpen(true)}
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">🗑</span>
            <span>清空缓存</span>
          </button>

          <button
            type="button"
            onClick={() => setIsRoutingModalOpen(true)}
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">🌐</span>
            <span>分流说明</span>
          </button>

          <a
            href="https://github.com/Sagisawa/GBF-Accelerator"
            target="_blank"
            rel="noreferrer"
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">⭐</span>
            <span>GitHub</span>
          </a>

          <button
            type="button"
            onClick={() => setIsUpdateModalOpen(true)}
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">🔄</span>
            <span>检查更新</span>
          </button>

          <button
            type="button"
            onClick={() => setIsLatencyModalOpen(true)}
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">📶</span>
            <span>延迟测试</span>
          </button>

          <button
            type="button"
            onClick={openLogsWindow}
            className="px-4 py-2 sm:py-2.5 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-50 hover:bg-slate-100 hover:text-slate-900 border border-slate-200 active:scale-[0.98] transition-all cursor-pointer flex items-center gap-2 shadow-2xs"
          >
            <span className="text-base">📜</span>
            <span>实时日志</span>
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
        targetName={shimakazeTargetName}
      />

      <UpdateModal
        isOpen={isUpdateModalOpen}
        onClose={() => setIsUpdateModalOpen(false)}
        currentVersion={status?.version || '2.0.0'}
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
            <span>确认彻底退出 GBF 加速器？</span>
          </div>
        }
        subtitle="终止后台代理与管理服务"
        maxWidth="max-w-md"
      >
        <div className="space-y-4 text-xs sm:text-sm text-slate-600">
          <div className="p-3.5 bg-rose-50/80 border border-rose-200/80 rounded-xl space-y-2 text-rose-900">
            <p className="font-semibold text-xs sm:text-sm">
              退出后将完全关闭代理与后台管理服务：
            </p>
            <ul className="list-disc list-inside space-y-1 text-xs text-rose-800">
              <li>释放数据代理端口 <strong>{currentListenPort}</strong></li>
              <li>释放 Web 控制台端口 <strong>{currentControlPort}</strong></li>
              <li>安全回写内存缓存数据至磁盘</li>
            </ul>
          </div>
          <p className="text-slate-500 text-xs leading-relaxed">
            如需再次使用，需在电脑桌面或终端重新启动程序。
          </p>
          <div className="pt-2 flex justify-end gap-2.5 border-t border-slate-100">
            <button
              type="button"
              disabled={quitting}
              onClick={() => setIsQuitModalOpen(false)}
              className="px-4 py-2 rounded-xl text-xs sm:text-sm font-medium text-slate-700 bg-slate-100 hover:bg-slate-200 transition-all cursor-pointer"
            >
              取消
            </button>
            <button
              type="button"
              disabled={quitting}
              onClick={handleQuitApp}
              className="px-5 py-2 rounded-xl text-xs sm:text-sm font-bold text-white bg-rose-600 hover:bg-rose-700 active:bg-rose-800 transition-all cursor-pointer shadow-xs flex items-center gap-1.5"
            >
              <Power className="w-4 h-4" />
              <span>{quitting ? '正在退出...' : '确认彻底退出'}</span>
            </button>
          </div>
        </div>
      </Modal>
    </div>
  )
}

export default App