import { useState } from 'react'
import { keepPreviousData } from '@tanstack/react-query'
import { Download, ScrollText } from 'lucide-react'
import { useListAudit } from '@/api/generated/docibis'
import { EmptyState } from '@/components/common/EmptyState'
import { Pager, ResetFilters, SearchBox, Select, Table, td, th, useDebounced } from '@/components/common/ListTools'
import { PageTitle } from '@/components/layout/PageTitle'
import { useI18n } from '@/i18n/useI18n'

const LIMIT = 50

// Colours of the journal: creations and changes amber, deletions red,
// sign-ins green, sign-outs grey.
function tone(action: string): string {
  if (action === 'login.failed' || action.endsWith('.delete') || action.endsWith('.revoke')) return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
  if (action === 'login' || action === 'setup') return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
  if (action === 'logout' || action.includes('sessions')) return 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300'
  return 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300'
}

/** Audit journal: who did what in the console, searched as you type. */
export function AuditPage() {
  const { t, tr, date } = useI18n()
  const [text, setText] = useState('')
  const [action, setAction] = useState('')
  const [actor, setActor] = useState('')
  const [offset, setOffset] = useState(0)
  const q = useDebounced(text.trim(), 300)
  const who = useDebounced(actor.trim(), 300)
  const label = (a: string) => tr(`audit.action.${a}`, a)

  const { data, error, isPending } = useListAudit(
    { limit: LIMIT, offset, ...(q && { q }), ...(action && { action }), ...(who && { actor: who }) },
    { query: { placeholderData: keepPreviousData, refetchInterval: 15_000 } },
  )

  return (
    <div>
      <PageTitle>{t('nav.audit')}</PageTitle>
      <p className="text-sm text-gray-600 dark:text-gray-400 mb-5 max-w-3xl">{t('audit.intro')}</p>

      <div className="card">
        <div className="flex flex-wrap items-center gap-3 mb-4">
          <SearchBox value={text} onChange={(v) => { setText(v); setOffset(0) }} placeholder={t('audit.search')} />
          <Select value={action} onChange={(v) => { setAction(v); setOffset(0) }} label={t('audit.col.action')}>
            <option value="">{t('audit.allActions')}</option>
            {(data?.actions ?? []).map((a) => (
              <option key={a} value={a}>
                {label(a)}
              </option>
            ))}
          </Select>
          <input
            className="input w-44"
            value={actor}
            onChange={(e) => { setActor(e.target.value); setOffset(0) }}
            placeholder={t('audit.actor')}
            aria-label={t('audit.actor')}
          />
          <ResetFilters
            show={text !== '' || action !== '' || actor !== ''}
            onClick={() => {
              setText('')
              setAction('')
              setActor('')
              setOffset(0)
            }}
          />
          <a
            className="btn-secondary inline-flex items-center gap-2 text-sm ml-auto"
            href={`/api/audit/export?${new URLSearchParams({ ...(q && { q }), ...(action && { action }), ...(who && { actor: who }) })}`}
            download
          >
            <Download className="w-4 h-4" /> {t('audit.export')}
          </a>
        </div>

        {error ? (
          <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
        ) : isPending ? (
          <p className="text-sm text-gray-500">{t('common.loading')}</p>
        ) : data.items.length === 0 ? (
          <EmptyState icon={<ScrollText className="w-10 h-10" />} title={t('audit.none')} />
        ) : (
          <>
            <Table
              head={
                <>
                  <th className={th}>{t('audit.col.date')}</th>
                  <th className={th}>{t('audit.col.action')}</th>
                  <th className={th}>{t('audit.actor')}</th>
                  <th className={th}>{t('audit.col.target')}</th>
                  <th className={th}>{t('audit.col.details')}</th>
                  <th className={th}>{t('audit.col.ip')}</th>
                </>
              }
            >
              {data.items.map((e) => (
                <tr key={e.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/30">
                  <td className={`${td} whitespace-nowrap text-gray-500`}>{date(e.createdAt)}</td>
                  <td className={td}>
                    <span className={`inline-block whitespace-nowrap text-xs font-semibold px-2 py-0.5 rounded-full ${tone(e.action)}`} title={e.action}>
                      {label(e.action)}
                    </span>
                  </td>
                  <td className={`${td} font-medium text-gray-900 dark:text-gray-100`}>{e.actor || '—'}</td>
                  <td className={`${td} font-mono text-xs text-gray-600 dark:text-gray-300 break-all`}>{e.target || '—'}</td>
                  <td className={`${td} text-gray-600 dark:text-gray-300 max-w-md break-words`}>{e.details || '—'}</td>
                  <td className={`${td} font-mono text-xs text-gray-500 whitespace-nowrap`}>{e.ip || '—'}</td>
                </tr>
              ))}
            </Table>
            <Pager offset={offset} limit={LIMIT} total={data.total} onChange={setOffset} />
          </>
        )}
      </div>
    </div>
  )
}
