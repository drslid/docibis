import { useId, useState, type ReactNode } from 'react'
import { CircleHelp } from 'lucide-react'
import { useI18n } from '@/i18n/useI18n'

/** A small "?" next to a field: its explanation and an example, at the moment it is needed. */
export function HelpTip({ children, label }: { children: ReactNode; label?: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const id = useId()
  return (
    <span className="relative inline-flex align-middle" onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}>
      <button
        type="button"
        className="p-0.5 rounded-full text-gray-400 hover:text-brand-600 dark:hover:text-brand-400 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-500"
        aria-label={label ?? t('tip.label')}
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen(!open)}
        onBlur={() => setOpen(false)}
      >
        <CircleHelp className="w-4 h-4" />
      </button>
      {open && (
        <span id={id} role="tooltip" className="absolute left-1/2 top-full z-40 mt-1.5 w-72 -translate-x-1/2 rounded-lg border border-gray-200 bg-white p-3 text-left text-xs font-normal normal-case tracking-normal leading-relaxed text-gray-700 shadow-lg dark:border-gray-700 dark:bg-gray-800 dark:text-gray-200">
          {children}
        </span>
      )}
    </span>
  )
}
