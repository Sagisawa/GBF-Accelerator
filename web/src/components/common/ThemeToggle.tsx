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
          className="ui-control w-10 h-10 rounded-full flex items-center justify-center transition-all cursor-pointer group mx-auto p-0 active:scale-95"
          title={titleText}
          aria-label={titleText}
        >
          <div className="relative shrink-0 flex items-center justify-center transition-transform duration-300 ease-fluid">
            {isDark ? (
              <Sun
                className="w-[18px] h-[18px] text-amber-400 group-hover:scale-110 transition-transform"
                strokeWidth={2}
              />
            ) : (
              <Moon
                className="w-[18px] h-[18px] text-label-secondary group-hover:scale-110 transition-transform"
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
      className={`flex items-center justify-start h-9 w-full gap-2.5 px-2.5 rounded-xl text-[13px] font-medium text-label-secondary hover:text-label-primary hover:bg-slate-500/10 active:scale-[0.98] transition-all cursor-pointer group ${className}`}
      title={titleText}
      aria-label={titleText}
    >
      <div className="relative shrink-0 flex items-center justify-center transition-transform duration-300 ease-fluid">
        {isDark ? (
          <Sun
            className="w-[18px] h-[18px] text-amber-400 group-hover:scale-110 group-hover:rotate-45 transition-transform"
            strokeWidth={2}
          />
        ) : (
          <Moon
            className="w-[18px] h-[18px] text-label-secondary group-hover:scale-110 group-hover:-rotate-12 transition-transform"
            strokeWidth={1.85}
          />
        )}
      </div>
      <span className="whitespace-nowrap leading-tight truncate">
        {labelText}
      </span>
    </button>
  )
}
