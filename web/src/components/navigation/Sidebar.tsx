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

  const navItemClass = collapsed
    ? 'w-10 h-10 mx-auto p-0 justify-center rounded-full'
    : 'h-9 w-full justify-start gap-2.5 px-2.5 rounded-xl text-[13px] font-medium'

  const baseActionClass = `flex items-center ${navItemClass} text-label-secondary hover:text-label-primary hover:bg-slate-500/10 active:scale-[0.98] transition-all cursor-pointer`

  return (
    <aside
      className={`${
        collapsed ? 'w-[68px] px-2' : 'w-52 px-3'
      } liquid-glass rounded-[20px] my-2.5 ml-2.5 sm:my-3 sm:ml-3 h-[calc(100vh-1.25rem)] sm:h-[calc(100vh-1.5rem)] sticky top-2.5 sm:top-3 flex flex-col justify-between py-3.5 select-none shrink-0 z-30 transition-[width,padding] duration-200 ease-fluid`}
      aria-label="Navigation Sidebar"
    >
      {/* Top Group: Collapse Toggle, View Tabs & Tool Actions */}
      <div className="flex flex-col gap-1.5 w-full overflow-y-auto overflow-x-hidden min-h-0">
        {onToggleCollapse && (
          <div className={`flex items-center mb-1 ${collapsed ? 'justify-center w-full' : 'justify-end px-0.5'}`}>
            <button
              type="button"
              onClick={onToggleCollapse}
              className={`${
                collapsed
                  ? 'ui-control w-10 h-10 rounded-full flex items-center justify-center hover:text-sky-600 dark:hover:text-sky-400 transition-all cursor-pointer group mx-auto p-0'
                  : 'w-8 h-8 rounded-lg flex items-center justify-center text-label-tertiary hover:text-label-primary hover:bg-slate-500/10 active:scale-95 transition-all cursor-pointer'
              }`}
              title={collapsed ? (t('sidebar.expand') || '展开侧栏') : (t('sidebar.collapse') || '折叠侧栏')}
              aria-label={collapsed ? (t('sidebar.expand') || '展开侧栏') : (t('sidebar.collapse') || '折叠侧栏')}
            >
              {collapsed ? (
                <PanelLeftOpen className="w-[18px] h-[18px] group-hover:scale-110 transition-transform" strokeWidth={2} />
              ) : (
                <PanelLeftClose className="w-4 h-4" strokeWidth={2} />
              )}
            </button>
          </div>
        )}

        {/* Primary View Tabs with Liquid Pill Sliding Indicator */}
        <div className="relative flex flex-col gap-1.5 w-full">
          {showAndroidPatch ? (
            <div
              className={`liquid-pill absolute left-0 right-0 top-0 ${
                collapsed ? 'w-10 h-10 mx-auto rounded-full' : 'h-10 rounded-xl'
              } transition-transform duration-[240ms] ease-fluid pointer-events-none z-0 ${
                activeTab === 'core'
                  ? 'translate-y-0'
                  : 'translate-y-[46px]'
              }`}
              aria-hidden="true"
            />
          ) : (
            <div
              className={`liquid-pill absolute left-0 right-0 top-0 ${
                collapsed ? 'w-10 h-10 mx-auto rounded-full' : 'h-10 rounded-xl'
              } pointer-events-none z-0`}
              aria-hidden="true"
            />
          )}

          {/* Tab 1: Core Dashboard */}
          <button
            type="button"
            onClick={() => onTabChange('core')}
            className={`relative z-10 flex items-center ${
              collapsed ? 'w-10 h-10 mx-auto p-0 justify-center rounded-full' : 'h-10 w-full justify-start gap-2.5 px-2.5 rounded-xl text-[13px]'
            } transition-colors duration-150 cursor-pointer ${
              activeTab === 'core'
                ? 'text-sky-700 dark:text-sky-300 font-semibold'
                : 'text-label-secondary font-medium hover:text-label-primary hover:bg-slate-500/10'
            }`}
            title={t('tabs.core')}
            aria-label={t('sidebar.overview') || t('tabs.core')}
          >
            <div className="relative shrink-0 flex items-center justify-center">
              <Home className="w-[18px] h-[18px] shrink-0" strokeWidth={activeTab === 'core' ? 2.2 : 1.85} />
              {isRunning && (
                <span
                  className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-emerald-500 ring-2 ring-white dark:ring-slate-900"
                  title="Proxy Running"
                />
              )}
            </div>
            <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
              {t('sidebar.overview')}
            </span>
          </button>

          {/* Tab 2: Android Companion */}
          {showAndroidPatch && (
            <button
              type="button"
              onClick={() => onTabChange('android')}
              className={`relative z-10 flex items-center ${
                collapsed ? 'w-10 h-10 mx-auto p-0 justify-center rounded-full' : 'h-10 w-full justify-start gap-2.5 px-2.5 rounded-xl text-[13px]'
              } transition-colors duration-150 cursor-pointer ${
                activeTab === 'android'
                  ? 'text-indigo-700 dark:text-indigo-300 font-semibold'
                  : 'text-label-secondary font-medium hover:text-label-primary hover:bg-slate-500/10'
              }`}
              title={t('tabs.android')}
              aria-label={t('sidebar.android') || t('tabs.android')}
            >
              <div className="relative shrink-0 flex items-center justify-center">
                <Smartphone className="w-[18px] h-[18px] shrink-0" strokeWidth={activeTab === 'android' ? 2.2 : 1.85} />
              </div>
              <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
                {t('sidebar.android')}
              </span>
            </button>
          )}
        </div>

        <div className="w-full my-1">
          <div className={`h-px bg-slate-500/15 ${collapsed ? 'w-8 mx-auto' : 'mx-1'}`} />
        </div>

        {/* Diagnostics & Utilities */}
        <button
          type="button"
          onClick={onOpenLogs}
          className={baseActionClass}
          title={`${t('footer.liveLogs')} (L)`}
          aria-label={t('sidebar.liveLogs') || t('footer.liveLogs')}
        >
          <Terminal className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.liveLogs')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenHealth}
          className={baseActionClass}
          title={t('footer.diagnostics')}
          aria-label={t('sidebar.diagnostics') || t('footer.diagnostics')}
        >
          <Activity className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.diagnostics')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenLatency}
          className={baseActionClass}
          title={t('footer.latencyTest')}
          aria-label={t('sidebar.latencyTest') || t('footer.latencyTest')}
        >
          <Wifi className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.latencyTest')}
          </span>
        </button>

        <div className="w-full my-0.5">
          <div className={`h-px bg-slate-500/10 ${collapsed ? 'w-6 mx-auto' : 'mx-2'}`} />
        </div>

        {/* Cache & Routing Tools */}
        <button
          type="button"
          onClick={onOpenFolder}
          disabled={loadingOpenFolder}
          className={`${baseActionClass} disabled:opacity-50`}
          title={loadingOpenFolder ? t('common.loading') : t('footer.openFolder')}
          aria-label={t('sidebar.openFolder') || t('footer.openFolder')}
        >
          <FolderOpen className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.openFolder')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenClearCache}
          className={`flex items-center ${navItemClass} text-label-secondary hover:text-rose-600 dark:hover:text-rose-400 hover:bg-rose-500/10 active:scale-[0.98] transition-all cursor-pointer`}
          title={t('footer.clearCache')}
          aria-label={t('sidebar.clearCache') || t('footer.clearCache')}
        >
          <Trash2 className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.clearCache')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenRoutingGuide}
          className={baseActionClass}
          title={t('footer.routingTips')}
          aria-label={t('sidebar.routingTips') || t('footer.routingTips')}
        >
          <Compass className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.routingTips')}
          </span>
        </button>
      </div>

      {/* Bottom Group: Language, Theme, Update, Shortcuts, GitHub */}
      <div className={`flex flex-col gap-1.5 w-full pt-3 shrink-0 hairline-t ${collapsed ? 'items-center' : ''}`}>
        <LanguageSwitcher
          language={language}
          onSelect={onLanguageSelect}
          compact={collapsed}
          className={collapsed ? 'mb-0.5' : 'w-full mb-0.5'}
        />

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
          className={`relative ${baseActionClass}`}
          title={t('footer.checkUpdate')}
          aria-label={t('sidebar.checkUpdate') || t('footer.checkUpdate')}
        >
          <div className="relative shrink-0 flex items-center justify-center">
            <RefreshCw className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
            {hasUpdate && (
              <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white dark:ring-slate-900" />
            )}
          </div>
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.checkUpdate')}
          </span>
        </button>

        <button
          type="button"
          onClick={onOpenShortcuts}
          className={baseActionClass}
          title={`${t('header.shortcuts')} (?)`}
          aria-label={t('sidebar.shortcuts') || t('header.shortcuts')}
        >
          <HelpCircle className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.shortcuts')}
          </span>
        </button>

        <a
          href="https://github.com/Sagisawa/GBF-Accelerator"
          target="_blank"
          rel="noreferrer"
          className={baseActionClass}
          title="GitHub Repository"
          aria-label="GitHub Repository"
        >
          <Github className="w-[18px] h-[18px] shrink-0" strokeWidth={1.85} />
          <span className={`whitespace-nowrap leading-tight truncate ${collapsed ? 'sr-only' : ''}`}>
            {t('sidebar.github')}
          </span>
        </a>
      </div>
    </aside>
  )
}
