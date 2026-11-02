import { useState } from 'react'
import { CircleCheck, GitCompare, Loader2 } from 'lucide-react'
import { useCompareTemplateVersions, type LibraryVersion } from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { HelpTip } from '@/components/help/HelpTip'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

const LINE = {
  same: 'text-gray-500',
  removed: 'bg-blue-50 text-blue-800 dark:bg-blue-900/30 dark:text-blue-200',
  added: 'bg-red-50 text-red-800 dark:bg-red-900/30 dark:text-red-200',
}
const SIGN = { same: ' ', removed: '−', added: '+' }

/** Produces two versions with the same data and shows what changed. */
export function CompareVersions({ id, versions, fixed }: { id: number; versions: LibraryVersion[]; fixed: boolean }) {
  const { t } = useI18n()
  const [from, setFrom] = useState(versions[1]?.number ?? 1)
  const [to, setTo] = useState(versions[0]?.number ?? 1)
  const [data, setData] = useState('')
  const compare = useCompareTemplateVersions()
  const result = compare.data

  let parsed: Record<string, unknown> | undefined
  let invalid = false
  if (data.trim()) {
    try {
      const value: unknown = JSON.parse(data)
      if (value && typeof value === 'object' && !Array.isArray(value)) parsed = value as Record<string, unknown>
      else invalid = true
    } catch {
      invalid = true
    }
  }

  return (
    <div className="space-y-4">
      <div className="grid gap-3 md:grid-cols-[10rem_10rem_1fr_auto] md:items-end">
        <div>
          <label className="label" htmlFor="cmp-from">{t('cmp.from')}</label>
          <select id="cmp-from" className="input" value={from} onChange={(e) => setFrom(Number(e.target.value))}>
            {versions.map((v) => <option key={v.number} value={v.number}>v{v.number}</option>)}
          </select>
        </div>
        <div>
          <label className="label" htmlFor="cmp-to">{t('cmp.to')}</label>
          <select id="cmp-to" className="input" value={to} onChange={(e) => setTo(Number(e.target.value))}>
            {versions.map((v) => <option key={v.number} value={v.number}>v{v.number}</option>)}
          </select>
        </div>
        {!fixed ? (
          <div>
            <label className="label" htmlFor="cmp-data">{t('cmp.data')} <HelpTip>{t('tip.compareData')}</HelpTip></label>
            <input id="cmp-data" className="input font-mono text-xs" value={data} onChange={(e) => setData(e.target.value)} placeholder={t('cmp.dataHint')} />
          </div>
        ) : <div />}
        <button className="btn-primary inline-flex items-center gap-2 justify-center" disabled={from === to || invalid || compare.isPending}
          onClick={() => compare.mutate({ id, data: { from, to, ...(parsed && { data: parsed }) } })}>
          {compare.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <GitCompare className="w-4 h-4" />} {t('cmp.run')}
        </button>
      </div>
      {invalid && <p className="text-sm text-red-700">{t('cmp.invalid')}</p>}
      {compare.error && <p className="text-sm text-red-700 dark:text-red-400" role="alert">{message(compare.error)}</p>}
      {result && (
        <div className="space-y-4">
          {result.identical ? (
            <p className="text-sm text-green-700 dark:text-green-400 inline-flex items-center gap-2"><CircleCheck className="w-4 h-4" /> {t('cmp.identical', { from: result.from, to: result.to })}</p>
          ) : (
            <p className="text-sm text-gray-600 dark:text-gray-300">
              {t('cmp.legend')} <span className="text-red-600 font-medium">{t('cmp.added')}</span> · <span className="text-blue-600 font-medium">{t('cmp.removed')}</span>
            </p>
          )}
          <div className="grid gap-4">
            {result.pages.map((p) => (
              <div key={p.number} className="rounded-lg border border-gray-200 dark:border-gray-700 p-3">
                <div className="flex items-center justify-between text-sm mb-2">
                  <span className="font-medium">{t('cmp.page', { n: p.number })}</span>
                  <span className={p.permille === 0 ? 'text-green-700 dark:text-green-400' : 'text-amber-700 dark:text-amber-400'}>
                    {p.permille < 0 ? (p.before ? t('cmp.onlyFrom', { v: result.from }) : t('cmp.onlyTo', { v: result.to })) : p.permille === 0 ? t('cmp.same') : t('cmp.permille', { n: p.permille.toLocaleString() })}
                  </span>
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <figure>
                    {p.before ? <img src={p.before} alt={t('cmp.pageOf', { n: p.number, v: result.from })} className="w-full border border-gray-200 dark:border-gray-700 bg-white" /> : <div className="h-full min-h-40 rounded bg-gray-50 dark:bg-gray-900" />}
                    <figcaption className="text-xs text-gray-400 mt-1 text-center">v{result.from}</figcaption>
                  </figure>
                  <figure>
                    {p.after ? <img src={p.after} alt={t('cmp.pageOf', { n: p.number, v: result.to })} className="w-full border border-gray-200 dark:border-gray-700 bg-white" /> : <div className="h-full min-h-40 rounded bg-gray-50 dark:bg-gray-900" />}
                    <figcaption className="text-xs text-gray-400 mt-1 text-center">v{result.to}</figcaption>
                  </figure>
                </div>
              </div>
            ))}
          </div>
          {result.text.length > 0 && (
            <div>
              <h3 className="text-sm font-medium mb-2">{t('cmp.text')}</h3>
              <pre className="text-xs rounded-lg border border-gray-200 dark:border-gray-700 p-2 overflow-x-auto max-h-96">
                {result.text.map((l, i) => (
                  <div key={i} className={`px-2 ${LINE[l.op]}`}>{SIGN[l.op]} {l.line}</div>
                ))}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
