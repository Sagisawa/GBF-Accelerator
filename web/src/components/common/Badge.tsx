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
      bg: 'bg-sys-greenBg text-sys-green border-sys-green/20',
      dot: 'bg-sys-green',
    },
    blue: {
      bg: 'bg-sys-blueBg text-sys-blue border-sys-blue/20',
      dot: 'bg-sys-blue',
    },
    amber: {
      bg: 'bg-sys-amberBg text-sys-amber border-sys-amber/20',
      dot: 'bg-sys-amber',
    },
    red: {
      bg: 'bg-sys-redBg text-sys-red border-sys-red/20',
      dot: 'bg-sys-red',
    },
    neutral: {
      bg: 'bg-surface-active text-label-secondary border-hairline',
      dot: 'bg-label-tertiary',
    },
  }[variant]

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium border leading-none select-none',
        variantStyles.bg,
        mono && 'font-mono text-[11px]',
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
