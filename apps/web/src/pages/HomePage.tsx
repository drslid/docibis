import type { ReactNode } from 'react'
import { AlertTriangle, Database, Loader2, Server } from 'lucide-react'
import { useGetInfo, type Info } from '@/api/generated/docibis'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { PageTitle } from '@/components/layout/PageTitle'
import { useI18n } from '@/i18n/useI18n'

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex justify-between gap-4 py-2 text-sm border-b last:border-b-0 border-gray-100 dark:border-gray-700">
      <span className="text-gray-500 dark:text-gray-400">{label}</span>
      <span className="font-medium text-right wrap-anywhere">{children}</span>
    </div>
  )
}

function Notice({ tone, children }: { tone: 'amber' | 'red'; children: ReactNode }) {
  const colours = {
    amber: 'bg-amber-50 border-amber-200 text-amber-800 dark:bg-amber-900/20 dark:border-amber-800 dark:text-amber-300',
    red: 'bg-red-50 border-red-200 text-red-800 dark:bg-red-900/20 dark:border-red-800 dark:text-red-300',
  }[tone]
  return (
    <div className={`flex items-start gap-2 border px-4 py-2 rounded-lg mt-4 text-sm ${colours}`}>
      <AlertTriangle className="w-4 h-4 mt-0.5 shrink-0" />
      <span>{children}</span>
    </div>
  )
}

function DatabaseCard({ database }: { database: Info['database'] }) {
  const { engine, replication } = database
  const { t } = useI18n()
  return (
    <div className="card">
      <h2 className="flex items-center gap-2 text-lg font-semibold mb-3">
        <Database className="w-5 h-5 text-teal-500" /> {t('home.database')}
      </h2>
      <Row label={t('home.engine')}>{engine === 'sqlite' ? 'SQLite' : 'PostgreSQL'}</Row>
      {engine === 'sqlite' && (
        <>
          <Row label={t('home.replica')}>{replication.enabled ? replication.target : t('home.none')}</Row>
          {replication.enabled && <Row label={t('home.lock')}>{replication.locked ? t('home.lockOn') : t('home.lockOff')}</Row>}
        </>
      )}
      {engine === 'sqlite' && !replication.enabled && <Notice tone="amber">{t('home.noCopy')}</Notice>}
      {replication.leaseLost && <Notice tone="red">{t('home.leaseLost')}</Notice>}
    </div>
  )
}

export function HomePage({ embedded }: { embedded?: boolean }) {
  const { t } = useI18n()
  const { data: info, error, isPending, refetch, isFetching } = useGetInfo({
    query: {
      retry: false,
      // While the API starts (503), ask again every 3 seconds.
      refetchInterval: (query) => (query.state.error?.status === 503 ? 3_000 : false),
    },
  })

  return (
    <div>
      {!embedded && <PageTitle>{t('home.title')}</PageTitle>}
      {isPending && <LoadingSpinner />}
      {error && error.status === 503 && (
        <div className="card flex items-center gap-3 text-gray-600 dark:text-gray-300">
          <Loader2 className="w-5 h-5 animate-spin text-brand-500" />
          {t('home.starting')}
        </div>
      )}
      {error && error.status !== 503 && (
        <div className="card">
          <p className="text-gray-700 dark:text-gray-300 mb-4">{t('home.noApi', { message: error.message })}</p>
          <button className="btn-secondary" onClick={() => refetch()} disabled={isFetching}>
            {t('common.retry')}
          </button>
        </div>
      )}
      {info && (
        <div className="grid gap-6 md:grid-cols-2">
          <div className="card">
            <h2 className="flex items-center gap-2 text-lg font-semibold mb-3">
              <Server className="w-5 h-5 text-sky-500" /> {t('home.instance')}
            </h2>
            <Row label={t('home.application')}>{info.name}</Row>
            <Row label={t('home.version')}>{info.version}</Row>
            <Row label={t('home.environment')}>{info.instance || t('home.undefined')}</Row>
          </div>
          <DatabaseCard database={info.database} />
        </div>
      )}
    </div>
  )
}
