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
  PanelLeftClose,
  PanelLeftOpen,
} from 'lucide-react'
import { useI18n } from '../../i18n'
import { LanguageSwitcher } from '../common/LanguageSwitcher'
import { ThemeToggle } from '../common/ThemeToggle'

export interface SidebarProps {
  activeTab: 'core' | 'android'
  onTabChange: (tab: 'core' | 'android') => void
  showAndroidPatch: boolean
  isRunning: boolean
  hasUpdate: boolean
  loadingOpenFolder: boolean
  language: 'zh' | 'en' | 'ja'
  onLanguageSelect: (lang: 'zh' | 'en' | 'ja') => void
  theme?: 'light' | 'dark'
  onToggleTheme?: () => void
  onOpenLogs: () => void
  onOpenHealth: () => void
  onOpenLatency: () => void
  onOpenFolder: () => void
  onOpenClearCache: () => void
  onOpenRoutingGuide: () => void
  onCheckUpdate: () => void
  onOpenShortcuts: () => void
  collapsed?: boolean
  onToggleCollapse?: () => void
}

export const Sidebar: React.FC<SidebarProps> = ({
  activeTab,
  onTabChange,
  showAndroidPatch,
  isRunning,
  hasUpdate,
  loadingOpenFolder,
  language,
  onLanguageSelect,
  theme = 'light',
  onToggleTheme,
  onOpenLogs,
  onOpenHealth,
  onOpenLatency,
  onOpenFolder,
  onOpenClearCache,
  onOpenRoutingGuide,
  onCheckUpdate,
  onOpenShortcuts,
  collapsed = false,
  onToggleCollapse,
}) => {
  const { t } = useI18n()

  return (
    <aside
      className={`${
        collapsed ? 'w-[68px] px-2' : 'w-52 px-3'
      } bg-white border-r border-slate-200/90 flex flex-col justify-between h-screen sticky top-0 py-3.5 select-none shrink-0 z-30 transition-all duration-200 ease-in-out`}
      aria-label="Navigation Sidebar"
    >
      {/* Top Group: Toggle Header, Tabs & Functional Actions */}
      <div className="flex flex-col gap-2 w-full overflow-y-auto overflow-x-hidden min-h-0">
        {/* Sleek Collapse / Expand Toggle Button (No branding text/banner) */}
        {onToggleCollapse && (
          <div className={`flex items-center mb-0.5 ${collapsed ? 'justify-center w-full' : 'justify-end px-1'}`}>
            <button
              type="button"
              onClick={onToggleCollapse}
              className={`${
                collapsed
                  ? 'w-10 h-10 rounded-full bg-slate-50 hover:bg-slate-100 border border-slate-200/70 flex items-center justify-center text-slate-600 hover:text-sky-600 transition-all cursor-pointer shadow-2xs group mx-auto p-0'
                  : 'w-8 h-8 rounded-full flex items-center justify-center text-slate-500 hover:text-slate-800 hover:bg-slate-100/80 active:scale-95 transition-all cursor-pointer'
              }`}
              title={collapsed ? (t('sidebar.expand') || '展开侧栏') : (t('sidebar.collapse') || '折叠侧栏')}
              aria-label={collapsed ? (t('sidebar.expand') || '展开侧栏') : (t('sidebar.collapse') || '折叠侧栏')}
            >
              {collapsed ? (
                <PanelLeftOpen className="w-5 h-5 group-hover:scale-110 transition-transform" strokeWidth={2} />
              ) : (
                <PanelLeftClose className="w-4 h-4" strokeWidth={2} />
              )}
            </button>
          </div>
        )}

        {/* Tabs Container with Smooth Sliding Indicator */}
        <div className="relative flex flex-col gap-2 w-full">
          {showAndroidPatch && (
            <div
              className={`absolute left-0 right-0 top-0 ${
                collapsed ? 'w-10 h-10 mx-auto' : 'h-11'
              } rounded-full transition-all duration-[220ms] ease-[cubic-bezier(.32,.72,0,1)] pointer-events-none z-0 ${
                activeTab === 'core'
                  ? 'translate-y-0 bg-sky-100/90 shadow-2xs'
                  : collapsed
                  ? 'translate-y-[48px] bg-indigo-100/90 shadow-2xs'
                  : 'translate-y-[52px] bg-indigo-100/90 shadow-2xs'
              }`}
              aria-hidden="true"
            />
          )}
          {!showAndroidPatch && (
            <div
              className={`absolute left-0 right-0 top-0 ${
                collapsed ? 'w-10 h-10 mx-auto' : 'h-11'
              } rounded-full bg-sky-100/90 shadow-2xs pointer-events-none z-0`}
              aria-hidden="true"
            />
          )}

          {/* Tab 1: Core Dashboard (控制首页) */}
          <button
            type="button"
            onClick={() => onTabChange('core')}
            className={`relative z-10 flex items-center justify-center ${
              collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-11 w-full gap-3 px-3'
            } rounded-full text-base font-semibold transition-colors duration-150 cursor-pointer ${
              activeTab === 'core'
                ? 'text-sky-800 font-bold'
                : 'text-slate-700 hover:text-slate-900 hover:bg-slate-100/50'
            }`}
            title={t('tabs.core')}
            aria-label={t('sidebar.overview') || t('tabs.core')}
          >
            <div className="relative shrink-0 flex items-center justify-center">
              <Home className="w-5 h-5 shrink-0" strokeWidth={activeTab === 'core' ? 2.2 : 1.85} />
              {isRunning && (
                <span
                  className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-emerald-500 ring-2 ring-white animate-pulse"
                  title="Proxy Running"
                />
              )}
            </div>
            <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
              {t('sidebar.overview')}
            </span>
          </button>

          {/* Tab 2: Android Companion (安卓修补) */}
          {showAndroidPatch && (
            <button
              type="button"
              onClick={() => onTabChange('android')}
              className={`relative z-10 flex items-center justify-center ${
                collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-11 w-full gap-3 px-3'
              } rounded-full text-base font-semibold transition-colors duration-150 cursor-pointer ${
                activeTab === 'android'
                  ? 'text-indigo-800 font-bold'
                  : 'text-slate-700 hover:text-slate-900 hover:bg-slate-100/50'
              }`}
              title={t('tabs.android')}
              aria-label={t('sidebar.android') || t('tabs.android')}
            >
              <div className="relative shrink-0 flex items-center justify-center">
                <Smartphone className="w-5 h-5 shrink-0" strokeWidth={activeTab === 'android' ? 2.2 : 1.85} />
              </div>
              <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
                {t('sidebar.android')}
              </span>
            </button>
          )}
        </div>

        <div className="w-full my-1">
          <div className={`h-px bg-slate-200/80 ${collapsed ? 'w-8 mx-auto' : 'mx-1'}`} />
        </div>

        {/* Core Tool Triggers with centered icons & text labels */}
        <button
          type="button"
          onClick={onOpenLogs}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title={`${t('footer.liveLogs')} (L)`}
          aria-label={t('sidebar.liveLogs') || t('footer.liveLogs')}
        >
          <Terminal className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.liveLogs')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenHealth}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title={t('footer.diagnostics')}
          aria-label={t('sidebar.diagnostics') || t('footer.diagnostics')}
        >
          <Activity className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.diagnostics')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenLatency}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title={t('footer.latencyTest')}
          aria-label={t('sidebar.latencyTest') || t('footer.latencyTest')}
        >
          <Wifi className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.latencyTest')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenFolder}
          disabled={loadingOpenFolder}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer disabled:opacity-50`}
          title={loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}
          aria-label={t('sidebar.openFolder') || t('footer.openFolder')}
        >
          <FolderOpen className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.openFolder')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenClearCache}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-rose-600 hover:bg-rose-50 active:scale-[0.98] transition-all cursor-pointer`}
          title={t('footer.clearCache')}
          aria-label={t('sidebar.clearCache') || t('footer.clearCache')}
        >
          <Trash2 className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.clearCache')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenRoutingGuide}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title={t('footer.routingTips')}
          aria-label={t('sidebar.routingTips') || t('footer.routingTips')}
        >
          <Compass className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.routingTips')}
          </span>
        </button>
      </div>

      {/* Bottom Group: Always pinned to bottom, independent of right content scroll */}
      <div className={`flex flex-col gap-2 w-full pt-3 shrink-0 border-t border-slate-200/80 ${collapsed ? 'items-center' : ''}`}>
        {/* Language Switcher Capsule */}
        <LanguageSwitcher
          language={language}
          onSelect={onLanguageSelect}
          compact={collapsed}
          className={collapsed ? 'mb-0.5' : 'w-full mb-0.5'}
        />

        {/* Day / Night Theme Switcher */}
        {onToggleTheme && (
          <ThemeToggle
            theme={theme}
            onToggle={onToggleTheme}
            compact={collapsed}
            className={collapsed ? 'mb-0.5' : 'w-full'}
          />
        )}

        <button
          type="button"
          onClick={onCheckUpdate}
          className={`relative flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title={t('footer.checkUpdate')}
          aria-label={t('sidebar.checkUpdate') || t('footer.checkUpdate')}
        >
          <div className="relative shrink-0 flex items-center justify-center">
            <RefreshCw className="w-5 h-5 shrink-0" strokeWidth={1.85} />
            {hasUpdate && (
              <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white animate-pulse" />
            )}
          </div>
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.checkUpdate')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenShortcuts}
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title={`${t('header.shortcuts')} (?)`}
          aria-label={t('sidebar.shortcuts') || t('header.shortcuts')}
        >
          <HelpCircle className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.shortcuts')}
          </span>
        </button>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className={`flex items-center justify-center ${
            collapsed ? 'w-10 h-10 mx-auto p-0' : 'h-10 w-full gap-3 px-3'
          } rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer`}
          title="GitHub Repository"
          aria-label="GitHub Repository"
        >
          <Github className="w-5 h-5 shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.github')}
          </span>
        </a>
      </div>
    </aside>
  )
}
