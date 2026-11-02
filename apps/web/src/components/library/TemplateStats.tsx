import { useState } from 'react'
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useGetTemplateStats } from '@/api/generated/docibis'
import { useI18n } from '@/i18n/useI18n'

const axis = { fontSize: 11, fill: '#9ca3af' }
const SOURCES: Record<string, string> = { api: 'API', console: 'Console', mcp: 'MCP' }

function Figure({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="rounded-lg border border-gray-200 dark:border-gray-700 p-3 min-w-0">
      <div className="text-xs text-gray-500 dark:text-gray-400 truncate">{label}</div>
      <div className="text-xl font-bold tabular-nums text-gray-900 dark:text-gray-100 truncate">{value}</div>
      {sub && <div className="text-[11px] text-gray-400 truncate">{sub}</div>}
    </div>
  )
}

/** How a template is used: documents per day, who produces it, how fast. */
export function TemplateStats({ id }: { id: number }) {
  const { t, number, date } = useI18n()
  const [days, setDays] = useState(30)
  const { data, error } = useGetTemplateStats(id, { days })
  if (error) return <p className="text-sm text-red-700" role="alert">{error.message}</p>
  if (!data) return <p className="text-sm text-gray-500">{t('common.loading')}</p>
  const series = data.series.map((p) => ({ day: p.day.slice(5).split('-').reverse().join('/'), ok: p.count - p.errors, errors: p.errors }))
  const success = data.count > 0 ? Math.round(((data.count - data.errors - data.refused) * 1000) / data.count) / 10 : undefined

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        {[7, 30, 90].map((d) => (
          <button key={d} type="button" aria-pressed={days === d} onClick={() => setDays(d)}
            className={`text-xs px-2.5 py-1 rounded-full border ${days === d ? 'border-brand-500 bg-brand-50 text-brand-700 dark:bg-brand-900/30 dark:text-brand-300' : 'border-gray-200 text-gray-600 dark:border-gray-700 dark:text-gray-300'}`}>
            {t('stats.days', { n: d })}
          </button>
        ))}
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Figure label={t('stats.documents')} value={number(data.count)} sub={success !== undefined ? t('stats.success', { pct: success.toLocaleString() }) : undefined} />
        <Figure label={t('stats.failures')} value={number(data.errors)} sub={t('stats.refused', { n: data.refused })} />
        <Figure label={t('stats.duration')} value={`${number(data.p50Ms)} ms`} sub={t('stats.p95', { ms: number(data.p95Ms) })} />
        <Figure label={t('stats.lastUse')} value={data.lastUsedAt ? date(data.lastUsedAt) : '—'} sub={data.lastUsedAt ? undefined : t('stats.unused', { n: days })} />
      </div>
      {data.count > 0 && (
        <div className="h-48">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={series} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
              <XAxis dataKey="day" tick={axis} interval={Math.max(0, Math.floor(series.length / 10) - 1)} />
              <YAxis tick={axis} allowDecimals={false} />
              <Tooltip contentStyle={{ borderRadius: 8, fontSize: 12 }} />
              <Bar dataKey="ok" name={t('stats.documents')} stackId="d" fill="#10b981" />
              <Bar dataKey="errors" name={t('stats.failures')} stackId="d" fill="#ef4444" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      )}
      <div className="grid gap-4 md:grid-cols-2 text-sm">
        <div>
          <h3 className="font-medium mb-2">{t('stats.callers')}</h3>
          {data.callers.length === 0 ? <p className="text-gray-500">—</p> : (
            <ul className="space-y-1">
              {data.callers.slice(0, 8).map((c) => (
                <li key={`${c.source}-${c.actor}`} className="flex justify-between gap-3">
                  <span className="truncate"><span className="text-gray-400">{SOURCES[c.source] ?? c.source} ·</span> {c.actor || '—'}</span>
                  <span className="tabular-nums text-gray-600 dark:text-gray-300">{number(c.count)}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div>
          <h3 className="font-medium mb-2">{t('stats.versions')}</h3>
          {data.versions.length === 0 ? <p className="text-gray-500">—</p> : (
            <ul className="space-y-1">
              {data.versions.map((v) => (
                <li key={v.version} className="flex justify-between gap-3"><span>v{v.version}</span><span className="tabular-nums text-gray-600 dark:text-gray-300">{number(v.count)}</span></li>
              ))}
            </ul>
          )}
          {data.lastError && <p className="text-xs text-red-700 dark:text-red-400 mt-3">{t('stats.lastError', { error: data.lastError })}</p>}
        </div>
      </div>
    </div>
  )
}
