import { createElement } from 'react'
import { Link } from 'react-router'
import { CircleAlert, CircleCheck, Info, TriangleAlert } from 'lucide-react'
import type { LintIssue, LintSummary } from '@/api/generated/docibis'
import { useI18n } from '@/i18n/useI18n'

const badge = 'inline-flex items-center gap-1 whitespace-nowrap text-xs font-medium px-2 py-0.5 rounded-full'

const LEVELS = {
  error: { icon: CircleAlert, tone: 'text-red-700 dark:text-red-400' },
  warning: { icon: TriangleAlert, tone: 'text-amber-700 dark:text-amber-400' },
  info: { icon: Info, tone: 'text-sky-700 dark:text-sky-400' },
}

/** The checks of the newest version of a template, in one badge. */
export function LintBadge({ lint }: { lint?: LintSummary }) {
  const { t } = useI18n()
  if (!lint) return <span className={`${badge} bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-300`}>{t('lint.pending')}</span>
  if (lint.errors > 0) {
    return <span className={`${badge} bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300`}><CircleAlert className="w-3 h-3" /> {t('lint.errors', { n: lint.errors })}</span>
  }
  if (lint.warnings > 0) {
    return <span className={`${badge} bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300`}><TriangleAlert className="w-3 h-3" /> {t('lint.warnings', { n: lint.warnings })}</span>
  }
  return <span className={`${badge} bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300`}><CircleCheck className="w-3 h-3" /> {t('lint.ok')}</span>
}

/** The checks of a template, errors first. */
export function LintList({ issues }: { issues?: LintIssue[] }) {
  const { t } = useI18n()
  if (!issues) return <p className="text-sm text-gray-500">{t('lint.pendingLong')}</p>
  if (issues.length === 0) {
    return <p className="text-sm text-green-700 dark:text-green-400 inline-flex items-center gap-2"><CircleCheck className="w-4 h-4" /> {t('lint.clean')}</p>
  }
  return (
    <>
      <ul className="space-y-1.5 text-sm">
        {issues.map((i) => (
          <li key={`${i.code}-${i.message}`} className="flex items-start gap-2">
            {createElement(LEVELS[i.level].icon, { className: `w-4 h-4 mt-0.5 shrink-0 ${LEVELS[i.level].tone}` })}
            <span className="text-gray-700 dark:text-gray-300">{i.message}</span>
          </li>
        ))}
      </ul>
      {issues.some((i) => i.level !== 'info') && (
        <Link to="/aide/syntaxe" className="inline-block mt-2 text-sm text-brand-700 dark:text-brand-400 underline">{t('lint.howTo')}</Link>
      )}
    </>
  )
}
