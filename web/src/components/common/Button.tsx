import React from 'react'
import { cn } from '../../utils/cn'
import { Loader2 } from 'lucide-react'

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'success'
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
    'inline-flex items-center justify-center font-medium rounded-lg select-none transition-all duration-100 ease-out focus:outline-none disabled:opacity-40 disabled:pointer-events-none active:scale-[0.985]'

  const variantClasses = {
    primary:
      'bg-sys-blue text-white hover:bg-sys-blue/90 shadow-specular active:shadow-specular-active',
    secondary:
      'bg-surface hover:bg-surface-elevated text-label-primary border border-hairline shadow-specular active:shadow-specular-active hover:border-hairline-strong',
    danger:
      'bg-sys-red text-white hover:bg-sys-red/90 shadow-specular active:shadow-specular-active',
    ghost:
      'bg-transparent hover:bg-surface text-label-secondary hover:text-label-primary',
    success:
      'bg-sys-green text-black font-semibold hover:bg-sys-green/90 shadow-specular active:shadow-specular-active',
  }[variant]

  const sizeClasses = {
    xs: 'text-xs px-2 py-1 gap-1.5',
    sm: 'text-xs px-2.5 py-1.5 gap-1.5',
    md: 'text-sm px-3.5 py-2 gap-2',
    lg: 'text-sm px-4 py-2.5 gap-2.5 font-medium',
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
