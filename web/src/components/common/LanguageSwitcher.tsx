import React from 'react'

export interface LanguageSwitcherProps {
  language: 'zh' | 'en' | 'ja'
  onSelect: (lang: 'zh' | 'en' | 'ja') => void
  className?: string
  compact?: boolean
}

/**
 * Segmented Language Switcher capsule with smooth sliding pill indicator
 * Inspired by smooth-option-switcher
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
          className="flex items-center justify-center w-10 h-10 rounded-full bg-slate-100/90 hover:bg-slate-200/90 border border-slate-200/90 text-xs font-bold text-slate-700 hover:text-slate-900 transition-all cursor-pointer shadow-2xs active:scale-95 mx-auto p-0"
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
      className={`relative inline-flex items-center rounded-full bg-slate-100 p-1 border border-slate-200/90 text-xs font-semibold shadow-2xs select-none w-full ${className}`}
    >
      {/* Smooth Sliding Pill Indicator */}
      <div
        className="absolute top-1 bottom-1 left-1 w-[calc((100%-8px)/3)] bg-white rounded-full shadow-xs transition-transform duration-[220ms] ease-[cubic-bezier(.32,.72,0,1)] pointer-events-none"
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
                ? 'text-sky-700 font-bold'
                : 'text-slate-600 hover:text-slate-900'
            }`}
          >
            {opt.label}
          </button>
        )
      })}
    </div>
  )
}
