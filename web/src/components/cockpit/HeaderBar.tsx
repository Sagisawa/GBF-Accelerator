import React from 'react'
import { RuntimeStatus, TelemetrySummary } from '../../types'
import {
  Play,
  Square,
  Loader2,
  Smartphone,
  Terminal,
  Trash2,
  Keyboard,
  RotateCcw,
  Zap,
} from 'lucide-react'

export interface HeaderBarProps {
  status: RuntimeStatus | null
  telemetry?: TelemetrySummary | null
  config?: Record<string, any>
  loading: boolean
  onToggleProxy: () => void
  onToggleDirect?: () => void
  onOpenMobileGuide?: () => void
  onToggleLogs?: () => void
  onOpenClearModal?: () => void
  onOpenShortcuts?: () => void
  onRefresh?: () => void
  logCount?: number
}

export const HeaderBar: React.FC<HeaderBarProps> = ({
  status,
  telemetry,
  config = {},
  loading,
  onToggleProxy,
  onToggleDirect,
  onOpenMobileGuide,
  onToggleLogs,
  onOpenClearModal,
  onOpenShortcuts,
  onRefresh,
  logCount = 0,
}) => {
  const isRunning = Boolean(status?.proxy_running)
  const port = status?.listen_port || 8124
  const uptimeMinutes = status ? Math.floor(status.uptime_seconds / 60) : 0
  const isDirect = Boolean(config.direct_mode ?? status?.direct_mode)
  const upstream = config.upstream_proxy || status?.upstream_proxy || 'auto'

  const req = status?.requests
  const totalHits = req?.total_hits || 0
  const misses = req?.cache_misses || 0
  const totalLookups = totalHits + misses
  const hitRatio = totalLookups > 0 ? ((totalHits / totalLookups) * 100).toFixed(1) : '0.0'
  const p50 = telemetry?.percentiles?.p50_ms ?? 0
  const totalRequests = (req?.total_apis || 0) + (req?.total_assets || 0)

  // Actual numerical progress for scrubber
  const progressPercent = isRunning
    ? (totalLookups > 0 ? Math.min(100, Math.max(2, parseFloat(hitRatio))) : 5)
    : 0

  return (
    <header className="sticky top-0 z-20 backdrop-blur-2xl bg-[#0a0a0c]/85 border-b border-white/[0.08] transition-all">
      <div className="w-full px-4 sm:px-6 h-16 sm:h-20 flex items-center justify-between gap-3 sm:gap-6">
        {/* Left: Apple Music Transport Control Cluster & Artwork */}
        <div className="flex items-center gap-3 shrink-0 select-none">
          {/* Secondary Skip Backward (Refresh) */}
          <button
            type="button"
            onClick={onRefresh}
            className="w-8 h-8 rounded-full hidden sm:flex items-center justify-center text-label-secondary hover:text-white hover:bg-white/[0.08] active:scale-95 transition-all"
            title="刷新状态数据"
          >
            <RotateCcw className="w-3.5 h-3.5" />
          </button>

          {/* Main Circular Play/Stop Transport Button */}
          <button
            type="button"
            disabled={loading}
            onClick={onToggleProxy}
            className={`w-10 h-10 sm:w-11 sm:h-11 rounded-full flex items-center justify-center transition-all duration-200 select-none shrink-0 ${
              isRunning
                ? 'bg-apple-red text-white hover:bg-apple-redHover shadow-apple active:scale-95'
                : 'bg-white text-black hover:bg-white/90 hover:scale-105 active:scale-95 shadow-sm'
            }`}
            title={isRunning ? '停止加速服务 (Space)' : '启动加速服务 (Space)'}
          >
            {loading ? (
              <Loader2 className="w-5 h-5 animate-spin" />
            ) : isRunning ? (
              <Square className="w-4 h-4 fill-current" />
            ) : (
              <Play className="w-4 h-4 fill-current translate-x-0.5" />
            )}
          </button>

          {/* Secondary Skip Forward (Toggle Direct Mode) */}
          {onToggleDirect && (
            <button
              type="button"
              onClick={onToggleDirect}
              className={`w-8 h-8 rounded-full hidden sm:flex items-center justify-center transition-all active:scale-95 ${
                isDirect
                  ? 'text-apple-green bg-apple-green/15'
                  : 'text-label-secondary hover:text-white hover:bg-white/[0.08]'
              }`}
              title={isDirect ? '当前: 日本官方直连 (点击切回代理)' : '当前: 上游代理分流 (点击切换直连)'}
            >
              <Zap className="w-3.5 h-3.5 fill-current" />
            </button>
          )}

          {/* Apple Music Style Album Artwork & Track Info */}
          <div className="flex items-center gap-2.5 sm:gap-3 pl-1">
            {/* Square Album Cover */}
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-[#2a2a2e] to-[#161618] border border-white/[0.1] shadow-apple flex items-center justify-center shrink-0 relative overflow-hidden group">
              <div
                className={`w-full h-full absolute inset-0 bg-gradient-to-tr transition-opacity duration-300 ${
                  isRunning
                    ? 'from-apple-red/40 via-purple-600/20 to-apple-blue/30 opacity-100'
                    : 'from-white/5 to-transparent opacity-40'
                }`}
              />
              <span className="font-mono font-bold text-xs text-white z-10 select-none">
                GBF
              </span>
              {isRunning && (
                <div className="absolute bottom-1 right-1 w-1.5 h-1.5 rounded-full bg-apple-green animate-pulse z-10" />
              )}
            </div>

            {/* Track Info (Proxy State) */}
            <div className="flex flex-col min-w-0">
              <div className="flex items-center gap-2">
                <span className="font-semibold text-white tracking-tight text-xs sm:text-sm truncate">
                  {isRunning ? `127.0.0.1:${port}` : 'GBF-Accelerator'}
                </span>
                <span className="hidden md:inline-flex text-[10px] font-mono px-1.5 py-0.2 rounded-full bg-white/[0.08] text-label-secondary border border-white/[0.06]">
                  v{status?.version || '2.0'}
                </span>
              </div>
              <div className="flex items-center gap-1.5 text-xs text-label-secondary mt-0.5 truncate">
                {isRunning ? (
                  <span className="inline-flex items-center gap-1.5 text-apple-green font-medium text-[11px] truncate">
                    <span className="w-1.5 h-1.5 rounded-full bg-apple-green shrink-0 animate-pulse" />
                    {isDirect ? '日本官方CDN直连' : (upstream === 'auto' ? '自动代理分流' : upstream)}
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 text-label-secondary text-[11px]">
                    <span className="w-1.5 h-1.5 rounded-full bg-label-tertiary shrink-0" />
                    加速服务已就绪 · 空格启动
                  </span>
                )}
              </div>
            </div>
          </div>
        </div>

        {/* Center: Apple Music "Now Playing" LCD & Scrubber Bar */}
        <div className="hidden md:flex flex-col justify-center px-4 py-2 rounded-2xl bg-[#1c1c1e]/80 border border-white/[0.08] w-full max-w-sm lg:max-w-md shadow-inner backdrop-blur-md">
          {/* Top row: Status info & Equalizer wave */}
          <div className="flex items-center justify-between text-xs mb-1">
            <div className="flex items-center gap-2 truncate">
              {isRunning ? (
                <div className="flex items-end gap-0.5 h-3 shrink-0" title="正在加速传输中">
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-1" />
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-2" />
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-3" />
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-4" />
                </div>
              ) : (
                <div className="w-2 h-2 rounded-full bg-label-tertiary shrink-0" />
              )}
              <span className="font-semibold text-white truncate text-[12px]">
                {isRunning ? (isDirect ? '直连日本官方' : '智能上游链路') : '未启动加速'}
              </span>
              <span className="text-label-tertiary">·</span>
              <span className="text-label-secondary text-[11px] truncate font-mono">
                {isRunning ? `${totalRequests.toLocaleString()} 请求` : '待机'}
              </span>
            </div>
            <span className="text-[11px] font-mono text-label-secondary shrink-0 ml-2">
              {isRunning ? (uptimeMinutes > 0 ? `${uptimeMinutes}m 运行` : '刚刚启动') : '就绪'}
            </span>
          </div>

          {/* Bottom row: Scrubber progress bar */}
          <div className="flex items-center gap-2.5">
            <span className="text-[10px] font-mono text-label-secondary tnum shrink-0 w-12 text-left">
              {isRunning ? `${hitRatio}%` : '0:00'}
            </span>
            <div className="flex-1 h-1 rounded-full bg-white/10 overflow-hidden relative">
              <div
                className={`h-full rounded-full transition-all duration-300 ${
                  isRunning ? 'bg-apple-red' : 'bg-white/20'
                }`}
                style={{ width: `${progressPercent}%` }}
              />
            </div>
            <span className="text-[10px] font-mono text-label-secondary tnum shrink-0 w-14 text-right">
              {isRunning ? (p50 > 0 ? `${p50}ms` : '极速') : '--'}
            </span>
          </div>
        </div>

        {/* Right: Apple Music Quick Toolbar */}
        <div className="flex items-center gap-1 sm:gap-2 shrink-0">
          {onOpenMobileGuide && (
            <button
              type="button"
              onClick={onOpenMobileGuide}
              className="p-2 rounded-full text-label-secondary hover:text-white hover:bg-white/[0.08] transition-colors relative"
              title="移动设备 Wi-Fi 连接指引"
            >
              <Smartphone className="w-4 h-4" />
              {Boolean(status?.allow_lan) && (
                <span className="absolute top-1.5 right-1.5 w-1.5 h-1.5 rounded-full bg-apple-green" />
              )}
            </button>
          )}

          {onToggleLogs && (
            <button
              type="button"
              onClick={onToggleLogs}
              className="p-2 rounded-full text-label-secondary hover:text-white hover:bg-white/[0.08] transition-colors relative"
              title="实时日志终端 (L)"
            >
              <Terminal className="w-4 h-4" />
              {logCount > 0 && (
                <span className="absolute top-1 right-1 px-1 min-w-[14px] h-3.5 rounded-full bg-apple-red text-white text-[9px] font-mono font-bold flex items-center justify-center leading-none">
                  {logCount > 99 ? '99+' : logCount}
                </span>
              )}
            </button>
          )}

          {onOpenClearModal && (
            <button
              type="button"
              onClick={onOpenClearModal}
              className="p-2 rounded-full text-label-secondary hover:text-apple-red hover:bg-white/[0.08] transition-colors"
              title="清空缓存 (C)"
            >
              <Trash2 className="w-4 h-4" />
            </button>
          )}

          {onOpenShortcuts && (
            <button
              type="button"
              onClick={onOpenShortcuts}
              className="p-2 rounded-full text-label-secondary hover:text-white hover:bg-white/[0.08] transition-colors"
              title="键盘快捷键 (?)"
            >
              <Keyboard className="w-4 h-4" />
            </button>
          )}
        </div>
      </div>
    </header>
  )
}
