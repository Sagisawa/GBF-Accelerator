import React from 'react'
import { cn } from '../../utils/cn'

export interface SwitchProps {
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
  label?: string
  description?: string
  color?: 'green' | 'blue'
  className?: string
}

export const Switch: React.FC<SwitchProps> = ({
  checked,
  onChange,
  disabled = false,
  label,
  description,
  color = 'green',
  className,
}) => {
  const activeBg = color === 'green' ? 'bg-sys-green' : 'bg-sys-blue'

  return (
    <label
      className={cn(
        'flex items-center justify-between select-none group',
        disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer',
        className
      )}
    >
      {(label || description) && (
        <div className="flex flex-col pr-4">
          {label && <span className="text-sm font-medium text-label-primary">{label}</span>}
          {description && (
            <span className="text-xs text-label-secondary mt-0.5 leading-relaxed">
              {description}
            </span>
          )}
        </div>
      )}
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={(e) => {
          e.preventDefault()
          if (!disabled) onChange(!checked)
        }}
        className={cn(
          'relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors duration-200 ease-out focus:outline-none border',
          checked
            ? `${activeBg} border-transparent shadow-sm`
            : 'bg-surface-active border-hairline group-hover:border-hairline-strong'
        )}
      >
        <span
          className={cn(
            'inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform duration-200 ease-out shadow-md',
            checked ? 'translate-x-[18px]' : 'translate-x-[3px]'
          )}
        />
      </button>
    </label>
  )
}
