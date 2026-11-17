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
      className="w-14 sm:w-16 bg-white border-r border-slate-200/90 flex flex-col items-center py-3.5 select-none shrink-0 z-30 justify-between transition-all"
      aria-label="Navigation Sidebar"
    >
      {/* Top Group: Logo & Tab Navigation */}
      <div className="flex flex-col items-center gap-2 w-full px-2">
        {/* Brand Mini Badge */}
        <div
          className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 mb-1 transition-colors shadow-2xs ${
            isRunning
              ? 'bg-emerald-600 text-white shadow-emerald-500/20'
              : 'bg-slate-700 text-slate-300'
          }`}
          title={`GBF-Accelerator (${isRunning ? t('header.running') : t('header.stopped')})`}
        >
          <Zap className="w-5 h-5 fill-current" />
        </div>

        {/* Tab 1: Core Dashboard */}
        <button
          type="button"
          onClick={() => onTabChange('core')}
          className={`w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer ${
            activeTab === 'core'
              ? 'bg-sky-100 text-sky-700 shadow-2xs font-semibold'
              : 'text-slate-500 hover:text-slate-900 hover:bg-slate-100'
          }`}
          title={t('tabs.core')}
          aria-label={t('tabs.core')}
        >
          <Home className="w-5 h-5" strokeWidth={1.75} />
        </button>

        {/* Tab 2: Android Companion (Conditionally displayed) */}
        {showAndroidPatch && (
          <button
            type="button"
            onClick={() => onTabChange('android')}
            className={`w-10 h-10 rounded-xl flex items-center justify-center transition-all cursor-pointer ${
              activeTab === 'android'
                ? 'bg-indigo-100 text-indigo-700 shadow-2xs font-semibold'
                : 'text-slate-500 hover:text-slate-900 hover:bg-slate-100'
            }`}
            title={t('tabs.android')}
            aria-label={t('tabs.android')}
          >
            <Smartphone className="w-5 h-5" strokeWidth={1.75} />
          </button>
        )}

        <div className="w-7 h-px bg-slate-200 my-1" />

        {/* Core Tool Triggers (Clash Verge style line icons) */}
        <button
          type="button"
          onClick={onOpenLogs}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title={`${t('footer.liveLogs')} (L)`}
          aria-label={t('footer.liveLogs')}
        >
          <Terminal className="w-5 h-5" strokeWidth={1.75} />
        </button>

        <button
          type="button"
          onClick={onOpenHealth}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title={t('footer.diagnostics')}
          aria-label={t('footer.diagnostics')}
        >
          <Activity className="w-5 h-5" strokeWidth={1.75} />
        </button>

        <button
          type="button"
          onClick={onOpenLatency}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title={t('footer.latencyTest')}
          aria-label={t('footer.latencyTest')}
        >
          <Wifi className="w-5 h-5" strokeWidth={1.75} />
        </button>

        <button
          type="button"
          onClick={onOpenFolder}
          disabled={loadingOpenFolder}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer disabled:opacity-50"
          title={loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}
          aria-label={t('footer.openFolder')}
        >
          <FolderOpen className="w-5 h-5" strokeWidth={1.75} />
        </button>

        <button
          type="button"
          onClick={onOpenClearCache}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-rose-600 hover:bg-rose-50 active:scale-95 transition-all cursor-pointer"
          title={t('footer.clearCache')}
          aria-label={t('footer.clearCache')}
        >
          <Trash2 className="w-5 h-5" strokeWidth={1.75} />
        </button>

        <button
          type="button"
          onClick={onOpenRoutingGuide}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title={t('footer.routingTips')}
          aria-label={t('footer.routingTips')}
        >
          <Compass className="w-5 h-5" strokeWidth={1.75} />
        </button>
      </div>

      {/* Bottom Group: Updates, Shortcuts & About */}
      <div className="flex flex-col items-center gap-2 w-full px-2 pt-2">
        <div className="w-7 h-px bg-slate-200 my-1" />

        <button
          type="button"
          onClick={onCheckUpdate}
          className="relative w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title={t('footer.checkUpdate')}
          aria-label={t('footer.checkUpdate')}
        >
          <RefreshCw className="w-5 h-5" strokeWidth={1.75} />
          {hasUpdate && (
            <span className="absolute top-2 right-2 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white animate-pulse" />
          )}
        </button>

        <button
          type="button"
          onClick={onOpenShortcuts}
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title={`${t('header.shortcuts')} (?)`}
          aria-label={t('header.shortcuts')}
        >
          <HelpCircle className="w-5 h-5" strokeWidth={1.75} />
        </button>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className="w-10 h-10 rounded-xl flex items-center justify-center text-slate-500 hover:text-slate-900 hover:bg-slate-100 active:scale-95 transition-all cursor-pointer"
          title="GitHub Repository"
          aria-label="GitHub Repository"
        >
          <Github className="w-5 h-5" strokeWidth={1.75} />
        </a>
      </div>
    </aside>
  )
}
