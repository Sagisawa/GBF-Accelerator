import React, { useState, useEffect } from 'react'
import { Zap, ArrowUpCircle, Power } from 'lucide-react'
import { RuntimeStatus } from '../../types'
import { useI18n } from '../../i18n'
import { RealtimeRequestHud } from './RealtimeRequestHud'

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

export interface HeaderCockpitProps {
  status: RuntimeStatus | null
  isRunning: boolean
  isScrolled: boolean
  isTerminated: boolean
  loadingProxy: boolean
  currentListenPort: number | string
  uptimeSec: number
  updateInfo: { available: boolean; version: string } | null
  onToggleProxy: () => void
  onOpenUpdate: () => void
  onOpenQuit: () => void
}

/**
 * Floating Liquid Glass Header Cockpit:
 * Positioned sticky inside the scroll container so scrolled content refracts beneath it.
 */
export const HeaderCockpit: React.FC<HeaderCockpitProps> = ({
  status,
  isRunning,
  isScrolled,
  isTerminated,
  loadingProxy,
  currentListenPort,
  uptimeSec,
  updateInfo,
  onToggleProxy,
  onOpenUpdate,
  onOpenQuit,
}) => {
  const { t } = useI18n()

  return (
    <div className="sticky top-2.5 sm:top-3 z-20 px-4 sm:px-6 pt-2.5 sm:pt-3 pointer-events-none">
      <header
        className={`max-w-5xl mx-auto liquid-glass rounded-[20px] pointer-events-auto transition-[padding] duration-200 ease-fluid ${
          isScrolled ? 'px-3.5 sm:px-4 py-2' : 'px-4 sm:px-5 py-3.5 sm:py-4'
        }`}
      >
        <div className={`flex flex-col transition-[gap] duration-200 ease-fluid ${isScrolled ? 'gap-0' : 'gap-3'}`}>
          {/* Top Bar */}
          <div className="flex items-center justify-between gap-3">
            {/* Left Identity & Runtime Status */}
            <div className="flex items-center gap-2.5 sm:gap-3 min-w-0">
              <div
                className={`rounded-xl flex items-center justify-center shrink-0 transition-all duration-200 ${
                  isScrolled ? 'w-8 h-8 rounded-[10px]' : 'w-10 h-10'
                } ${
                  isRunning
                    ? 'bg-emerald-600 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.28),0_4px_14px_-4px_rgba(16,185,129,0.55)]'
                    : 'bg-slate-700 text-slate-300 shadow-[inset_0_1px_0_rgba(255,255,255,0.12)]'
                }`}
              >
                <Zap className={`${isScrolled ? 'w-4 h-4' : 'w-5 h-5'} fill-current transition-all duration-200`} />
              </div>

              <div className="flex flex-col min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <span
                    className={`font-bold text-label-primary tracking-tight leading-none transition-all duration-200 ${
                      isScrolled ? 'text-sm sm:text-[15px]' : 'text-base sm:text-lg'
                    }`}
                  >
                    {t('header.title')}
                  </span>

                  {!isScrolled && (
                    <span className="text-[11px] font-mono font-semibold px-2 py-0.5 rounded-full inset-panel text-label-secondary leading-tight">
                      v{status?.version || '2.5.1'}
                    </span>
                  )}

                  {!isScrolled && updateInfo?.available && (
                    <button
                      type="button"
                      onClick={onOpenUpdate}
                      className="px-2.5 py-0.5 rounded-full bg-sys-amberBg hover:opacity-90 text-sys-amber border border-amber-500/30 text-[11px] font-semibold inline-flex items-center gap-1 cursor-pointer active:scale-[0.98] transition-all"
                    >
                      <ArrowUpCircle className="w-3.5 h-3.5 shrink-0" />
                      <span>{t('header.updateFound', { version: updateInfo.version })}</span>
                    </button>
                  )}

                  {isScrolled && (
                    <span className="inline-flex items-center gap-1.5 text-xs whitespace-nowrap">
                      <span className={`w-2 h-2 rounded-full ${isRunning ? 'bg-emerald-500' : 'bg-slate-400'}`} />
                      <span className={isRunning ? 'font-mono font-semibold text-sys-green' : 'text-label-tertiary font-medium'}>
                        {isRunning ? `${currentListenPort}` : t('header.stopped')}
                      </span>
                    </span>
                  )}
                </div>

                {!isScrolled && (
                  <div className="flex items-center gap-2 mt-1.5 flex-wrap">
                    <span className="inline-flex items-center gap-1.5 text-xs">
                      <span className={`w-2 h-2 rounded-full ${isRunning ? 'bg-emerald-500 animate-pulse' : 'bg-slate-400'}`} />
                      <span className={isRunning ? 'font-semibold text-sys-green' : 'text-label-tertiary font-medium'}>
                        {isRunning ? t('header.runningWithPort', { port: currentListenPort }) : t('header.stopped')}
                      </span>
                    </span>
                    {isRunning && (
                      <>
                        <span className="text-label-tertiary opacity-50">·</span>
                        <span className="inline-flex items-center gap-1 text-xs text-label-tertiary font-mono tnum">
                          <span>{t('header.uptime')}</span>
                          <span className="font-semibold text-label-secondary">
                            <RuntimeUptime baseSeconds={uptimeSec} running={isRunning} />
                          </span>
                        </span>
                      </>
                    )}
                  </div>
                )}
              </div>
            </div>

            {/* Compact Middle Metrics when scrolled */}
            {isScrolled && (
              <div className="hidden md:flex items-center justify-center flex-1 min-w-0 overflow-hidden">
                <RealtimeRequestHud status={status} running={isRunning} compact={true} />
              </div>
            )}

            {/* Right Master Start / Stop & Quit */}
            <div className="flex items-center gap-2 shrink-0">
              <button
                type="button"
                disabled={loadingProxy || isTerminated}
                onClick={onToggleProxy}
                className={`font-semibold text-white transition-all cursor-pointer select-none active:scale-[0.98] whitespace-nowrap disabled:opacity-50 ${
                  isScrolled
                    ? 'px-3 py-1.5 rounded-lg text-xs'
                    : 'min-w-[88px] sm:min-w-[100px] px-3.5 sm:px-4 py-2 rounded-xl text-xs sm:text-[13px]'
                } ${
                  isRunning
                    ? 'bg-rose-600 hover:bg-rose-700 active:bg-rose-800 shadow-[inset_0_1px_0_rgba(255,255,255,0.2),0_4px_12px_-4px_rgba(225,29,72,0.45)]'
                    : 'bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 shadow-[inset_0_1px_0_rgba(255,255,255,0.2),0_4px_12px_-4px_rgba(5,150,105,0.45)]'
                }`}
              >
                {loadingProxy ? t('header.processing') : isRunning ? t('header.stopProxy') : t('header.startProxy')}
              </button>
              <button
                type="button"
                disabled={isTerminated}
                onClick={onOpenQuit}
                className={`ui-control hover:!text-rose-600 dark:hover:!text-rose-400 hover:!border-rose-500/30 transition-all cursor-pointer select-none active:scale-[0.98] flex items-center justify-center gap-1.5 shrink-0 whitespace-nowrap disabled:opacity-50 ${
                  isScrolled ? 'p-1.5 rounded-lg' : 'px-3 py-2 rounded-xl text-xs sm:text-[13px] font-medium'
                }`}
                title={t('header.quitAppTip')}
                aria-label={t('header.quitApp')}
              >
                <Power className="w-4 h-4 text-rose-500" />
                {!isScrolled && <span className="hidden min-[400px]:inline">{t('header.quitApp')}</span>}
              </button>
            </div>
          </div>

          {/* Expanded Realtime Request Telemetry Strip */}
          {!isScrolled && (
            <RealtimeRequestHud status={status} running={isRunning} />
          )}
        </div>
      </header>
    </div>
  )
}
