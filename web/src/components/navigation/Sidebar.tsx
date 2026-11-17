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
  Zap,
} from 'lucide-react'
import { useI18n } from '../../i18n'

export interface SidebarProps {
  activeTab: 'core' | 'android'
  onTabChange: (tab: 'core' | 'android') => void
  showAndroidPatch: boolean
  isRunning: boolean
  hasUpdate: boolean
  loadingOpenFolder?: boolean
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
  isRunning,
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
      className="w-14 sm:w-36 md:w-40 bg-white border-r border-slate-200/90 flex flex-col justify-between py-3 select-none shrink-0 z-30 transition-all overflow-y-auto"
      aria-label="Navigation Sidebar"
    >
      {/* Top Group: Logo & Tab Navigation */}
      <div className="flex flex-col gap-1 w-full px-2">
        {/* Brand Header */}
        <div
          className="flex items-center gap-2.5 px-1 sm:px-2 py-1.5 mb-1 text-slate-800"
          title={`GBF-Accelerator (${isRunning ? t('header.running') : t('header.stopped')})`}
        >
          <div
            className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 transition-colors shadow-2xs ${
              isRunning
                ? 'bg-emerald-600 text-white shadow-emerald-500/20'
                : 'bg-slate-700 text-slate-300'
            }`}
          >
            <Zap className="w-4.5 h-4.5 fill-current" />
          </div>
          <div className="hidden sm:flex flex-col min-w-0">
            <span className="font-extrabold text-xs text-slate-900 tracking-tight leading-none truncate">
              GBF-Acc
            </span>
            <span className="text-[10px] text-slate-400 font-mono mt-0.5">
              {isRunning ? t('header.running') : t('header.stopped')}
            </span>
          </div>
        </div>

        {/* Tab 1: Core Dashboard (首页) */}
        <button
          type="button"
          onClick={() => onTabChange('core')}
          className={`flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] transition-all cursor-pointer w-full text-left ${
            activeTab === 'core'
              ? 'bg-sky-100/90 text-sky-800 font-semibold shadow-2xs'
              : 'text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium'
          }`}
          title={t('tabs.core')}
          aria-label={t('sidebar.overview') || t('tabs.core')}
        >
          <Home className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.overview')}</span>
        </button>

        {/* Tab 2: Android Companion (Conditionally displayed) */}
        {showAndroidPatch && (
          <button
            type="button"
            onClick={() => onTabChange('android')}
            className={`flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] transition-all cursor-pointer w-full text-left ${
              activeTab === 'android'
                ? 'bg-indigo-100/90 text-indigo-800 font-semibold shadow-2xs'
                : 'text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium'
            }`}
            title={t('tabs.android')}
            aria-label={t('sidebar.android') || t('tabs.android')}
          >
            <Smartphone className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
            <span className="truncate hidden sm:inline">{t('sidebar.android')}</span>
          </button>
        )}

        <div className="w-full my-1">
          <div className="h-px bg-slate-200/80 mx-1" />
        </div>

        {/* Core Tool Triggers with text labels */}
        <button
          type="button"
          onClick={onOpenLogs}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={`${t('footer.liveLogs')} (L)`}
          aria-label={t('sidebar.liveLogs') || t('footer.liveLogs')}
        >
          <Terminal className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.liveLogs')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenHealth}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={t('footer.diagnostics')}
          aria-label={t('sidebar.diagnostics') || t('footer.diagnostics')}
        >
          <Activity className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.diagnostics')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenLatency}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={t('footer.latencyTest')}
          aria-label={t('sidebar.latencyTest') || t('footer.latencyTest')}
        >
          <Wifi className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.latencyTest')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenFolder}
          disabled={loadingOpenFolder}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left disabled:opacity-50"
          title={loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}
          aria-label={t('sidebar.openFolder') || t('footer.openFolder')}
        >
          <FolderOpen className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.openFolder')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenClearCache}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-rose-600 hover:bg-rose-50 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={t('footer.clearCache')}
          aria-label={t('sidebar.clearCache') || t('footer.clearCache')}
        >
          <Trash2 className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.clearCache')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenRoutingGuide}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={t('footer.routingTips')}
          aria-label={t('sidebar.routingTips') || t('footer.routingTips')}
        >
          <Compass className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.routingTips')}</span>
        </button>
      </div>

      {/* Bottom Group: Updates, Shortcuts & About */}
      <div className="flex flex-col gap-1 w-full px-2 pt-2">
        <div className="w-full my-1">
          <div className="h-px bg-slate-200/80 mx-1" />
        </div>

        <button
          type="button"
          onClick={onCheckUpdate}
          className="relative flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={t('footer.checkUpdate')}
          aria-label={t('sidebar.checkUpdate') || t('footer.checkUpdate')}
        >
          <div className="relative shrink-0">
            <RefreshCw className="w-4.5 h-4.5" strokeWidth={1.75} />
            {hasUpdate && (
              <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white animate-pulse" />
            )}
          </div>
          <span className="truncate hidden sm:inline">{t('sidebar.checkUpdate')}</span>
          {hasUpdate && (
            <span className="ml-auto hidden sm:inline-block w-1.5 h-1.5 rounded-full bg-amber-500" />
          )}
        </button>

        <button
          type="button"
          onClick={onOpenShortcuts}
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title={`${t('header.shortcuts')} (?)`}
          aria-label={t('sidebar.shortcuts') || t('header.shortcuts')}
        >
          <HelpCircle className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.shortcuts')}</span>
        </button>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className="flex items-center justify-center sm:justify-start gap-2.5 px-2 sm:px-3 py-2 rounded-xl text-xs sm:text-[13px] text-slate-600 hover:text-slate-900 hover:bg-slate-100/80 font-medium active:scale-[0.98] transition-all cursor-pointer w-full text-left"
          title="GitHub Repository"
          aria-label="GitHub Repository"
        >
          <Github className="w-4.5 h-4.5 shrink-0" strokeWidth={1.75} />
          <span className="truncate hidden sm:inline">{t('sidebar.github')}</span>
        </a>
      </div>
    </aside>
  )
}
