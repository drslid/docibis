import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, Download, Loader2, Play } from 'lucide-react'
import { getListBackupsQueryKey, useCreateBackup, useListBackups, type Backup, type BackupKind } from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { CopyButton } from '@/components/common/CopyButton'
import { SettingsCard } from '@/components/common/SettingsCard'
import { useI18n, type Key } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

const KINDS: Record<BackupKind, Key> = {
  nightly: 'backups.kind.nightly',
  manual: 'backups.kind.manual',
  update: 'backups.kind.update',
  restore: 'backups.kind.restore',
}

const th = 'px-3 py-2 text-left text-xs font-semibold text-gray-500 dark:text-gray-400'
const td = 'px-3 py-2 align-top'

const rows = (b: Backup) => Object.values(b.rows ?? {}).reduce((sum, n) => sum + n, 0)

/** The backups of the database, made each night or on demand; restoring is a command, the service stopped. */
export function BackupsSection() {
  const { t, date, number } = useI18n()
  const queryClient = useQueryClient()
  const { data, error } = useListBackups({ query: { refetchInterval: (q) => (q.state.data?.running ? 3_000 : false) } })
  const create = useCreateBackup({ mutation: { onSuccess: (out) => queryClient.setQueryData(getListBackupsQueryKey(), out) } })

  if (error) return <div className="card text-red-700">{t('common.unavailable', { message: message(error) })}</div>
  if (!data) return <Loader2 className="w-5 h-5 animate-spin text-gray-400" />
  const latest = data.backups.find((b) => b.kind !== 'restore')
  const command = `docibis restore ${latest?.name ?? 'latest'}`

  return (
    <SettingsCard
      title={t('backups.title')}
      actions={
        <button type="button" className="btn-primary inline-flex items-center gap-2 text-sm" disabled={data.keyMissing || data.running || create.isPending} onClick={() => create.mutate()}>
          {data.running || create.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />} {t('backups.now')}
        </button>
      }
    >
      <p className="mb-4 max-w-3xl">{t('backups.help')}</p>
      {data.keyMissing && <p className="text-sm text-amber-700 dark:text-amber-400 mb-3" role="alert">{t('backups.keyMissing')}</p>}
      {data.lastError && <p className="text-sm text-red-700 mb-3" role="alert">{t('backups.failed', { error: data.lastError })}</p>}
      {create.error && <p className="text-sm text-red-700 mb-3" role="alert">{message(create.error)}</p>}
      {data.running && <p className="text-sm text-gray-600 dark:text-gray-300 mb-3" role="status">{t('backups.running')}</p>}
      <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">{t('backups.location', { location: data.location || '—' })}</p>
      {data.backups.length === 0 ? (
        <p className="text-gray-500 dark:text-gray-400">{t('backups.none')}</p>
      ) : (
        <div className="overflow-x-auto -mx-3">
          <table className="w-full text-sm">
            <thead>
              <tr>
                <th className={th}>{t('backups.date')}</th>
                <th className={th}>{t('backups.kind')}</th>
                <th className={th}>{t('backups.content')}</th>
                <th className={th}>{t('backups.check')}</th>
                <th className={th}><span className="sr-only">{t('backups.download')}</span></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
              {data.backups.map((b) => (
                <tr key={b.name}>
                  <td className={td}>
                    <div className="font-medium text-gray-900 dark:text-gray-100">{date(b.createdAt)}</div>
                    <div className="text-xs text-gray-400 font-mono break-all">{b.name}</div>
                  </td>
                  <td className={td}>{t(KINDS[b.kind] ?? 'backups.kind.manual')}</td>
                  <td className={td}>
                    {number(b.size / 1048576, 1)} Mo · {t('backups.rows', { n: number(rows(b)) })}
                    {b.version && <span className="text-gray-500 dark:text-gray-400"> · DocIbis {b.version}</span>}
                  </td>
                  <td className={td}>
                    {b.verified ? (
                      <span className="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400"><CheckCircle2 className="w-4 h-4" /> {t('backups.verified')}</span>
                    ) : (
                      <span className="inline-flex items-center gap-1 text-amber-700 dark:text-amber-400" title={b.problem}><AlertTriangle className="w-4 h-4" /> {t('backups.unverified')}</span>
                    )}
                  </td>
                  <td className={`${td} text-right`}>
                    <a className="inline-flex p-1.5 text-gray-500 hover:text-brand-700 dark:hover:text-brand-400" href={`/api/backups/${encodeURIComponent(b.name)}`} download={b.name} aria-label={`${t('backups.download')} ${b.name}`}>
                      <Download className="w-4 h-4" />
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="mt-6 pt-4 border-t border-gray-100 dark:border-gray-700">
        <div className="label">{t('backups.restoreTitle')}</div>
        <p className="mb-2 max-w-3xl">{t('backups.restoreHelp')}</p>
        <div className="flex items-center gap-2">
          <code className="flex-1 min-w-0 text-xs break-all rounded-md bg-gray-50 dark:bg-gray-900 px-2 py-1.5">{command}</code>
          <CopyButton text={command} className="shrink-0 py-1 px-2 text-xs" />
        </div>
      </div>
    </SettingsCard>
  )
}
