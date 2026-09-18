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
            'w-full bg-black/30 text-label-primary placeholder-label-tertiary rounded-xl border border-white/[0.08] px-3.5 py-2 text-sm transition-all duration-150 ease-out',
            'focus:outline-none focus:border-apple-red/60 focus:ring-2 focus:ring-apple-red/20 focus:bg-black/50',
            'hover:border-white/[0.14]',
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
