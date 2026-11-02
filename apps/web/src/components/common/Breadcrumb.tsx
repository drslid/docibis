import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { ChevronRight, Home } from 'lucide-react'
import { useI18n } from '@/i18n/useI18n'

export interface Crumb {
  label: ReactNode
  to?: string
}

/** Where the page sits: home, then the sections down to the page. */
export function Breadcrumb({ items }: { items: Crumb[] }) {
  const { t } = useI18n()
  return (
    <nav aria-label={t('crumbs.label')} className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400 flex-wrap min-w-0">
      <Link to="/" className="hover:text-brand-600 dark:hover:text-brand-400 transition-colors" aria-label={t('nav.dashboard')}>
        <Home className="w-4 h-4" />
      </Link>
      {items.map((item, i) => (
        <span key={i} className="flex items-center gap-1.5 min-w-0">
          <ChevronRight className="w-3.5 h-3.5 shrink-0 text-gray-300 dark:text-gray-600" />
          {item.to ? (
            <Link to={item.to} className="hover:text-brand-600 dark:hover:text-brand-400 transition-colors">{item.label}</Link>
          ) : (
            <span className="text-gray-900 dark:text-gray-100 font-medium truncate" aria-current="page">{item.label}</span>
          )}
        </span>
      ))}
    </nav>
  )
}
