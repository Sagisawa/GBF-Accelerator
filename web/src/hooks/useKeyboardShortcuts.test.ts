import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useKeyboardShortcuts } from './useKeyboardShortcuts'

let listeners: Record<string, ((e: any) => void)[]> = {}
let cleanups: (() => void)[] = []

vi.mock('react', () => {
  return {
    useRef: (val: any) => ({ current: val }),
    useEffect: (fn: () => any, deps?: any[]) => {
      // If disabled is true in deps, don't run
      if (deps && deps[0] === true) {
        return
      }
      const cleanup = fn()
      if (typeof cleanup === 'function') {
        cleanups.push(cleanup)
      }
    },
  }
})

describe('useKeyboardShortcuts', () => {
  beforeEach(() => {
    listeners = {}
    cleanups.forEach((c) => c())
    cleanups = []

    const mockWindow = {
      addEventListener: vi.fn((event: string, handler: (e: any) => void) => {
        listeners[event] = listeners[event] || []
        listeners[event].push(handler)
      }),
      removeEventListener: vi.fn((event: string, handler: (e: any) => void) => {
        if (listeners[event]) {
          listeners[event] = listeners[event].filter((h) => h !== handler)
        }
      }),
    }
    ;(globalThis as any).window = mockWindow
  })

  const dispatchKeydown = (eventInit: Partial<KeyboardEvent>) => {
    const event = {
      key: '',
      code: '',
      isComposing: false,
      keyCode: 0,
      metaKey: false,
      ctrlKey: false,
      altKey: false,
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
      target: { tagName: 'DIV' },
      ...eventInit,
    }
    const handlers = listeners['keydown'] || []
    handlers.forEach((h) => h(event as any))
    return event
  }

  it('triggers onToggleProxy when Space is pressed in normal dashboard mode', () => {
    const onToggleProxy = vi.fn()
    useKeyboardShortcuts({ onToggleProxy })

    const event = dispatchKeydown({ key: ' ', code: 'Space' })
    expect(onToggleProxy).toHaveBeenCalledTimes(1)
    expect(event.preventDefault).toHaveBeenCalled()
  })

  it('does NOT trigger onToggleProxy when disabled is true (e.g. in standalone logs window)', () => {
    const onToggleProxy = vi.fn()
    useKeyboardShortcuts({ onToggleProxy, disabled: true })

    const event = dispatchKeydown({ key: ' ', code: 'Space' })
    expect(onToggleProxy).not.toHaveBeenCalled()
    expect(event.preventDefault).not.toHaveBeenCalled()
  })

  it('does NOT trigger onToggleProxy when a modal is open', () => {
    const onToggleProxy = vi.fn()
    useKeyboardShortcuts({ onToggleProxy, isModalOpen: true })

    const event = dispatchKeydown({ key: ' ', code: 'Space' })
    expect(onToggleProxy).not.toHaveBeenCalled()
    expect(event.preventDefault).not.toHaveBeenCalled()
  })

  it('does NOT trigger onToggleProxy when focus is inside an input or textarea', () => {
    const onToggleProxy = vi.fn()
    useKeyboardShortcuts({ onToggleProxy })

    dispatchKeydown({
      key: ' ',
      code: 'Space',
      target: { tagName: 'INPUT' } as any,
    })
    expect(onToggleProxy).not.toHaveBeenCalled()
  })

  it('does NOT trigger onToggleProxy when focus is on a button', () => {
    const onToggleProxy = vi.fn()
    useKeyboardShortcuts({ onToggleProxy })

    dispatchKeydown({
      key: ' ',
      code: 'Space',
      target: { tagName: 'BUTTON', closest: () => null } as any,
    })
    expect(onToggleProxy).not.toHaveBeenCalled()
  })

  it('does NOT trigger any shortcut when disabled is true (standalone logs mode)', () => {
    const onToggleProxy = vi.fn()
    const onToggleLogs = vi.fn()
    const onToggleDirect = vi.fn()
    const onOpenClearModal = vi.fn()
    const onOpenShortcutsModal = vi.fn()
    const onCloseAll = vi.fn()

    useKeyboardShortcuts({
      onToggleProxy,
      onToggleLogs,
      onToggleDirect,
      onOpenClearModal,
      onOpenShortcutsModal,
      onCloseAll,
      disabled: true,
    })

    dispatchKeydown({ key: ' ', code: 'Space' })
    dispatchKeydown({ key: 'l' })
    dispatchKeydown({ key: 'L' })
    dispatchKeydown({ key: 'd' })
    dispatchKeydown({ key: 'D' })
    dispatchKeydown({ key: 'c' })
    dispatchKeydown({ key: 'C' })
    dispatchKeydown({ key: '?' })
    dispatchKeydown({ key: 'Escape' })

    expect(onToggleProxy).not.toHaveBeenCalled()
    expect(onToggleLogs).not.toHaveBeenCalled()
    expect(onToggleDirect).not.toHaveBeenCalled()
    expect(onOpenClearModal).not.toHaveBeenCalled()
    expect(onOpenShortcutsModal).not.toHaveBeenCalled()
    expect(onCloseAll).not.toHaveBeenCalled()
  })

  it('triggers onCloseAll when Escape is pressed while modal is open', () => {
    const onCloseAll = vi.fn()
    useKeyboardShortcuts({ onCloseAll, isModalOpen: true })

    const event = dispatchKeydown({ key: 'Escape' })
    expect(onCloseAll).toHaveBeenCalledTimes(1)
    expect(event.preventDefault).toHaveBeenCalled()
  })
})
