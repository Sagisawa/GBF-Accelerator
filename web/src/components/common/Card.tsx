import React from 'react'
import { cn } from '../../utils/cn'

export interface CardProps {
  children: React.ReactNode
  className?: string
}

/**
 * Primary dashboard card surface: 20px concentric outer radius with frosted semi-solid material.
 */
export const Card: React.FC<CardProps> = ({ children, className }) => {
  return (
    <section
      className={cn(
        'frosted-card rounded-[20px] p-4 sm:p-5 flex flex-col justify-between gap-3.5 transition-[border-color,box-shadow] duration-200',
        className
      )}
    >
      {children}
    </section>
  )
}

export interface CardHeaderProps {
  icon?: React.ReactNode
  title: React.ReactNode
  badge?: React.ReactNode
  className?: string
}

export const CardHeader: React.FC<CardHeaderProps> = ({
  icon,
  title,
  badge,
  className,
}) => {
  return (
    <div className={cn('flex flex-wrap items-center justify-between gap-x-2 gap-y-1.5 pb-2.5 hairline-b', className)}>
      <div className="flex items-center gap-2 min-w-0">
        {icon && <span className="shrink-0 text-label-secondary">{icon}</span>}
        <h2 className="text-sm sm:text-[15px] font-bold text-label-primary tracking-tight leading-snug">
          {title}
        </h2>
      </div>
      {badge && <div className="shrink-0">{badge}</div>}
    </div>
  )
}

export interface InsetPanelProps {
  children: React.ReactNode
  className?: string
  tone?: 'neutral' | 'sky' | 'amber' | 'rose'
}

/**
 * 12px concentric sub-panel nested inside a 20px Card.
 */
export const InsetPanel: React.FC<InsetPanelProps> = ({
  children,
  className,
  tone = 'neutral',
}) => {
  const toneClasses = {
    neutral: 'inset-panel',
    sky: 'bg-sky-50/75 border border-sky-200/70 text-sky-900',
    amber: 'bg-amber-50/70 border border-amber-200/70 text-amber-900',
    rose: 'bg-rose-50/80 border border-rose-200/80 text-rose-900',
  }[tone]

  return (
    <div className={cn('rounded-xl p-3', toneClasses, className)}>
      {children}
    </div>
  )
}

export interface CompoundInputGroupProps {
  children: React.ReactNode
  className?: string
}

/**
 * Unified 12px field shell wrapping an input and trailing inline actions (8px inner buttons).
 */
export const CompoundInputGroup: React.FC<CompoundInputGroupProps> = ({
  children,
  className,
}) => {
  return (
    <div
      className={cn(
        'ui-field flex items-center gap-1 rounded-xl p-1 shadow-[inset_0_1px_2px_rgba(15,23,42,0.03)]',
        className
      )}
    >
      {children}
    </div>
  )
}
