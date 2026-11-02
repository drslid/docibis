import { Gauge, RefreshCw, Zap } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import { getGetTemplateQueryKey, getListTemplatesQueryKey, useQualifyTemplateVersion, type LibraryVersion } from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { HelpTip } from '@/components/help/HelpTip'
import { useI18n } from '@/i18n/useI18n'

const TONES = {
  qualified: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300',
  refused: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  standard: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  error: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
}

/** The fast engine of the served version: its state, the gap measured with LibreOffice, a new qualification. */
export function FastPanel({ id, version, editor }: { id: number; version?: LibraryVersion; editor: boolean }) {
  const { t, date, number } = useI18n()
  const queryClient = useQueryClient()
  const qualify = useQualifyTemplateVersion({
    mutation: {
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey: getGetTemplateQueryKey(id) })
        queryClient.invalidateQueries({ queryKey: getListTemplatesQueryKey() })
      },
    },
  })
  const fast = version?.fast
  return (
    <section className="card">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-1">
        <h2 className="text-lg font-semibold inline-flex items-center gap-2">
          <Gauge className="w-5 h-5 text-brand-600" /> {t('fast.title')} <HelpTip>{t('tip.fast')}</HelpTip>
        </h2>
        {editor && version && (
          <button className="btn-ghost py-1 text-sm inline-flex items-center gap-1" disabled={qualify.isPending}
            onClick={() => qualify.mutate({ id, number: version.number })}>
            <RefreshCw className={`w-4 h-4 ${qualify.isPending ? 'animate-spin' : ''}`} /> {t('fast.requalify')}
          </button>
        )}
      </div>
      <p className="text-xs text-gray-500 mb-3">{t('fast.intro')}</p>
      {!version ? (
        <p className="text-sm text-gray-500">{t('fast.unpublished')}</p>
      ) : !fast ? (
        <p className="text-sm text-gray-500">{t('fast.pending')}</p>
      ) : (
        <div className="space-y-1 text-sm">
          <span className={`inline-flex items-center gap-1 text-xs font-medium px-2 py-0.5 rounded-full ${TONES[fast.state]}`}>
            {fast.state === 'qualified' && <Zap className="w-3 h-3" />} {t(`fast.state.${fast.state}`)}
          </span>
          {fast.reason && <p className="text-gray-600 dark:text-gray-300">{fast.reason}</p>}
          {fast.permille !== undefined && fast.tests !== undefined && (
            <p className="text-gray-600 dark:text-gray-300">{t('fast.measure', { permille: number(fast.permille, 2), tests: fast.tests })}</p>
          )}
          {fast.overlayMs !== undefined && fast.standardMs !== undefined && (
            <p className="text-gray-600 dark:text-gray-300">{t('fast.speed', { overlay: number(fast.overlayMs, 1), standard: number(fast.standardMs) })}</p>
          )}
          <p className="text-xs text-gray-400">{t('fast.at', { date: date(fast.at) })}</p>
        </div>
      )}
      {qualify.error && (
        <p className="text-sm text-red-700 dark:text-red-400 mt-2" role="alert">
          {qualify.error instanceof ApiError ? (qualify.error.problem?.detail ?? qualify.error.message) : String(qualify.error)}
        </p>
      )}
    </section>
  )
}
