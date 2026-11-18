import React from 'react'

export interface ToggleSwitchProps {
  checked: boolean
  onChange: () => void
  disabled?: boolean
  id?: string
  ariaLabel?: string
}

/**
 * Standard compact iOS/macOS styled Toggle Switch component
 * - Track: 36x20px (w-9 h-5), rounded-full (999px), p-0.5 (2px)
 * - Thumb: 16x16px (w-4 h-4, 4px shorter than track, 2px margins), with 1px shadow
 * - Active: Track emerald-500, thumb translateX(16px / translate-x-4) with 200ms cubic-bezier(.32,.72,0,1)
 * - Hover: Subtle darken on track to indicate clickability
 */
export const ToggleSwitch: React.FC<ToggleSwitchProps> = ({
  checked,
  onChange,
  disabled = false,
  id,
  ariaLabel,
}) => {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={ariaLabel}
      id={id}
      disabled={disabled}
      onClick={onChange}
      className={`relative inline-flex items-center shrink-0 w-9 h-5 p-0.5 rounded-full transition-colors duration-150 cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/50 focus-visible:ring-offset-2 ${
        checked
          ? 'bg-emerald-500 hover:bg-emerald-600'
          : 'bg-slate-300 hover:bg-slate-400'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
    >
      <span
        className={`w-4 h-4 rounded-full bg-white shadow-[0_1px_2px_rgba(0,0,0,0.2)] transition-transform duration-[200ms] ease-[cubic-bezier(.32,.72,0,1)] ${
          checked ? 'translate-x-4' : 'translate-x-0'
        }`}
      />
    </button>
  )
}

export interface SettingToggleRowProps {
  label: React.ReactNode
  description?: React.ReactNode
  checked: boolean
  onChange: () => void
  disabled?: boolean
  ariaLabel?: string
  className?: string
  children?: React.ReactNode
}

/**
 * Full row layout: Left label + description, right switch, divider border
 */
export const SettingToggleRow: React.FC<SettingToggleRowProps> = ({
  label,
  description,
  checked,
  onChange,
  disabled = false,
  ariaLabel,
  className = '',
  children,
}) => {
  const handleRowClick = () => {
    if (!disabled) {
      onChange()
    }
  }

  return (
    <div className={`py-2 border-b border-slate-100 last:border-b-0 space-y-1.5 ${className}`}>
      <div className="flex items-center justify-between gap-3">
        <div
          className={`space-y-0.5 min-w-0 pr-2 ${disabled ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer select-none'}`}
          onClick={handleRowClick}
        >
          <div className="text-[13px] sm:text-sm font-semibold text-slate-800 leading-snug">
            {label}
          </div>
          {description && (
            <div className="text-xs text-slate-500 leading-relaxed">
              {description}
            </div>
          )}
        </div>
        <ToggleSwitch
          checked={checked}
          onChange={onChange}
          disabled={disabled}
          ariaLabel={ariaLabel || (typeof label === 'string' ? label : undefined)}
        />
      </div>
      {children}
    </div>
  )
}
