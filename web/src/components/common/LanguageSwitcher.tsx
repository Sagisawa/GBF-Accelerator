import React from 'react'

export interface LanguageSwitcherProps {
  language: 'zh' | 'en' | 'ja'
  onSelect: (lang: 'zh' | 'en' | 'ja') => void
  className?: string
}

/**
 * Segmented Language Switcher capsule with smooth sliding pill indicator
 * Inspired by smooth-option-switcher
 */
export const LanguageSwitcher: React.FC<LanguageSwitcherProps> = ({
  language,
  onSelect,
  className = '',
}) => {
  const options: Array<{ code: 'zh' | 'en' | 'ja'; label: string }> = [
    { code: 'zh', label: '简中' },
    { code: 'en', label: 'EN' },
    { code: 'ja', label: '日本語' },
  ]

  const activeIndex = options.findIndex((opt) => opt.code === language)
  const safeIndex = activeIndex >= 0 ? activeIndex : 0

  return (
    <div
      role="radiogroup"
      aria-label="Language selection"
      className={`relative inline-flex items-center rounded-full bg-slate-100 p-1 border border-slate-200/90 text-xs font-semibold shadow-2xs select-none ${className}`}
    >
      {/* Smooth Sliding Pill Indicator */}
      <div
        className="absolute top-1 bottom-1 w-[calc((100%-8px)/3)] bg-white rounded-full shadow-xs transition-transform duration-[220ms] ease-[cubic-bezier(.32,.72,0,1)] pointer-events-none"
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
            className={`relative z-10 flex-1 px-2 py-1 rounded-full transition-colors duration-150 cursor-pointer text-center min-w-[46px] focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-500/40 ${
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
