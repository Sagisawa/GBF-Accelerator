import React, { useEffect, useId } from 'react'
import { cn } from '../../utils/cn'
import { X } from 'lucide-react'
import { useI18n } from '../../i18n'

export interface ModalProps {
  isOpen: boolean
  onClose: () => void
  title: React.ReactNode
  subtitle?: string
  children: React.ReactNode
  maxWidth?: string
  className?: string
}

export const Modal: React.FC<ModalProps> = ({
  isOpen,
  onClose,
  title,
  subtitle,
  children,
  maxWidth = 'max-w-md',
  className,
}) => {
  const { t } = useI18n()
  const titleId = useId()

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose()
      }
    }
    if (isOpen) {
      window.addEventListener('keydown', handleKeyDown)
    }
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [isOpen, onClose])

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4 overflow-hidden">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-slate-950/35 backdrop-blur-sm animate-fade-soft"
        onClick={onClose}
      />

      {/* Liquid Glass Modal Dialog (20px outer radius) */}
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className={cn(
          'relative w-full max-h-[calc(100vh-2rem)] flex flex-col liquid-glass rounded-[20px] overflow-hidden z-10 animate-modal-in',
          maxWidth,
          className
        )}
      >
        {/* Header */}
        <div className="px-5 py-3.5 hairline-b bg-white/40 dark:bg-white/[0.03] flex items-center justify-between gap-3">
          <div className="min-w-0">
            <h3 id={titleId} className="text-sm font-bold text-label-primary tracking-tight">
              {title}
            </h3>
            {subtitle && (
              <p className="text-xs text-label-tertiary mt-0.5">{subtitle}</p>
            )}
          </div>
          <button
            type="button"
            onClick={onClose}
            className="w-7 h-7 rounded-lg hover:bg-slate-500/15 flex items-center justify-center text-label-tertiary hover:text-label-primary transition-colors shrink-0 cursor-pointer"
            title={t('common.closeEsc')}
            aria-label={t('common.closeDialog')}
          >
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* Body (semi-solid inner surface for high legibility) */}
        <div className="flex-1 min-h-0 overflow-y-auto p-5 bg-white/85 dark:bg-slate-900/70 text-label-primary">
          {children}
        </div>
      </div>
    </div>
  )
}
