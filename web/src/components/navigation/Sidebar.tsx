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
      className="w-44 bg-white border-r border-slate-200/90 flex flex-col justify-between h-screen sticky top-0 py-3 select-none shrink-0 z-30 transition-all"
      aria-label="Navigation Sidebar"
    >
      {/* Top Group: Tabs & Functional Actions */}
      <div className="flex flex-col gap-1 w-full px-2.5 overflow-y-auto min-h-0">
        {/* Tab 1: Core Dashboard (控制首页) */}
        <button
          type="button"
          onClick={() => onTabChange('core')}
          className={`flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold transition-all cursor-pointer w-full ${
            activeTab === 'core'
              ? 'bg-sky-100/90 text-sky-800 font-bold shadow-2xs'
              : 'text-slate-700 hover:text-slate-900 hover:bg-slate-100/80'
          }`}
          title={t('tabs.core')}
          aria-label={t('sidebar.overview') || t('tabs.core')}
        >
          <Home className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.overview')}</span>
        </button>

        {/* Tab 2: Android Companion (安卓修补) */}
        {showAndroidPatch && (
          <button
            type="button"
            onClick={() => onTabChange('android')}
            className={`flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold transition-all cursor-pointer w-full ${
              activeTab === 'android'
                ? 'bg-indigo-100/90 text-indigo-800 font-bold shadow-2xs'
                : 'text-slate-700 hover:text-slate-900 hover:bg-slate-100/80'
            }`}
            title={t('tabs.android')}
            aria-label={t('sidebar.android') || t('tabs.android')}
          >
            <Smartphone className="w-5 h-5 shrink-0" strokeWidth={1.85} />
            <span className="truncate leading-tight">{t('sidebar.android')}</span>
          </button>
        )}

        <div className="w-full my-1">
          <div className="h-px bg-slate-200/80 mx-1" />
        </div>

        {/* Core Tool Triggers with bold, readable text labels */}
        <button
          type="button"
          onClick={onOpenLogs}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={`${t('footer.liveLogs')} (L)`}
          aria-label={t('sidebar.liveLogs') || t('footer.liveLogs')}
        >
          <Terminal className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.liveLogs')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenHealth}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.diagnostics')}
          aria-label={t('sidebar.diagnostics') || t('footer.diagnostics')}
        >
          <Activity className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.diagnostics')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenLatency}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.latencyTest')}
          aria-label={t('sidebar.latencyTest') || t('footer.latencyTest')}
        >
          <Wifi className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.latencyTest')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenFolder}
          disabled={loadingOpenFolder}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full disabled:opacity-50"
          title={loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}
          aria-label={t('sidebar.openFolder') || t('footer.openFolder')}
        >
          <FolderOpen className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.openFolder')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenClearCache}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-rose-600 hover:bg-rose-50 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.clearCache')}
          aria-label={t('sidebar.clearCache') || t('footer.clearCache')}
        >
          <Trash2 className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.clearCache')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenRoutingGuide}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.routingTips')}
          aria-label={t('sidebar.routingTips') || t('footer.routingTips')}
        >
          <Compass className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.routingTips')}</span>
        </button>
      </div>

      {/* Bottom Group: Always pinned to bottom, independent of right content scroll */}
      <div className="flex flex-col gap-1 w-full px-2.5 pt-2 shrink-0 border-t border-slate-200/80">
        <button
          type="button"
          onClick={onCheckUpdate}
          className="relative flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={t('footer.checkUpdate')}
          aria-label={t('sidebar.checkUpdate') || t('footer.checkUpdate')}
        >
          <div className="relative shrink-0">
            <RefreshCw className="w-5 h-5" strokeWidth={1.85} />
            {hasUpdate && (
              <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white animate-pulse" />
            )}
          </div>
          <span className="truncate leading-tight">{t('sidebar.checkUpdate')}</span>
        </button>

        <button
          type="button"
          onClick={onOpenShortcuts}
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title={`${t('header.shortcuts')} (?)`}
          aria-label={t('sidebar.shortcuts') || t('header.shortcuts')}
        >
          <HelpCircle className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.shortcuts')}</span>
        </button>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className="flex items-center justify-center gap-2.5 px-3 py-2 rounded-xl text-[15px] font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer w-full"
          title="GitHub Repository"
          aria-label="GitHub Repository"
        >
          <Github className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className="truncate leading-tight">{t('sidebar.github')}</span>
        </a>
      </div>
    </aside>
  )
}
