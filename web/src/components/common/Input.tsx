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
            'ui-input w-full rounded-lg px-3 py-1.5 text-sm',
            mono && 'font-mono text-[13px]',
            leftIcon && 'pl-9',
            rightElement && 'pr-20',
            className
          )}
          {...props}
        />
        {rightElement && (
          <div className="absolute right-1.5 flex items-center gap-1.5">
            {rightElement}
          </div>
        )}
      </div>
    )
  }
)

Input.displayName = 'Input'
