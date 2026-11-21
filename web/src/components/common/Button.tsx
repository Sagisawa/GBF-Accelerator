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
    'inline-flex items-center justify-center font-medium rounded-lg select-none transition-[background-color,border-color,color,transform] duration-150 ease-out focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-500/40 focus-visible:ring-offset-1 disabled:opacity-50 disabled:pointer-events-none active:scale-[0.98]'

  const variantClasses = {
    desktop: 'ui-control',
    secondary: 'ui-control',
    primary:
      'bg-sky-600 hover:bg-sky-700 active:bg-sky-800 text-white border border-sky-700/20 shadow-[inset_0_1px_0_rgba(255,255,255,0.18)]',
    danger:
      'bg-rose-600 hover:bg-rose-700 active:bg-rose-800 text-white font-semibold border border-rose-700/20 shadow-[inset_0_1px_0_rgba(255,255,255,0.16)]',
    success:
      'bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white font-semibold border border-emerald-700/20 shadow-[inset_0_1px_0_rgba(255,255,255,0.16)]',
    ghost:
      'bg-transparent hover:bg-slate-500/10 active:bg-slate-500/15 text-slate-600 dark:text-slate-200',
    apple:
      'bg-sky-600 hover:bg-sky-700 text-white font-medium border border-sky-700/20 shadow-[inset_0_1px_0_rgba(255,255,255,0.18)]',
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
        <kbd className="ml-1 px-1 py-px text-[10px] uppercase font-mono rounded bg-slate-200/80 text-slate-600 border border-slate-300 leading-none">
          {shortcut}
        </kbd>
      )}
    </button>
  )
}
