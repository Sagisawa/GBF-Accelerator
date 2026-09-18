import React from 'react'
import { RuntimeStatus, TelemetrySummary } from '../../types'
import { Play, Square, Loader2, Smartphone, Terminal, Trash2, Keyboard } from 'lucide-react'

export interface HeaderBarProps {
  status: RuntimeStatus | null
  telemetry?: TelemetrySummary | null
  config?: Record<string, any>
  loading: boolean
  onToggleProxy: () => void
  onOpenMobileGuide?: () => void
  onToggleLogs?: () => void
  onOpenClearModal?: () => void
  onOpenShortcuts?: () => void
  logCount?: number
}

export const HeaderBar: React.FC<HeaderBarProps> = ({
  status,
  telemetry,
  config = {},
  loading,
  onToggleProxy,
  onOpenMobileGuide,
  onToggleLogs,
  onOpenClearModal,
  onOpenShortcuts,
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

  return (
    <header className="sticky top-0 z-40 backdrop-blur-2xl bg-black/80 border-b border-white/[0.08] transition-all">
      <div className="max-w-5xl mx-auto px-4 sm:px-6 h-20 flex items-center justify-between gap-4">
        {/* Left: Apple Transport Control (Play/Stop) & Brand */}
        <div className="flex items-center gap-3 shrink-0 select-none">
          {/* Circular Transport Button */}
          <button
            type="button"
            disabled={loading}
            onClick={onToggleProxy}
            className={`w-11 h-11 rounded-full flex items-center justify-center transition-all duration-200 select-none ${
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

          {/* Brand Identity */}
          <div className="flex flex-col">
            <div className="flex items-center gap-2">
              <span className="font-semibold text-white tracking-tight text-sm">
                GBF-Accelerator
              </span>
              <span className="text-[10px] font-mono px-1.5 py-0.5 rounded-full bg-white/[0.08] text-label-secondary border border-white/[0.06]">
                v{status?.version || '2.0.0'}
              </span>
            </div>
            <div className="flex items-center gap-1.5 text-xs mt-0.5">
              {isRunning ? (
                <span className="inline-flex items-center gap-1.5 text-apple-green font-medium text-[11px]">
                  <span className="w-1.5 h-1.5 rounded-full bg-apple-green animate-pulse" />
                  运行中 :{port}
                </span>
              ) : (
                <span className="inline-flex items-center gap-1.5 text-label-secondary text-[11px]">
                  <span className="w-1.5 h-1.5 rounded-full bg-label-tertiary" />
                  已就绪 · 空格启动
                </span>
              )}
            </div>
          </div>
        </div>

        {/* Center: Apple Music "Now Playing" LCD & Scrubber Bar */}
        <div className="hidden md:flex flex-col justify-center px-4 py-1.5 rounded-2xl bg-[#1c1c1e]/70 border border-white/[0.06] w-full max-w-sm lg:max-w-md shadow-inner">
          {/* Top row: Status info & Equalizer wave */}
          <div className="flex items-center justify-between text-xs">
            <div className="flex items-center gap-2 truncate">
              {isRunning && (
                <div className="flex items-end gap-0.5 h-3 shrink-0" title="正在流式代理传输">
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-1" />
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-2" />
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-3" />
                  <span className="w-0.5 bg-apple-red rounded-full animate-eq-4" />
                </div>
              )}
              <span className="font-semibold text-white truncate text-[12px]">
                {isRunning ? `127.0.0.1:${port}` : 'GBF-Accelerator'}
              </span>
              <span className="text-label-tertiary">·</span>
              <span className="text-label-secondary text-[11px] truncate">
                {isRunning
                  ? (isDirect ? '日本官方CDN直连' : (upstream === 'auto' ? '自动分流' : upstream))
                  : '未启动加速服务'}
              </span>
            </div>
            <span className="text-[11px] font-mono text-label-secondary shrink-0">
              {isRunning ? (uptimeMinutes > 0 ? `${uptimeMinutes}m` : '刚刚') : '就绪'}
            </span>
          </div>

          {/* Bottom row: Scrubber progress bar */}
          <div className="flex items-center gap-2 mt-1">
            <span className="text-[10px] font-mono text-label-secondary tnum shrink-0">
              {isRunning ? `${hitRatio}% 命中` : '0:00'}
            </span>
            <div className="flex-1 h-1 rounded-full bg-white/10 overflow-hidden relative">
              <div
                className={`h-full rounded-full transition-all duration-300 ${
                  isRunning ? 'bg-apple-red' : 'bg-white/20'
                }`}
                style={{ width: isRunning ? `${Math.max(12, parseFloat(hitRatio) || 20)}%` : '0%' }}
              />
            </div>
            <span className="text-[10px] font-mono text-label-secondary tnum shrink-0">
              {isRunning ? (p50 > 0 ? `${p50}ms` : `${totalRequests} Req`) : '--'}
            </span>
          </div>
        </div>

        {/* Right: Apple Music Quick Toolbar */}
        <div className="flex items-center gap-1 sm:gap-2">
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
