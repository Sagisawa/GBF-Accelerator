import React from 'react'

export interface LanguageSwitcherProps {
  language: 'zh' | 'en' | 'ja'
  onSelect: (lang: 'zh' | 'en' | 'ja') => void
  className?: string
  compact?: boolean
}

/**
 * Segmented Language Switcher capsule with smooth sliding Liquid Pill indicator
 */
export const LanguageSwitcher: React.FC<LanguageSwitcherProps> = ({
  language,
  onSelect,
  className = '',
  compact = false,
}) => {
  const options: Array<{ code: 'zh' | 'en' | 'ja'; label: string }> = [
    { code: 'zh', label: '简中' },
    { code: 'en', label: 'EN' },
    { code: 'ja', label: '日本語' },
  ]

  const activeIndex = options.findIndex((opt) => opt.code === language)
  const safeIndex = activeIndex >= 0 ? activeIndex : 0

  if (compact) {
    const nextLang = language === 'zh' ? 'en' : language === 'en' ? 'ja' : 'zh'
    const currentOption = options[safeIndex] || options[0]
    const shortLabel = currentOption.code === 'zh' ? '中' : currentOption.code === 'ja' ? '日' : 'EN'

    return (
      <div className={`flex justify-center w-full ${className}`}>
        <button
          type="button"
          onClick={() => onSelect(nextLang)}
          className="ui-control flex items-center justify-center w-10 h-10 rounded-full text-xs font-bold transition-all cursor-pointer active:scale-95 mx-auto p-0"
          title={`语言 / Language: ${currentOption.label} (点击切换)`}
          aria-label={`Current language: ${currentOption.label}. Click to switch language`}
        >
          <span className="leading-none">{shortLabel}</span>
        </button>
      </div>
    )
  }

  return (
    <div
      role="radiogroup"
      aria-label="Language selection"
      className={`relative inline-flex items-center rounded-full inset-panel p-1 text-xs font-semibold select-none w-full ${className}`}
    >
      {/* Smooth Sliding Liquid Pill Indicator */}
      <div
        className="liquid-pill absolute top-1 bottom-1 left-1 w-[calc((100%-8px)/3)] rounded-full transition-transform duration-[240ms] ease-fluid pointer-events-none"
        style={{
          transform: `translateX(${safeIndex * 100}%)`,
        }}
        aria-hidden="true"
      />

      {options.map((opt) => {
        const isSelected = language === opt.code
        return (
          <button
            key={opt.code}
            type="button"
            role="radio"
            aria-checked={isSelected}
            onClick={() => onSelect(opt.code)}
            className={`relative z-10 flex-1 flex items-center justify-center py-1 px-1 rounded-full transition-colors duration-150 cursor-pointer text-center whitespace-nowrap text-xs tracking-tight focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-500/40 ${
              isSelected
                ? 'text-sky-700 dark:text-sky-300 font-bold'
                : 'text-label-tertiary hover:text-label-primary'
            }`}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
