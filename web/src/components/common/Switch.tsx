import React from 'react'
import { cn } from '../../utils/cn'

export interface SwitchProps {
  checked: boolean
  onChange: (checked: boolean) => void
  disabled?: boolean
  label?: string
  description?: string
  color?: 'green' | 'blue' | 'red'
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
  const activeBg = {
    green: 'bg-apple-green',
    blue: 'bg-apple-blue',
    red: 'bg-apple-red',
  }[color]

  return (
    <label
      className={cn(
        'flex items-center justify-between select-none group py-1',
        disabled ? 'opacity-40 cursor-not-allowed' : 'cursor-pointer',
        className
      )}
    >
      {(label || description) && (
        <div className="flex flex-col pr-4">
          {label && <span className="text-sm font-medium text-label-primary tracking-tight">{label}</span>}
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
          'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors duration-200 ease-in-out focus:outline-none p-0.5',
          checked
            ? `${activeBg} shadow-sm`
            : 'bg-[#3A3A3C] hover:bg-[#48484A]'
        )}
      >
        <span
          className={cn(
            'inline-block h-5 w-5 transform rounded-full bg-white transition-transform duration-200 ease-in-out shadow-[0_2px_4px_rgba(0,0,0,0.3)]',
            checked ? 'translate-x-5' : 'translate-x-0'
          )}
        />
      </button>
    </label>
  )
}
