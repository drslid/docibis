// Interface language (zustand), kept in localStorage and on the account.
// French is the default and the fallback of every missing text.
import { useCallback } from 'react'
import { create } from 'zustand'
import { dictionaries, type Key, type Locale } from './translations'

export type { Key, Locale } from './translations'
export const LOCALES: Locale[] = ['fr', 'en', 'de', 'it']
const STORAGE_KEY = 'docibis_locale'

export function isLocale(value: unknown): value is Locale {
  return LOCALES.includes(value as Locale)
}

function storedLocale(): Locale | null {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    return isLocale(stored) ? stored : null
  } catch {
    return null
  }
}

interface I18nState {
  locale: Locale
  /** True once chosen on this browser: the account language then does not override it. */
  chosen: boolean
  setLocale: (locale: Locale, remember?: boolean) => void
}

export const useI18nStore = create<I18nState>((set) => ({
  locale: storedLocale() ?? 'fr',
  chosen: storedLocale() !== null,
  setLocale: (locale, remember = true) => {
    if (remember) {
      try {
        localStorage.setItem(STORAGE_KEY, locale)
      } catch {
        // The choice lasts until the page is closed.
      }
    }
    document.documentElement.lang = locale
    set((s) => ({ locale, chosen: s.chosen || remember }))
  },
}))

export type Vars = Record<string, string | number>

export function translate(locale: Locale, key: Key, vars?: Vars): string {
  let text: string = dictionaries[locale][key] ?? dictionaries.fr[key] ?? key
  if (vars) {
    for (const [name, value] of Object.entries(vars)) text = text.replaceAll(`{${name}}`, String(value))
  }
  return text
}

/** Language tag for dates and numbers. */
export function tag(locale: Locale): string {
  return locale === 'en' ? 'en-GB' : `${locale}-${locale.toUpperCase()}`
}

export function useI18n() {
  const locale = useI18nStore((s) => s.locale)
  const setLocale = useI18nStore((s) => s.setLocale)
  const t = useCallback((key: Key, vars?: Vars) => translate(locale, key, vars), [locale])
  /** Translates a key built at run time, or shows the raw value when unknown. */
  const tr = useCallback(
    (key: string, fallback: string) => (key in dictionaries.fr ? translate(locale, key as Key) : fallback),
    [locale],
  )
  const date = useCallback(
    (value: string | number | Date | null | undefined, withTime = true) =>
      value == null
        ? '—'
        : new Date(value).toLocaleString(tag(locale), withTime ? { dateStyle: 'short', timeStyle: 'short' } : { dateStyle: 'long' }),
    [locale],
  )
  const number = useCallback((value: number, digits = 0) => value.toLocaleString(tag(locale), { maximumFractionDigits: digits }), [locale])
  return { t, tr, locale, setLocale, date, number }
}
