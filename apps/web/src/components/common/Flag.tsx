import { useId } from 'react'
import type { Locale } from '@/i18n/useI18n'

/** Small flags for the language picker (drawn, no image to load). */
export function Flag({ locale, className = 'w-5 h-3.5' }: { locale: Locale; className?: string }) {
  const clip = useId()
  const box = `shrink-0 rounded-[2px] shadow-[0_0_0_1px_rgba(0,0,0,0.12)] ${className}`
  switch (locale) {
    case 'fr':
    case 'it': {
      const [left, right] = locale === 'fr' ? ['#002654', '#ED2939'] : ['#009246', '#CE2B37']
      return (
        <svg viewBox="0 0 3 2" preserveAspectRatio="none" className={box} aria-hidden="true">
          <rect width="1" height="2" fill={left} />
          <rect x="1" width="1" height="2" fill="#fff" />
          <rect x="2" width="1" height="2" fill={right} />
        </svg>
      )
    }
    case 'de':
      return (
        <svg viewBox="0 0 5 3" preserveAspectRatio="none" className={box} aria-hidden="true">
          <rect width="5" height="1" fill="#000" />
          <rect y="1" width="5" height="1" fill="#DD0000" />
          <rect y="2" width="5" height="1" fill="#FFCE00" />
        </svg>
      )
    case 'en':
      return (
        <svg viewBox="0 0 60 30" preserveAspectRatio="none" className={box} aria-hidden="true">
          <clipPath id={clip}>
            <path d="M30,15 h30 v15 z v15 h-30 z h-30 v-15 z v-15 h30 z" />
          </clipPath>
          <path d="M0,0 v30 h60 v-30 z" fill="#012169" />
          <path d="M0,0 L60,30 M60,0 L0,30" stroke="#fff" strokeWidth="6" />
          <path d="M0,0 L60,30 M60,0 L0,30" clipPath={`url(#${clip})`} stroke="#C8102E" strokeWidth="4" />
          <path d="M30,0 v30 M0,15 h60" stroke="#fff" strokeWidth="10" />
          <path d="M30,0 v30 M0,15 h60" stroke="#C8102E" strokeWidth="6" />
        </svg>
      )
  }
}
