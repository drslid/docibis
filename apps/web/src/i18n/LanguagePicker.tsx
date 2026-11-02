import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Check, ChevronDown } from 'lucide-react'
import { getGetSessionQueryKey, useGetSession, useUpdateProfile } from '@/api/generated/docibis'
import { Flag } from '@/components/common/Flag'
import { LOCALE_LABELS } from './translations'
import { LOCALES, useI18n, type Locale } from './useI18n'

/** Flag and language menu of the top bar. The choice follows the account. */
export function LanguagePicker() {
  const { t, locale, setLocale } = useI18n()
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()
  const { data: session } = useGetSession({ query: { retry: false } })
  const save = useUpdateProfile({ mutation: { onSuccess: () => queryClient.invalidateQueries({ queryKey: getGetSessionQueryKey() }) } })

  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false)
    }
    const escape = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('mousedown', close)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  const choose = (next: Locale) => {
    setOpen(false)
    setLocale(next)
    if (session?.authenticated && session.user?.language !== next) save.mutate({ data: { language: next } })
  }

  return (
    <div ref={box} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="flex items-center gap-2 px-3 py-1.5 rounded-full bg-gray-200 dark:bg-gray-700 text-sm font-medium text-gray-700 dark:text-gray-200 hover:bg-gray-300 dark:hover:bg-gray-600 transition-colors"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t('top.languageIs', { name: LOCALE_LABELS[locale] })}
      >
        <Flag locale={locale} />
        <span className="hidden sm:inline">{locale.toUpperCase()}</span>
        <ChevronDown className="w-3.5 h-3.5" />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 mt-2 w-44 rounded-xl border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 shadow-lg py-1 z-50">
          {LOCALES.map((l) => (
            <button
              key={l}
              type="button"
              role="menuitemradio"
              aria-checked={l === locale}
              lang={l}
              onClick={() => choose(l)}
              className="w-full flex items-center gap-3 px-3 py-2 text-sm text-gray-700 dark:text-gray-200 hover:bg-gray-100 dark:hover:bg-gray-700"
            >
              <Flag locale={l} />
              <span className="flex-1 text-left">{LOCALE_LABELS[l]}</span>
              {l === locale && <Check className="w-4 h-4 text-brand-600" />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
