import React from 'react'
import {
  Home,
  Smartphone,
  Terminal,
  Activity,
  Wifi,
  FolderOpen,
  Trash2,
  Compass,
  RefreshCw,
  HelpCircle,
  Github,
} from 'lucide-react'
import { useI18n } from '../../i18n'

export interface SidebarProps {
  activeTab: 'core' | 'android'
  onTabChange: (tab: 'core' | 'android') => void
  showAndroidPatch: boolean
  isRunning: boolean
  hasUpdate: boolean
  loadingOpenFolder: boolean
  onOpenLogs: () => void
  onOpenHealth: () => void
  onOpenLatency: () => void
  onOpenFolder: () => void
  onOpenClearCache: () => void
  onOpenRoutingGuide: () => void
  onCheckUpdate: () => void
  onOpenShortcuts: () => void
}

export const Sidebar: React.FC<SidebarProps> = ({
  activeTab,
  onTabChange,
  showAndroidPatch,
  hasUpdate,
  loadingOpenFolder,
  onOpenLogs,
  onOpenHealth,
  onOpenLatency,
  onOpenFolder,
  onOpenClearCache,
  onOpenRoutingGuide,
  onCheckUpdate,
  onOpenShortcuts,
}) => {
  const { t } = useI18n()

  return (
    <aside
      className="w-52 bg-white border-r border-slate-200/90 flex flex-col justify-between h-screen sticky top-0 py-3.5 select-none shrink-0 z-30 transition-all"
      aria-label="Navigation Sidebar"
    >
      {/* Top Group: Tabs & Functional Actions */}
      <div className="flex flex-col gap-2 w-full px-3 overflow-y-auto min-h-0">
        {/* Tabs Container with Smooth Sliding Indicator */}
        <div className="relative flex flex-col gap-2 w-full">
          {showAndroidPatch && (
            <div
              className={`absolute left-0 right-0 top-0 h-11 rounded-xl transition-all duration-[220ms] ease-[cubic-bezier(.32,.72,0,1)] pointer-events-none z-0 ${
                activeTab === 'core'
                  ? 'translate-y-0 bg-sky-100/90 shadow-2xs'
                  : 'translate-y-[52px] bg-indigo-100/90 shadow-2xs'
              }`}
              aria-hidden="true"
            />
          )}
          {!showAndroidPatch && (
            <div
              className="absolute left-0 right-0 top-0 h-11 rounded-xl bg-sky-100/90 shadow-2xs pointer-events-none z-0"
              aria-hidden="true"
            />
          )}

          {/* Tab 1: Core Dashboard (控制首页) */}
          <button
            type="button"
            onClick={() => onTabChange('core')}
            className={`relative z-10 flex items-center justify-center gap-3 px-3 h-11 rounded-xl text-base font-semibold transition-colors duration-150 cursor-pointer w-full ${
              activeTab === 'core'
                ? 'text-sky-800 font-bold'
                : 'text-slate-700 hover:text-slate-900 hover:bg-slate-100/50'
            }`}
            title={t('tabs.core')}
            aria-label={t('sidebar.overview') || t('tabs.core')}
          >
            <Home className="w-5 h-5 shrink-0" strokeWidth={1.85} />
            <span className="whitespace-nowrap leading-tight">{t('sidebar.overview')}</span>
          </button>

          {/* Tab 2: Android Companion (安卓修补) */}
          {showAndroidPatch && (
            <button
              type="button"
              onClick={() => onTabChange('android')}
              className={`relative z-10 flex items-center justify-center gap-3 px-3 h-11 rounded-xl text-base font-semibold transition-colors duration-150 cursor-pointer w-full ${
                activeTab === 'android'
                  ? 'text-indigo-800 font-bold'
                  : 'text-slate-700 hover:text-slate-900 hover:bg-slate-100/50'
              }`}
              title={t('tabs.android')}
              aria-label={t('sidebar.android') || t('tabs.android')}
            >
              <Smartphone className="w-5 h-5 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight">{t('sidebar.android')}</span>
            </button>
          )}
        </div>

        <div className="w-full my-1">
          <div className="h-px bg-slate-200/80 mx-1" />
        </div>

        {/* Core Tool Triggers with bold, readable text labels */}
        <button
          type="button"
          onClick={onOpenLogs}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={`${t('footer.liveLogs')} (L)`}
          aria-label={t('sidebar.liveLogs') || t('footer.liveLogs')}
        >
          <Terminal className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.liveLogs')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenHealth}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.diagnostics')}
          aria-label={t('sidebar.diagnostics') || t('footer.diagnostics')}
        >
          <Activity className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.diagnostics')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenLatency}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.latencyTest')}
          aria-label={t('sidebar.latencyTest') || t('footer.latencyTest')}
        >
          <Wifi className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.latencyTest')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenFolder}
          disabled={loadingOpenFolder}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full disabled:opacity-50"
          title={loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}
          aria-label={t('sidebar.openFolder') || t('footer.openFolder')}
        >
          <FolderOpen className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.openFolder')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenClearCache}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-rose-600 hover:bg-rose-50 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.clearCache')}
          aria-label={t('sidebar.clearCache') || t('footer.clearCache')}
        >
          <Trash2 className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.clearCache')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenRoutingGuide}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.routingTips')}
          aria-label={t('sidebar.routingTips') || t('footer.routingTips')}
        >
          <Compass className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.routingTips')}</span>
        </button>
      </div>

      {/* Bottom Group: Always pinned to bottom, independent of right content scroll */}
      <div className="flex flex-col gap-2 w-full px-3 pt-3 shrink-0 border-t border-slate-200/80">
        <button
          type="button"
          onClick={onCheckUpdate}
          className="relative flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.checkUpdate')}
          aria-label={t('sidebar.checkUpdate') || t('footer.checkUpdate')}
        >
          <div className="relative shrink-0">
            <RefreshCw className="w-5 h-5" strokeWidth={1.85} />
            {hasUpdate && (
              <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white animate-pulse" />
            )}
          </div>
          <span className="whitespace-nowrap leading-tight">{t('sidebar.checkUpdate')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenShortcuts}
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={`${t('header.shortcuts')} (?)`}
          aria-label={t('sidebar.shortcuts') || t('header.shortcuts')}
        >
          <HelpCircle className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.shortcuts')}</span>
        </button>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className="flex items-center justify-center gap-3 px-3 py-2.5 rounded-xl text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title="GitHub Repository"
          aria-label="GitHub Repository"
        >
          <Github className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="whitespace-nowrap leading-tight">{t('sidebar.github')}</span>
        </a>
      </div>
    </aside>
  )
}
