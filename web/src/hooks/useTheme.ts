import { useState, useEffect, useCallback } from 'react'
import { applyConfig } from '../api'

export type Theme = 'light' | 'dark'

const STORAGE_KEY = 'gbf_theme'

export interface UseThemeReturn {
  theme: Theme
  isDark: boolean
  setTheme: (theme: Theme, syncBackend?: boolean) => void
  toggleTheme: () => void
}

function getInitialTheme(): Theme {
  if (typeof window !== 'undefined' && window.localStorage) {
    try {
      const stored = window.localStorage.getItem(STORAGE_KEY)
      if (stored === 'dark') {
        return 'dark'
      }
    } catch {
      // Ignore localStorage read errors in restricted contexts
    }
  }
  return 'light' // Default is Day / Light
}

export function useTheme(initialTheme?: Theme): UseThemeReturn {
  const [theme, setThemeState] = useState<Theme>(() => initialTheme || getInitialTheme())

  const applyDomTheme = useCallback((targetTheme: Theme) => {
    if (typeof document !== 'undefined') {
      const root = document.documentElement
      if (targetTheme === 'dark') {
        root.classList.add('dark')
      } else {
        root.classList.remove('dark')
      }
    }
  }, [])

  const setTheme = useCallback(
    (newTheme: Theme, syncBackend = true) => {
      if (newTheme !== 'light' && newTheme !== 'dark') return
      setThemeState(newTheme)
      applyDomTheme(newTheme)

      if (typeof window !== 'undefined' && window.localStorage) {
        try {
          window.localStorage.setItem(STORAGE_KEY, newTheme)
        } catch {
          // Ignore
        }
      }

      if (syncBackend) {
        applyConfig({ theme: newTheme }).catch(() => {})
      }
    },
    [applyDomTheme]
  )

  const toggleTheme = useCallback(() => {
    setThemeState((prev) => {
      const next: Theme = prev === 'dark' ? 'light' : 'dark'
      applyDomTheme(next)
      if (typeof window !== 'undefined' && window.localStorage) {
        try {
          window.localStorage.setItem(STORAGE_KEY, next)
        } catch {
          // Ignore
        }
      }
      applyConfig({ theme: next }).catch(() => {})
      return next
    })
  }, [applyDomTheme])

  // Sync with initialTheme prop if it changes (e.g. loaded asynchronously from backend config)
  useEffect(() => {
    if (initialTheme && (initialTheme === 'light' || initialTheme === 'dark')) {
      setThemeState(initialTheme)
      applyDomTheme(initialTheme)
      if (typeof window !== 'undefined' && window.localStorage) {
        try {
          window.localStorage.setItem(STORAGE_KEY, initialTheme)
        } catch {
          // Ignore
        }
      }
    }
  }, [initialTheme, applyDomTheme])

  return {
    theme,
    isDark: theme === 'dark',
    setTheme,
    toggleTheme,
  }
}
