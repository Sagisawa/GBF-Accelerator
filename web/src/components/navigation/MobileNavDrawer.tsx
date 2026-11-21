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
  X,
  Zap,
} from 'lucide-react'
import { useI18n } from '../../i18n'
import { SupportedLanguage } from '../../i18n/types'
import { LanguageSwitcher } from '../common/LanguageSwitcher'
import { ThemeToggle } from '../common/ThemeToggle'

export interface MobileNavDrawerProps {
  isOpen: boolean
  onClose: () => void
  activeTab: 'core' | 'android'
  onTabChange: (tab: 'core' | 'android') => void
  showAndroidPatch: boolean
  isRunning: boolean
  hasUpdate: boolean
  loadingOpenFolder: boolean
  language: SupportedLanguage
  onLanguageSelect: (lang: SupportedLanguage) => void
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
}

export const MobileNavDrawer: React.FC<MobileNavDrawerProps> = ({
  isOpen,
  onClose,
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
}) => {
  const { t } = useI18n()

  if (!isOpen) return null

  const handleAction = (fn: () => void) => {
    fn()
    onClose()
  }

  const baseActionClass =
    'flex items-center h-10 w-full justify-start gap-3 px-3 rounded-xl text-sm font-medium text-label-secondary hover:text-label-primary hover:bg-slate-500/10 active:scale-[0.98] transition-all cursor-pointer'

  return (
    <div className="fixed inset-0 z-50 flex md:hidden" role="dialog" aria-modal="true" aria-label="Mobile Navigation">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-slate-950/45 backdrop-blur-sm animate-fade-soft"
        onClick={onClose}
        aria-hidden="true"
      />

      {/* Drawer Content */}
      <aside
        className="relative z-10 w-72 max-w-[85vw] h-full liquid-glass rounded-r-[24px] rounded-l-none flex flex-col justify-between py-4 px-3.5 shadow-2xl select-none animate-slide-right overflow-y-auto"
        aria-label="Mobile Navigation Drawer"
      >
        {/* Top Header */}
        <div className="flex flex-col gap-2 w-full">
          <div className="flex items-center justify-between pb-3 hairline-b">
            <div className="flex items-center gap-2.5">
              <div className="w-8 h-8 rounded-lg bg-emerald-600 text-white flex items-center justify-center shrink-0 shadow-sm">
                <Zap className="w-4 h-4 fill-current" />
              </div>
              <span className="font-bold text-sm text-label-primary tracking-tight">
                {t('header.title')}
              </span>
            </div>
            <button
              type="button"
              onClick={onClose}
              className="w-8 h-8 rounded-lg flex items-center justify-center text-label-tertiary hover:text-label-primary hover:bg-slate-500/10 active:scale-95 transition-all cursor-pointer"
              aria-label={t('common.close') || '关闭'}
            >
              <X className="w-5 h-5" />
            </button>
          </div>

          {/* Primary View Tabs */}
          <div className="flex flex-col gap-1.5 w-full pt-1">
            <button
              type="button"
              onClick={() => handleAction(() => onTabChange('core'))}
              className={`flex items-center h-10 w-full justify-start gap-3 px-3 rounded-xl text-sm cursor-pointer transition-colors ${
                activeTab === 'core'
                  ? 'bg-sky-500/15 text-sky-700 dark:text-sky-300 font-semibold'
                  : 'text-label-secondary font-medium hover:text-label-primary hover:bg-slate-500/10'
              }`}
            >
              <div className="relative shrink-0 flex items-center justify-center">
                <Home className="w-4 h-4 shrink-0" strokeWidth={activeTab === 'core' ? 2.2 : 1.85} />
                {isRunning && (
                  <span
                    className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-emerald-500 ring-2 ring-white dark:ring-slate-900"
                    title="Proxy Running"
                  />
                )}
              </div>
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.overview')}
              </span>
            </button>

            {showAndroidPatch && (
              <button
                type="button"
                onClick={() => handleAction(() => onTabChange('android'))}
                className={`flex items-center h-10 w-full justify-start gap-3 px-3 rounded-xl text-sm cursor-pointer transition-colors ${
                  activeTab === 'android'
                    ? 'bg-indigo-500/15 text-indigo-700 dark:text-indigo-300 font-semibold'
                    : 'text-label-secondary font-medium hover:text-label-primary hover:bg-slate-500/10'
                }`}
              >
                <Smartphone className="w-4 h-4 shrink-0" strokeWidth={activeTab === 'android' ? 2.2 : 1.85} />
                <span className="whitespace-nowrap leading-tight truncate">
                  {t('sidebar.android')}
                </span>
              </button>
            )}
          </div>

          <div className="w-full my-1">
            <div className="h-px bg-slate-500/15 mx-1" />
          </div>

          {/* Diagnostics & Utilities */}
          <div className="flex flex-col gap-1 w-full">
            <button
              type="button"
              onClick={() => handleAction(onOpenLogs)}
              className={baseActionClass}
            >
              <Terminal className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.liveLogs')}
              </span>
            </button>

            <button
              type="button"
              onClick={() => handleAction(onOpenHealth)}
              className={baseActionClass}
            >
              <Activity className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.diagnostics')}
              </span>
            </button>

            <button
              type="button"
              onClick={() => handleAction(onOpenLatency)}
              className={baseActionClass}
            >
              <Wifi className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.latencyTest')}
              </span>
            </button>

            <div className="w-full my-0.5">
              <div className="h-px bg-slate-500/10 mx-2" />
            </div>

            <button
              type="button"
              onClick={() => handleAction(onOpenFolder)}
              disabled={loadingOpenFolder}
              className={`${baseActionClass} disabled:opacity-50`}
            >
              <FolderOpen className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.openFolder')}
              </span>
            </button>

            <button
              type="button"
              onClick={() => handleAction(onOpenClearCache)}
              className="flex items-center h-10 w-full justify-start gap-3 px-3 rounded-xl text-sm font-medium text-label-secondary hover:text-rose-600 dark:hover:text-rose-400 hover:bg-rose-500/10 active:scale-[0.98] transition-all cursor-pointer"
            >
              <Trash2 className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.clearCache')}
              </span>
            </button>

            <button
              type="button"
              onClick={() => handleAction(onOpenRoutingGuide)}
              className={baseActionClass}
            >
              <Compass className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              <span className="whitespace-nowrap leading-tight truncate">
                {t('sidebar.routingTips')}
              </span>
            </button>
          </div>
        </div>

        {/* Bottom Section */}
        <div className="flex flex-col gap-1.5 w-full pt-3 shrink-0 hairline-t">
          <LanguageSwitcher
            language={language}
            onSelect={onLanguageSelect}
            className="w-full mb-0.5"
          />

          {onToggleTheme && (
            <ThemeToggle
              theme={theme}
              onToggle={onToggleTheme}
              className="w-full"
            />
          )}

          <button
            type="button"
            onClick={() => handleAction(onCheckUpdate)}
            className={`relative ${baseActionClass}`}
          >
            <div className="relative shrink-0 flex items-center justify-center">
              <RefreshCw className="w-4 h-4 shrink-0" strokeWidth={1.85} />
              {hasUpdate && (
                <span className="absolute -top-1 -right-1 w-2 h-2 rounded-full bg-amber-500 ring-2 ring-white dark:ring-slate-900" />
              )}
            </div>
            <span className="whitespace-nowrap leading-tight truncate">
              {t('sidebar.checkUpdate')}
            </span>
          </button>

          <button
            type="button"
            onClick={() => handleAction(onOpenShortcuts)}
            className={baseActionClass}
          >
            <HelpCircle className="w-4 h-4 shrink-0" strokeWidth={1.85} />
            <span className="whitespace-nowrap leading-tight truncate">
              {t('sidebar.shortcuts')}
            </span>
          </button>

          <a
            href="https://github.com/Sagisawa/GBF-Accelerator"
            target="_blank"
            rel="noreferrer"
            className={baseActionClass}
          >
            <Github className="w-4 h-4 shrink-0" strokeWidth={1.85} />
            <span className="whitespace-nowrap leading-tight truncate">
              {t('sidebar.github')}
            </span>
          </a>
        </div>
      </aside>
    </div>
  )
}
