import React, { useState, useEffect, useRef } from 'react'
import { Zap, ArrowUpCircle, Power, Menu } from 'lucide-react'
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
  onOpenMobileMenu?: () => void
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
  onOpenMobileMenu,
}) => {
  const { t } = useI18n()
  const slotRef = useRef<HTMLDivElement | null>(null)
  const [expandedSlotHeight, setExpandedSlotHeight] = useState<number | undefined>(undefined)
  const [keepSlotLocked, setKeepSlotLocked] = useState(isScrolled)
  const prevScrolledRef = useRef(isScrolled)

  useEffect(() => {
    const wasScrolled = prevScrolledRef.current
    prevScrolledRef.current = isScrolled

    if (isScrolled) {
      setKeepSlotLocked(true)
      return
    }

    if (wasScrolled && !isScrolled) {
      setKeepSlotLocked(true)
      const timer = window.setTimeout(() => {
        setKeepSlotLocked(false)
      }, 220)
      return () => window.clearTimeout(timer)
    }
  }, [isScrolled])

  const isSlotLocked = isScrolled || keepSlotLocked

  useEffect(() => {
    if (isSlotLocked) return
    const el = slotRef.current
    if (!el) return

    const updateHeight = () => {
      const h = el.offsetHeight
      if (h > 0) {
        setExpandedSlotHeight((prev) => (prev === h ? prev : h))
      }
    }

    updateHeight()

    if (typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => {
      updateHeight()
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [isSlotLocked])

  return (
    <div
      ref={slotRef}
      className="sticky top-2.5 sm:top-3 z-20 px-4 sm:px-6 mt-2.5 sm:mt-3 pointer-events-none"
      style={{
        minHeight: isSlotLocked && expandedSlotHeight ? `${expandedSlotHeight}px` : undefined,
        overflowAnchor: 'none',
      }}
    >
      <header
        className={`max-w-5xl mx-auto liquid-glass rounded-[20px] pointer-events-auto transition-[padding] duration-200 ease-fluid ${
          isScrolled ? 'px-3.5 sm:px-4 py-2' : 'px-4 sm:px-5 py-3.5 sm:py-4'
        }`}
      >
        <div className={`flex flex-col transition-[gap] duration-200 ease-fluid ${isScrolled ? 'gap-0' : 'gap-3'}`}>
          {/* Top Bar */}
          <div className="flex items-center justify-between gap-2.5 sm:gap-3">
            {/* Left Identity & Runtime Status */}
            <div className="flex items-center gap-2 sm:gap-3 min-w-0">
              {onOpenMobileMenu && (
                <button
                  type="button"
                  onClick={onOpenMobileMenu}
                  className="md:hidden w-8 h-8 rounded-lg flex items-center justify-center text-label-secondary hover:text-label-primary hover:bg-slate-500/10 active:scale-95 transition-all cursor-pointer shrink-0 ui-control"
                  aria-label={t('sidebar.menu') || '导航菜单'}
                  title={t('sidebar.menu') || '导航菜单'}
                >
                  <Menu className="w-4 h-4" />
                </button>
              )}

              <div
                className={`rounded-xl flex items-center justify-center shrink-0 transition-all duration-200 ${
                  isScrolled ? 'w-7 h-7 sm:w-8 sm:h-8 rounded-[10px]' : 'w-9 h-9 sm:w-10 sm:h-10'
                } ${
                  isRunning
                    ? 'bg-emerald-600 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.28),0_4px_14px_-4px_rgba(16,185,129,0.55)]'
                    : 'bg-slate-700 text-slate-300 shadow-[inset_0_1px_0_rgba(255,255,255,0.12)]'
                }`}
              >
                <Zap className={`${isScrolled ? 'w-3.5 h-3.5 sm:w-4 sm:h-4' : 'w-4 h-4 sm:w-5 sm:h-5'} fill-current transition-all duration-200`} />
              </div>

              <div className="flex flex-col min-w-0">
                <div className="flex items-center gap-1.5 sm:gap-2 flex-wrap">
                  <span
                    className={`font-bold text-label-primary tracking-tight leading-none transition-all duration-200 whitespace-nowrap ${
                      isScrolled ? 'text-xs sm:text-[15px]' : 'text-sm sm:text-lg'
                    }`}
                  >
                    {t('header.title')}
                  </span>

                  {!isScrolled && (
                    <span className="text-[10px] sm:text-[11px] font-mono font-semibold px-1.5 sm:px-2 py-0.5 rounded-full inset-panel text-label-secondary leading-tight whitespace-nowrap">
                      v{status?.version || '2.6.0'}
                    </span>
                  )}

                  {!isScrolled && updateInfo?.available && (
                    <button
                      type="button"
                      onClick={onOpenUpdate}
                      className="px-2 py-0.5 rounded-full bg-sys-amberBg hover:opacity-90 text-sys-amber border border-amber-500/30 text-[10px] sm:text-[11px] font-semibold inline-flex items-center gap-1 cursor-pointer active:scale-[0.98] transition-all whitespace-nowrap"
                    >
                      <ArrowUpCircle className="w-3 h-3 sm:w-3.5 sm:h-3.5 shrink-0" />
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

                <div
                  className={`grid transition-[grid-template-rows,opacity,margin] duration-200 ease-fluid ${
                    isScrolled
                      ? 'grid-rows-[0fr] opacity-0 mt-0 pointer-events-none'
                      : 'grid-rows-[1fr] opacity-100 mt-1 sm:mt-1.5'
                  }`}
                  aria-hidden={isScrolled}
                >
                  <div className="overflow-hidden min-h-0">
                    <div className="flex items-center gap-1.5 sm:gap-2 flex-wrap">
                      <span className="inline-flex items-center gap-1.5 text-[11px] sm:text-xs whitespace-nowrap">
                        <span className={`w-2 h-2 rounded-full ${isRunning ? 'bg-emerald-500 animate-pulse' : 'bg-slate-400'}`} />
                        <span className={isRunning ? 'font-semibold text-sys-green' : 'text-label-tertiary font-medium'}>
                          {isRunning ? t('header.runningWithPort', { port: currentListenPort }) : t('header.stopped')}
                        </span>
                      </span>
                      {isRunning && (
                        <>
                          <span className="text-label-tertiary opacity-50 hidden sm:inline">·</span>
                          <span className="inline-flex items-center gap-1 text-[11px] sm:text-xs text-label-tertiary font-mono tnum whitespace-nowrap">
                            <span className="hidden sm:inline">{t('header.uptime')}</span>
                            <span className="font-semibold text-label-secondary">
                              <RuntimeUptime baseSeconds={uptimeSec} running={isRunning} />
                            </span>
                          </span>
                        </>
                      )}
                    </div>
                  </div>
                </div>
              </div>
            </div>

            {/* Compact Middle Metrics when scrolled */}
            {isScrolled && (
              <div className="hidden lg:flex items-center justify-center flex-1 min-w-0 overflow-hidden">
                <RealtimeRequestHud status={status} running={isRunning} compact={true} />
              </div>
            )}

            {/* Right Master Start / Stop & Quit */}
            <div className="flex items-center gap-1.5 sm:gap-2 shrink-0">
              <button
                type="button"
                disabled={loadingProxy || isTerminated}
                onClick={onToggleProxy}
                className={`font-semibold text-white transition-all cursor-pointer select-none active:scale-[0.98] whitespace-nowrap disabled:opacity-50 ${
                  isScrolled
                    ? 'px-2.5 sm:px-3 py-1.5 rounded-lg text-xs'
                    : 'min-w-[76px] sm:min-w-[100px] px-2.5 sm:px-4 py-1.5 sm:py-2 rounded-xl text-xs sm:text-[13px]'
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
                  isScrolled ? 'p-1.5 rounded-lg' : 'p-2 sm:px-3 sm:py-2 rounded-xl text-xs sm:text-[13px] font-medium'
                }`}
                title={t('header.quitAppTip')}
                aria-label={t('header.quitApp')}
              >
                <Power className="w-4 h-4 text-rose-500" />
                {!isScrolled && <span className="hidden sm:inline">{t('header.quitApp')}</span>}
              </button>
            </div>
          </div>

          {/* Expanded Realtime Request Telemetry Strip */}
          <div
            className={`grid transition-[grid-template-rows,opacity] duration-200 ease-fluid ${
              isScrolled
                ? 'grid-rows-[0fr] opacity-0 pointer-events-none'
                : 'grid-rows-[1fr] opacity-100'
            }`}
            aria-hidden={isScrolled}
          >
            <div className="overflow-hidden min-h-0">
              <RealtimeRequestHud status={status} running={isRunning} />
            </div>
          </div>
        </div>
      </header>
    </div>
  )
}
