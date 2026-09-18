import React from 'react'
import { cn } from '../../utils/cn'
import { Loader2 } from 'lucide-react'

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'success' | 'apple'
  size?: 'xs' | 'sm' | 'md' | 'lg'
  loading?: boolean
  icon?: React.ReactNode
  shortcut?: string
}

export const Button: React.FC<ButtonProps> = ({
  children,
  className,
  variant = 'secondary',
  size = 'md',
  loading = false,
  disabled,
  icon,
  shortcut,
  ...props
}) => {
  const baseClasses =
    'inline-flex items-center justify-center font-medium rounded-full select-none transition-all duration-150 ease-out focus:outline-none disabled:opacity-40 disabled:pointer-events-none active:scale-[0.96] tracking-tight'

  const variantClasses = {
    primary:
      'bg-white text-black font-semibold hover:bg-white/90 shadow-sm active:bg-white/80',
    apple:
      'bg-apple-red text-white font-semibold hover:bg-apple-redHover shadow-apple active:scale-[0.96]',
    secondary:
      'bg-white/[0.08] hover:bg-white/[0.14] text-white border border-white/[0.08] active:bg-white/[0.06]',
    danger:
      'bg-apple-red text-white font-medium hover:bg-apple-redHover shadow-apple active:scale-[0.96]',
    ghost:
      'bg-transparent hover:bg-white/[0.08] text-label-secondary hover:text-white active:bg-white/[0.04]',
    success:
      'bg-apple-green text-white font-semibold hover:bg-apple-green/90 shadow-sm active:scale-[0.96]',
  }[variant]

  const sizeClasses = {
    xs: 'text-[11px] px-2.5 py-1 gap-1.5',
    sm: 'text-xs px-3.5 py-1.5 gap-1.5',
    md: 'text-xs px-4 py-2 gap-2 font-medium',
    lg: 'text-sm px-5 py-2.5 gap-2.5 font-semibold',
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
        <kbd className="ml-1 px-1.5 py-0.5 text-[10px] uppercase font-mono rounded bg-white/10 text-label-tertiary border border-white/5 leading-none">
          {shortcut}
        </kbd>
      )}
    </button>
  )
}
