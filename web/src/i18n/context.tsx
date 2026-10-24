import React, { createContext, useContext, useState, useCallback, useMemo } from 'react'
import { SupportedLanguage, TranslationSchema } from './types'
import { zh } from './locales/zh'
import { en } from './locales/en'
import { ja } from './locales/ja'
import { applyConfig } from '../api'

const dictionaries: Record<SupportedLanguage, TranslationSchema> = {
  zh,
  en,
  ja,
}

const STORAGE_KEY = 'gbf_language'

export interface I18nContextValue {
  language: SupportedLanguage
  setLanguage: (lang: SupportedLanguage, syncBackend?: boolean) => void
  t: (path: string, params?: Record<string, string | number>) => string
}

function resolveNestedKey(obj: any, path: string): string | undefined {
  if (!obj || typeof obj !== 'object') return undefined
  const parts = path.split('.')
  let current: any = obj
  for (const part of parts) {
    if (current == null || typeof current !== 'object') return undefined
    current = current[part]
  }
  return typeof current === 'string' ? current : undefined
}

function interpolate(text: string, params?: Record<string, string | number>): string {
  if (!params) return text
  return text.replace(/\{(\w+)\}/g, (match, key) => {
    return key in params ? String(params[key]) : match
  })
}

export function translate(
  lang: SupportedLanguage,
  path: string,
  params?: Record<string, string | number>
): string {
  const dict = dictionaries[lang] || zh
  let text = resolveNestedKey(dict, path)
  if (text === undefined && lang !== 'zh') {
    text = resolveNestedKey(zh, path)
  }
  if (text === undefined) {
    return path
  }
  return interpolate(text, params)
}

function getInitialLanguage(): SupportedLanguage {
  if (typeof window !== 'undefined' && window.localStorage) {
    try {
      const stored = window.localStorage.getItem(STORAGE_KEY)
      if (stored === 'zh' || stored === 'en' || stored === 'ja') {
        return stored
      }
    } catch {
      // Ignore localStorage read errors in restricted contexts
    }
  }

  if (typeof navigator !== 'undefined' && navigator.language) {
    const nav = navigator.language.toLowerCase()
    if (nav.startsWith('ja')) return 'ja'
    if (nav.startsWith('en')) return 'en'
  }

  return 'zh'
}

const defaultContextValue: I18nContextValue = {
  language: 'zh',
  setLanguage: () => {},
  t: (path: string, params?: Record<string, string | number>) => translate('zh', path, params),
}

const I18nContext = createContext<I18nContextValue>(defaultContextValue)

export const I18nProvider: React.FC<{
  initialLanguage?: SupportedLanguage
  children: React.ReactNode
}> = ({ initialLanguage, children }) => {
  const [language, setLanguageState] = useState<SupportedLanguage>(() => {
    return initialLanguage || getInitialLanguage()
  })

  const setLanguage = useCallback((lang: SupportedLanguage, syncBackend = true) => {
    if (lang !== 'zh' && lang !== 'en' && lang !== 'ja') return
    setLanguageState(lang)
    if (typeof window !== 'undefined' && window.localStorage) {
      try {
        window.localStorage.setItem(STORAGE_KEY, lang)
      } catch {
        // Ignore
      }
    }
    if (syncBackend) {
      applyConfig({ language: lang }).catch(() => {})
    }
  }, [])

  const t = useCallback(
    (path: string, params?: Record<string, string | number>) => {
      return translate(language, path, params)
    },
    [language]
  )

  const value = useMemo(
    () => ({
      language,
      setLanguage,
      t,
    }),
    [language, setLanguage, t]
  )

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n(): I18nContextValue {
  try {
    const dispatcher = (React as any)?.__SECRET_INTERNALS_DO_NOT_USE_OR_YOU_WILL_BE_FIRED?.ReactCurrentDispatcher?.current
    if (dispatcher && typeof dispatcher.useContext === 'function') {
      const ctx = useContext(I18nContext)
      if (ctx) return ctx
    }
  } catch {
    // In test harnesses or environments without ReactCurrentDispatcher
  }
  return defaultContextValue
}
