import React, { useState, useEffect } from 'react'
import { 
  RuntimeStatus, 
  TelemetrySummary, 
  CacheStats, 
  PrefetchStatus, 
  LogItem 
} from './types'
import { 
  fetchStatus, 
  fetchConfig, 
  fetchTelemetry, 
  fetchCacheStats, 
  fetchPrefetchStatus, 
  fetchLogs, 
  toggleProxy 
} from './api'
import { Dashboard } from './pages/Dashboard'
import { CacheManager } from './pages/CacheManager'
import { TraceTimeline } from './pages/TraceTimeline'
import { Settings } from './pages/Settings'
import { 
  LayoutDashboard, 
  Database, 
  Terminal, 
  Settings as SettingsIcon, 
  Zap, 
  ExternalLink
} from 'lucide-react'

export const App: React.FC = () => {
  const [activeTab, setActiveTab] = useState<'dashboard' | 'cache' | 'logs' | 'settings'>('dashboard')
  const [status, setStatus] = useState<RuntimeStatus | null>(null)
  const [telemetry, setTelemetry] = useState<TelemetrySummary | null>(null)
  const [cacheStats, setCacheStats] = useState<CacheStats | null>(null)
  const [prefetch, setPrefetch] = useState<PrefetchStatus | null>(null)
  const [config, setConfig] = useState<Record<string, any>>({})
  const [logs, setLogs] = useState<LogItem[]>([])
  const [loading, setLoading] = useState<boolean>(false)

  // Initial load
  const loadAll = async () => {
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
      console.error('Error fetching initial state', e)
    }
  }

  useEffect(() => {
    loadAll()

    // Establish SSE stream
    let es: EventSource | null = null
    try {
      es = new EventSource('/api/events')

      es.addEventListener('status', (e) => {
        try {
          const data = JSON.parse(e.data)
          setStatus(data)
        } catch (err) {}
      })

      es.addEventListener('metrics', (e) => {
        try {
          const data = JSON.parse(e.data)
          if (data.telemetry) setTelemetry(data.telemetry)
        } catch (err) {}
      })

      es.addEventListener('log', (e) => {
        try {
          const item: LogItem = JSON.parse(e.data)
          setLogs((prev) => [...prev.slice(-300), item])
        } catch (err) {}
      })
    } catch (e) {
      console.warn('SSE connection failed, falling back to polling', e)
    }

    // Polling interval as safety net
    const interval = setInterval(() => {
      fetchStatus().then(setStatus).catch(() => {})
      fetchTelemetry().then(setTelemetry).catch(() => {})
    }, 4000)

    return () => {
      clearInterval(interval)
      if (es) es.close()
    }
  }, [])

  const handleToggleProxy = async (start: boolean) => {
    setLoading(true)
    try {
      const newStatus = await toggleProxy(start)
      setStatus(newStatus)
    } catch (e: any) {
      alert(`代理操作失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-[#090d16] text-slate-100 flex flex-col font-sans">
      {/* Top Navigation */}
      <header className="bg-[#101726] border-b border-slate-800/80 sticky top-0 z-50 backdrop-blur-md bg-opacity-90">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-tr from-sky-600 to-indigo-500 flex items-center justify-center shadow-lg shadow-sky-500/20">
              <Zap className="w-5 h-5 text-white" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <span className="font-bold text-white tracking-wide text-base">GBF-Accelerator</span>
                <span className="text-[10px] uppercase font-semibold bg-sky-950 text-sky-400 px-1.5 py-0.5 rounded border border-sky-800/60">
                  Control Plane
                </span>
              </div>
              <div className="text-[11px] text-slate-400">高性能本地静态资源缓存与透明代理控制台</div>
            </div>
          </div>

          {/* Navigation Tabs */}
          <nav className="flex items-center gap-1 bg-[#0b0f19] p-1 rounded-xl border border-slate-800/90">
            <button
              onClick={() => setActiveTab('dashboard')}
              className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-medium transition ${
                activeTab === 'dashboard'
                  ? 'bg-sky-600 text-white shadow-md'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/50'
              }`}
            >
              <LayoutDashboard className="w-4 h-4" /> 实时总览
            </button>

            <button
              onClick={() => {
                setActiveTab('cache')
                fetchCacheStats().then(setCacheStats).catch(() => {})
              }}
              className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-medium transition ${
                activeTab === 'cache'
                  ? 'bg-sky-600 text-white shadow-md'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/50'
              }`}
            >
              <Database className="w-4 h-4" /> 缓存管理
            </button>

            <button
              onClick={() => setActiveTab('logs')}
              className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-medium transition ${
                activeTab === 'logs'
                  ? 'bg-sky-600 text-white shadow-md'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/50'
              }`}
            >
              <Terminal className="w-4 h-4" /> 请求监控
            </button>

            <button
              onClick={() => {
                setActiveTab('settings')
                fetchConfig().then(setConfig).catch(() => {})
              }}
              className={`flex items-center gap-2 px-3.5 py-1.5 rounded-lg text-xs font-medium transition ${
                activeTab === 'settings'
                  ? 'bg-sky-600 text-white shadow-md'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-slate-800/50'
              }`}
            >
              <SettingsIcon className="w-4 h-4" /> 系统设置
            </button>
          </nav>

          {/* Quick Info */}
          <div className="hidden lg:flex items-center gap-3 text-xs text-slate-400">
            <span className="flex items-center gap-1.5">
              <span className={`w-2 h-2 rounded-full ${status?.proxy_running ? 'bg-emerald-400 animate-pulse' : 'bg-rose-500'}`} />
              <span className="font-mono text-slate-300">:{status?.listen_port || 8124}</span>
            </span>
          </div>
        </div>
      </header>

      {/* Main Content Area */}
      <main className="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-8">
        {activeTab === 'dashboard' && (
          <Dashboard
            status={status}
            telemetry={telemetry}
            prefetch={prefetch}
            onToggleProxy={handleToggleProxy}
            loading={loading}
          />
        )}

        {activeTab === 'cache' && (
          <CacheManager
            stats={cacheStats}
            onRefresh={() => fetchCacheStats().then(setCacheStats).catch(() => {})}
          />
        )}

        {activeTab === 'logs' && (
          <TraceTimeline
            logs={logs}
            onClearLogs={() => setLogs([])}
          />
        )}

        {activeTab === 'settings' && (
          <Settings
            status={status}
            config={config}
            onConfigUpdated={() => {
              fetchConfig().then(setConfig).catch(() => {})
              fetchStatus().then(setStatus).catch(() => {})
            }}
          />
        )}
      </main>

      {/* Footer */}
      <footer className="border-t border-slate-900 bg-[#090d16] py-4 text-xs text-slate-500 text-center">
        <div className="max-w-7xl mx-auto px-4 flex flex-col sm:flex-row items-center justify-between gap-2">
          <span>GBF-Accelerator 本地透明代理控制平台 &middot; 业务语义透明 &middot; 零篡改防风控</span>
          <div className="flex items-center gap-4">
            <a 
              href="https://game.granbluefantasy.jp" 
              target="_blank" 
              rel="noreferrer"
              className="hover:text-slate-300 flex items-center gap-1"
            >
              打开游戏页 <ExternalLink className="w-3 h-3" />
            </a>
          </div>
        </div>
      </footer>
    </div>
  )
}

export default App
