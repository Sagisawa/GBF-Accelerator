import React, { useState, useRef, useEffect, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { Info } from 'lucide-react'
import { cn } from '../../utils/cn'

export interface InfoTipProps {
  text: string
  className?: string
  ariaLabel?: string
}

interface TipCoords {
  left: number
  top: number
  arrowLeft: number
  placement: 'top' | 'bottom'
}

/**
 * Subtle circular (i) button that reveals a floating Liquid Glass popover above
 * the trigger on hover, keyboard focus, or tap — portaled to body so cards
 * never clip it.
 */
export const InfoTip: React.FC<InfoTipProps> = ({
  text,
  className,
  ariaLabel,
}) => {
  const [open, setOpen] = useState(false)
  const [coords, setCoords] = useState<TipCoords | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const popoverRef = useRef<HTMLDivElement>(null)
  const pointerTypeRef = useRef<string>('')

  const updatePosition = useCallback(() => {
    const el = triggerRef.current
    if (!el || typeof window === 'undefined') return
    const rect = el.getBoundingClientRect()
    const tipWidth = 256
    const margin = 12
    const center = rect.left + rect.width / 2
    const clampedLeft = Math.min(
      Math.max(margin, center - tipWidth / 2),
      window.innerWidth - tipWidth - margin
    )
    const arrowLeft = Math.min(Math.max(14, center - clampedLeft), tipWidth - 14)
    const placement: 'top' | 'bottom' = rect.top < 110 ? 'bottom' : 'top'
    const top = placement === 'top' ? rect.top - 10 : rect.bottom + 10

    setCoords({ left: clampedLeft, top, arrowLeft, placement })
  }, [])

  const show = useCallback(() => {
    updatePosition()
    setOpen(true)
  }, [updatePosition])

  const hide = useCallback(() => {
    setOpen(false)
  }, [])

  useEffect(() => {
    if (!open) return
    updatePosition()

    const handleDocClick = (e: MouseEvent) => {
      const target = e.target as Node
      if (
        triggerRef.current?.contains(target) ||
        popoverRef.current?.contains(target)
      ) {
        return
      }
      setOpen(false)
    }
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }

    window.addEventListener('scroll', updatePosition, true)
    window.addEventListener('resize', updatePosition)
    document.addEventListener('mousedown', handleDocClick)
    window.addEventListener('keydown', handleKey)
    return () => {
      window.removeEventListener('scroll', updatePosition, true)
      window.removeEventListener('resize', updatePosition)
      document.removeEventListener('mousedown', handleDocClick)
      window.removeEventListener('keydown', handleKey)
    }
  }, [open, updatePosition])

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={ariaLabel || text}
        onMouseEnter={show}
        onMouseLeave={hide}
        onFocus={show}
        onBlur={hide}
        onPointerDown={(e) => {
          pointerTypeRef.current = e.pointerType || ''
        }}
        onClick={(e) => {
          e.stopPropagation()
          if (pointerTypeRef.current === 'mouse') {
            show()
            return
          }
          if (open) {
            hide()
          } else {
            show()
          }
        }}
        className={cn(
          'inline-flex items-center justify-center w-4 h-4 rounded-full text-label-tertiary hover:text-sky-600 dark:hover:text-sky-400 hover:bg-sky-500/10 focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-500/40 transition-colors shrink-0 cursor-help align-middle',
          className
        )}
      >
        <Info className="w-3.5 h-3.5" strokeWidth={2} />
      </button>

      {open &&
        coords &&
        typeof document !== 'undefined' &&
        createPortal(
          <div
            ref={popoverRef}
            role="tooltip"
            style={{
              left: `${coords.left}px`,
              top: `${coords.top}px`,
              width: '256px',
              transform: coords.placement === 'top' ? 'translateY(-100%)' : 'translateY(0)',
            }}
            className="fixed z-[70] pointer-events-none animate-fade-soft"
          >
            <div className="liquid-popover rounded-xl px-3 py-2 text-xs leading-relaxed text-label-primary font-normal">
              {text}
            </div>
            <div
              style={{ left: `${coords.arrowLeft}px` }}
              className={cn(
                'absolute w-2 h-2 -translate-x-1 rotate-45 bg-white/95 dark:bg-slate-900/95 border-slate-200/80 dark:border-white/15',
                coords.placement === 'top'
                  ? '-bottom-1 border-r border-b'
                  : '-top-1 border-l border-t'
              )}
            />
          </div>,
          document.body
        )}
    </>
  )
}
