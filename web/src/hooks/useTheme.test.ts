import { describe, it, expect, vi, beforeEach } from 'vitest'
import { useTheme } from './useTheme'
import * as api from '../api'

vi.mock('../api', () => ({
  applyConfig: vi.fn().mockResolvedValue({ ok: true }),
}))

// Mock React useState, useEffect, useCallback
let stateStore: Record<string, any> = {}
let effectCleanups: (() => void)[] = []

vi.mock('react', () => ({
  useState: (initial: any) => {
    const val = typeof initial === 'function' ? initial() : initial
    const key = 'theme_state'
    if (!(key in stateStore)) {
      stateStore[key] = val
    }
    const setter = (next: any) => {
      const resolved = typeof next === 'function' ? next(stateStore[key]) : next
      stateStore[key] = resolved
    }
    return [stateStore[key], setter]
  },
  useEffect: (fn: () => any) => {
    const cleanup = fn()
    if (typeof cleanup === 'function') {
      effectCleanups.push(cleanup)
    }
  },
  useCallback: (fn: any) => fn,
}))

describe('useTheme hook', () => {
  let localStorageMock: Record<string, string> = {}
  let classListSet: Set<string> = new Set()

  beforeEach(() => {
    localStorageMock = {}
    classListSet = new Set()
    stateStore = {}
    effectCleanups = []
    vi.clearAllMocks()

    const mockStorage = {
      getItem: vi.fn((key: string) => localStorageMock[key] ?? null),
      setItem: vi.fn((key: string, val: string) => {
        localStorageMock[key] = String(val)
      }),
    }

    const mockDoc = {
      documentElement: {
        classList: {
          add: vi.fn((c: string) => classListSet.add(c)),
          remove: vi.fn((c: string) => classListSet.delete(c)),
          contains: vi.fn((c: string) => classListSet.has(c)),
        },
      },
    }

    ;(globalThis as any).window = {
      localStorage: mockStorage,
    }
    ;(globalThis as any).document = mockDoc
  })

  it('defaults to light theme when no stored theme is found', () => {
    const { theme, isDark } = useTheme()
    expect(theme).toBe('light')
    expect(isDark).toBe(false)
  })

  it('loads dark theme if stored in localStorage', () => {
    localStorageMock['gbf_theme'] = 'dark'
    const { theme, isDark } = useTheme()
    expect(theme).toBe('dark')
    expect(isDark).toBe(true)
  })

  it('updates DOM class and localStorage when setTheme is called', () => {
    const { setTheme } = useTheme()
    setTheme('dark')

    expect(classListSet.has('dark')).toBe(true)
    expect((globalThis as any).window.localStorage.setItem).toHaveBeenCalledWith('gbf_theme', 'dark')
    expect(api.applyConfig).toHaveBeenCalledWith({ theme: 'dark' })

    setTheme('light')
    expect(classListSet.has('dark')).toBe(false)
    expect((globalThis as any).window.localStorage.setItem).toHaveBeenCalledWith('gbf_theme', 'light')
    expect(api.applyConfig).toHaveBeenCalledWith({ theme: 'light' })
  })

  it('does not sync backend when syncBackend is false', () => {
    const { setTheme } = useTheme()
    setTheme('dark', false)

    expect(classListSet.has('dark')).toBe(true)
    expect(api.applyConfig).not.toHaveBeenCalled()
  })
})
