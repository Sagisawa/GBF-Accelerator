import React from 'react'
import { Sun, Moon } from 'lucide-react'
import { useI18n } from '../../i18n'

export interface ThemeToggleProps {
  theme: 'light' | 'dark'
  onToggle: () => void
  compact?: boolean
  className?: string
}

export const ThemeToggle: React.FC<ThemeToggleProps> = ({
  theme,
  onToggle,
  compact = false,
  className = '',
}) => {
  const { t } = useI18n()
  const isDark = theme === 'dark'

  const titleText = isDark
    ? `${t('sidebar.themeLight') || '浅色模式'} (T)`
    : `${t('sidebar.themeDark') || '深色模式'} (T)`

  const labelText = isDark
    ? t('sidebar.themeLight') || '浅色模式'
    : t('sidebar.themeDark') || '深色模式'

  if (compact) {
    return (
      <div className={`flex justify-center w-full ${className}`}>
        <button
          type="button"
          onClick={onToggle}
          className="w-10 h-10 rounded-full bg-slate-50 hover:bg-slate-100 border border-slate-200/70 flex items-center justify-center text-slate-700 hover:text-slate-900 transition-all cursor-pointer shadow-2xs group mx-auto p-0 active:scale-95"
          title={titleText}
          aria-label={titleText}
        >
          <div className="relative shrink-0 flex items-center justify-center transition-transform duration-300 ease-[cubic-bezier(.32,.72,0,1)]">
            {isDark ? (
              <Sun
                className="w-5 h-5 text-amber-400 group-hover:scale-110 transition-transform"
                strokeWidth={2}
              />
            ) : (
              <Moon
                className="w-5 h-5 text-slate-700 group-hover:scale-110 transition-transform"
                strokeWidth={1.85}
              />
            )}
          </div>
        </button>
      </div>
    )
  }

  return (
    <button
      type="button"
      onClick={onToggle}
      className={`flex items-center justify-center h-10 w-full gap-3 px-3 rounded-full text-base font-semibold text-slate-700 hover:text-slate-900 hover:bg-slate-100/80 active:scale-[0.98] transition-all cursor-pointer group ${className}`}
      title={titleText}
      aria-label={titleText}
    >
      <div className="relative shrink-0 flex items-center justify-center transition-transform duration-300 ease-[cubic-bezier(.32,.72,0,1)]">
        {isDark ? (
          <Sun
            className="w-5 h-5 text-amber-400 group-hover:scale-110 group-hover:rotate-45 transition-transform"
            strokeWidth={2}
          />
        ) : (
          <Moon
            className="w-5 h-5 text-slate-700 group-hover:scale-110 group-hover:-rotate-12 transition-transform"
            strokeWidth={1.85}
          />
        )}
      </div>
      <span className="whitespace-nowrap leading-tight">
        {labelText}
      </span>
    </button>
  )
}
