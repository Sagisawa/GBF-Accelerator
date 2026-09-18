import React, { useEffect } from 'react'
import { cn } from '../../utils/cn'
import { X } from 'lucide-react'

export interface DrawerProps {
  isOpen: boolean
  onClose: () => void
  title: React.ReactNode
  subtitle?: string
  children: React.ReactNode
  height?: string
  className?: string
  headerRight?: React.ReactNode
}

export const Drawer: React.FC<DrawerProps> = ({
  isOpen,
  onClose,
  title,
  subtitle,
  children,
  height = 'h-[75vh]',
  className,
  headerRight,
}) => {
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
    <div className="fixed inset-0 z-50 flex flex-col justify-end">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/60 backdrop-blur-sm transition-opacity duration-200 animate-in fade-in"
        onClick={onClose}
      />

      {/* Drawer Card */}
      <div
        className={cn(
          'relative w-full max-w-5xl mx-auto bg-surface-elevated border-t border-x border-hairline-strong rounded-t-2xl shadow-2xl flex flex-col overflow-hidden transition-transform duration-200 ease-out animate-in slide-in-from-bottom',
          height,
          className
        )}
      >
        {/* Drawer Drag Indicator */}
        <div className="w-full flex justify-center pt-2.5 pb-1 cursor-pointer" onClick={onClose}>
          <div className="w-10 h-1 rounded-full bg-white/20 hover:bg-white/40 transition-colors" />
        </div>

        {/* Drawer Header */}
        <div className="px-6 py-3 border-b border-hairline flex items-center justify-between shrink-0 bg-surface/50">
          <div className="flex items-center gap-3">
            <div>
              <div className="text-sm font-semibold text-label-primary flex items-center gap-2">
                {title}
              </div>
              {subtitle && (
                <div className="text-xs text-label-secondary mt-0.5">{subtitle}</div>
              )}
            </div>
          </div>

          <div className="flex items-center gap-3">
            {headerRight}
            <button
              onClick={onClose}
              className="p-1.5 rounded-lg text-label-tertiary hover:text-label-primary hover:bg-surface-active transition-colors"
              title="关闭 (Esc)"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>

        {/* Drawer Content */}
        <div className="flex-1 overflow-hidden p-6 flex flex-col">{children}</div>
      </div>
    </div>
  )
}
