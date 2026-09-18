import { useEffect } from 'react'

export interface ShortcutHandlers {
  onToggleProxy?: () => void
  onToggleLogs?: () => void
  onToggleDirect?: () => void
  onOpenClearModal?: () => void
  onOpenShortcutsModal?: () => void
  onCloseAll?: () => void
}

export function useKeyboardShortcuts(handlers: ShortcutHandlers) {
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Input / TextArea guard: Never trigger single-key actions while user is typing
      const target = e.target as HTMLElement | null
      if (
        target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.isContentEditable)
      ) {
        // Only allow Esc inside inputs to blur
        if (e.key === 'Escape') {
          target.blur()
          handlers.onCloseAll?.()
        }
        return
      }

      // Do not hijack standard modifier combos like Cmd+C, Ctrl+C, Ctrl+L, Alt+F4
      if (e.metaKey || e.ctrlKey || e.altKey) {
        return
      }

      switch (e.key) {
        case ' ':
          e.preventDefault()
          handlers.onToggleProxy?.()
          break

        case 'l':
        case 'L':
          e.preventDefault()
          handlers.onToggleLogs?.()
          break

        case 'd':
        case 'D':
          e.preventDefault()
          handlers.onToggleDirect?.()
          break

        case 'c':
        case 'C':
          e.preventDefault()
          handlers.onOpenClearModal?.()
          break

        case '?':
          e.preventDefault()
          handlers.onOpenShortcutsModal?.()
          break

        case 'Escape':
          e.preventDefault()
          handlers.onCloseAll?.()
          break

        default:
          break
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [handlers])
}
