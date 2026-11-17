import React from 'react'

export interface ToggleSwitchProps {
  checked: boolean
  onChange: () => void
  disabled?: boolean
  id?: string
  ariaLabel?: string
}

/**
 * Standard iOS/macOS styled Toggle Switch component
 * - Track: 48x28px, rounded-full (999px), p-[2px]
 * - Thumb: 24x24px (4px shorter than track, 2px margins left & right), with 1px shadow
 * - Active: Track emerald-500, thumb translateX(20px) with 220ms cubic-bezier(.32,.72,0,1)
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
      className={`relative inline-flex items-center shrink-0 w-12 h-7 p-[2px] rounded-full transition-colors duration-200 cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/50 focus-visible:ring-offset-2 ${
        checked
          ? 'bg-emerald-500 hover:bg-emerald-600'
          : 'bg-slate-300 hover:bg-slate-400'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
    >
      <span
        className={`w-6 h-6 rounded-full bg-white shadow-[0_1px_3px_rgba(0,0,0,0.2)] transition-transform duration-[220ms] ease-[cubic-bezier(.32,.72,0,1)] ${
          checked ? 'translate-x-5' : 'translate-x-0'
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
    <div className={`py-2.5 border-b border-slate-100 last:border-b-0 space-y-2 ${className}`}>
      <div className="flex items-center justify-between gap-4">
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
