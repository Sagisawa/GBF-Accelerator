import React from 'react'
import { cn } from '../../utils/cn'
import { Loader2 } from 'lucide-react'

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'success' | 'apple' | 'desktop'
  size?: 'xs' | 'sm' | 'md' | 'lg'
  loading?: boolean
  icon?: React.ReactNode
  shortcut?: string
}

export const Button: React.FC<ButtonProps> = ({
  children,
  className,
  variant = 'desktop',
  size = 'sm',
  loading = false,
  disabled,
  icon,
  shortcut,
  ...props
}) => {
  const baseClasses =
    'inline-flex items-center justify-center font-medium rounded select-none transition-colors duration-150 ease-out focus:outline-none disabled:opacity-50 disabled:pointer-events-none active:scale-[0.98]'

  const variantClasses = {
    desktop:
      'bg-[#f8f9fa] hover:bg-[#e2e6ea] active:bg-[#dae0e5] text-slate-700 border border-slate-300 shadow-xs',
    primary:
      'bg-blue-600 hover:bg-blue-700 active:bg-blue-800 text-white shadow-xs',
    secondary:
      'bg-slate-100 hover:bg-slate-200 active:bg-slate-300 text-slate-700 border border-slate-300 shadow-xs',
    danger:
      'bg-[#dc3545] hover:bg-[#c82333] active:bg-[#bd2130] text-white font-bold shadow-xs',
    success:
      'bg-[#28a745] hover:bg-[#218838] active:bg-[#1e7e34] text-white font-bold shadow-xs',
    ghost:
      'bg-transparent hover:bg-slate-100 active:bg-slate-200 text-slate-600',
    apple:
      'bg-blue-600 hover:bg-blue-700 text-white font-medium shadow-xs',
  }[variant]

  const sizeClasses = {
    xs: 'text-[11px] px-2 py-0.5 gap-1',
    sm: 'text-xs px-2.5 py-1 gap-1.5',
    md: 'text-xs px-3.5 py-1.5 gap-1.5',
    lg: 'text-sm px-5 py-2 gap-2 font-semibold',
  }[size]

  return (
    <button
      className={cn(baseClasses, variantClasses, sizeClasses, className)}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? (
        <Loader2 className="w-3.5 h-3.5 animate-spin" />
      ) : (
        icon && <span className="shrink-0">{icon}</span>
      )}
      <span>{children}</span>
      {shortcut && (
        <kbd className="ml-1 px-1 py-0.2 text-[10px] uppercase font-mono rounded bg-slate-200/80 text-slate-600 border border-slate-300 leading-none">
          {shortcut}
        </kbd>
      )}
    </button>
  )
}

