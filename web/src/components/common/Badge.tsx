import React from 'react'
import { cn } from '../../utils/cn'

export interface BadgeProps {
  children: React.ReactNode
  variant?: 'green' | 'blue' | 'amber' | 'red' | 'neutral'
  dot?: boolean
  pulse?: boolean
  className?: string
  mono?: boolean
}

export const Badge: React.FC<BadgeProps> = ({
  children,
  variant = 'neutral',
  dot = false,
  pulse = false,
  className,
  mono = false,
}) => {
  const variantStyles = {
    green: {
      bg: 'bg-sys-greenBg text-sys-green border-emerald-500/20',
      dot: 'bg-emerald-500',
    },
    blue: {
      bg: 'bg-sys-blueBg text-sys-blue border-sky-500/20',
      dot: 'bg-sky-500',
    },
    amber: {
      bg: 'bg-sys-amberBg text-sys-amber border-amber-500/25',
      dot: 'bg-amber-500',
    },
    red: {
      bg: 'bg-sys-redBg text-sys-red border-rose-500/20',
      dot: 'bg-rose-500',
    },
    neutral: {
      bg: 'bg-surface-active text-label-secondary border-hairline',
      dot: 'bg-slate-400',
    },
  }[variant]

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[11px] font-semibold border leading-none select-none whitespace-nowrap',
        variantStyles.bg,
        mono && 'font-mono',
        className
      )}
    >
      {dot && (
        <span
          className={cn(
            'w-1.5 h-1.5 rounded-full shrink-0',
            variantStyles.dot,
            pulse && 'animate-pulse'
          )}
        />
      )}
      <span>{children}</span>
    </span>
  )
}
