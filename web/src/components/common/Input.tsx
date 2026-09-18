import React, { forwardRef } from 'react'
import { cn } from '../../utils/cn'

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  mono?: boolean
  leftIcon?: React.ReactNode
  rightElement?: React.ReactNode
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, mono = false, leftIcon, rightElement, disabled, ...props }, ref) => {
    return (
      <div className="relative flex items-center w-full">
        {leftIcon && (
          <div className="absolute left-3 flex items-center pointer-events-none text-label-tertiary">
            {leftIcon}
          </div>
        )}
        <input
          ref={ref}
          disabled={disabled}
          className={cn(
            'w-full bg-surface-subtle text-label-primary placeholder-label-tertiary rounded-lg border border-hairline px-3 py-2 text-sm transition-all duration-150 ease-out shadow-inner',
            'focus:outline-none focus:border-sys-blue/50 focus:ring-1 focus:ring-sys-blue/30 focus:bg-surface',
            'hover:border-hairline-strong',
            'disabled:opacity-40 disabled:cursor-not-allowed',
            mono && 'font-mono text-[13px]',
            leftIcon && 'pl-9',
            rightElement && 'pr-20',
            className
          )}
          {...props}
        />
        {rightElement && (
          <div className="absolute right-2 flex items-center gap-1.5">
            {rightElement}
          </div>
        )}
      </div>
    )
  }
)

Input.displayName = 'Input'
