import { useEffect, useRef } from 'react'

export interface ShortcutHandlers {
  onToggleProxy?: () => void
  onToggleLogs?: () => void
  onToggleDirect?: () => void
  onOpenClearModal?: () => void
  onOpenShortcutsModal?: () => void
  onCloseAll?: () => void
  isModalOpen?: boolean
}

export function useKeyboardShortcuts(handlers: ShortcutHandlers) {
  const handlersRef = useRef(handlers)
  handlersRef.current = handlers

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // 1. IME composition guard: Never intercept IME candidate selection (Space/Enter/numbers)
      if (e.isComposing || e.keyCode === 229) {
        return
      }

      // 2. Input / Textarea / Select / ContentEditable guard
      const target = e.target as HTMLElement | null
      if (
        target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.tagName === 'SELECT' ||
          target.isContentEditable)
      ) {
        // Only allow Esc inside inputs to blur and close
        if (e.key === 'Escape') {
          target.blur()
          handlersRef.current.onCloseAll?.()
        }
        return
      }

      // 3. Button guard: Allow native button click with Space
      if (target && (target.tagName === 'BUTTON' || target.closest('button'))) {
        if (e.key === ' ') {
          return
        }
      }

      // 4. Do not hijack standard browser / OS modifier combos like Cmd+C, Ctrl+C, Ctrl+L, Alt+F4
      if (e.metaKey || e.ctrlKey || e.altKey) {
        return
      }

      // 5. If a modal or drawer is open:
      // Only allow Esc (or L if toggling logs drawer)
      const isModalOpen = handlersRef.current.isModalOpen
      if (isModalOpen) {
        if (e.key === 'Escape') {
          e.preventDefault()
          handlersRef.current.onCloseAll?.()
        } else if (e.key === 'l' || e.key === 'L') {
          e.preventDefault()
          handlersRef.current.onToggleLogs?.()
        }
        return
      }

      switch (e.key) {
        case ' ':
          e.preventDefault()
          handlersRef.current.onToggleProxy?.()
          break

        case 'l':
        case 'L':
          e.preventDefault()
          handlersRef.current.onToggleLogs?.()
          break

        case 'd':
        case 'D':
          e.preventDefault()
          handlersRef.current.onToggleDirect?.()
          break

        case 'c':
        case 'C':
          e.preventDefault()
          handlersRef.current.onOpenClearModal?.()
          break

        case '?':
          e.preventDefault()
          handlersRef.current.onOpenShortcutsModal?.()
          break

        case 'Escape':
          e.preventDefault()
          handlersRef.current.onCloseAll?.()
          break

        default:
          break
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [])
}

