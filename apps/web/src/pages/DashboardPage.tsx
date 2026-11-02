import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { Activity, AlertTriangle, CheckCircle2, Clock, FileText, Gauge, KeyRound, TrendingUp, Users, XCircle, type LucideIcon } from 'lucide-react'
import { Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useGetDashboard, type NameCount } from '@/api/generated/docibis'
import { FirstSteps } from '@/components/help/FirstSteps'
import { PageTitle } from '@/components/layout/PageTitle'
import { useI18n } from '@/i18n/useI18n'
import { HomePage } from './HomePage'

interface Kpi {
  icon: LucideIcon
  wrap: string
  colour: string
  value: ReactNode
  label: string
  sub?: string
  valueColour?: string
}

function KpiCard({ k }: { k: Kpi }) {
  return (
    <div className="card p-3 sm:p-4">
      <div className="flex items-start gap-2 sm:gap-3">
        <div className={`w-8 h-8 sm:w-10 sm:h-10 shrink-0 rounded-lg flex items-center justify-center ${k.wrap}`}>
          <k.icon className={`w-4 h-4 sm:w-5 sm:h-5 ${k.colour}`} />
        </div>
        <div className="min-w-0 flex-1">
          <div className={`text-lg sm:text-xl xl:text-2xl font-bold leading-tight truncate tabular-nums ${k.valueColour ?? 'text-gray-900 dark:text-gray-100'}`}>{k.value}</div>
          {k.sub && <div className="text-[10px] text-gray-400 dark:text-gray-500 truncate">{k.sub}</div>}
          <div className="text-xs sm:text-sm text-gray-500 dark:text-gray-400 leading-snug break-words">{k.label}</div>
        </div>
      </div>
    </div>
  )
}

function Ranking({ title, icon, items, empty }: { title: string; icon: ReactNode; items: NameCount[]; empty: string }) {
  const max = Math.max(1, ...items.map((i) => i.count))
  return (
    <div className="card p-4">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">
        {icon} {title}
      </h3>
      {items.length === 0 ? (
        <p className="text-sm text-gray-400">{empty}</p>
      ) : (
        <ul className="space-y-2">
          {items.map((i) => (
            <li key={i.name} className="text-sm">
              <div className="flex justify-between gap-2">
                <span className="truncate text-gray-700 dark:text-gray-300">{i.name}</span>
                <span className="tabular-nums text-gray-500">{i.count}</span>
              </div>
              <div className="h-1.5 rounded-full bg-gray-100 dark:bg-gray-700 mt-1">
                <div className="h-1.5 rounded-full bg-brand-500" style={{ width: `${(i.count / max) * 100}%` }} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

const axis = { fontSize: 11, fill: '#9ca3af' }

/** Home page: the figures of the last 30 days. */
export default function DashboardPage() {
  const { t, date, number, locale } = useI18n()
  const { data, error, isPending } = useGetDashboard({ query: { refetchInterval: 30_000 } })

  const sourceLabel = (s: string) => (s === 'console' ? t('history.console') : s === 'api' ? t('history.api') : s === 'mcp' ? t('history.mcp') : s)

  return (
    <div>
      <PageTitle>{t('nav.dashboard')}</PageTitle>
      {error ? (
        <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
      ) : isPending ? (
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      ) : (
        <div className="space-y-6">
          <FirstSteps dashboard={data} />
          <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 2xl:grid-cols-7 gap-3 lg:gap-4">
            {(
              [
                { icon: FileText, wrap: 'bg-brand-100 dark:bg-brand-900/30', colour: 'text-brand-600', value: number(data.today), label: t('dash.today'), sub: t('dash.week', { n: number(data.last7d) }) },
                { icon: Activity, wrap: 'bg-gray-100 dark:bg-gray-800', colour: 'text-gray-600 dark:text-gray-300', value: number(data.last30d), label: t('dash.month') },
                { icon: TrendingUp, wrap: 'bg-emerald-100 dark:bg-emerald-900/30', colour: 'text-emerald-600', value: `${number(data.successRate, 1)} %`, label: t('dash.successRate'), valueColour: 'text-emerald-600' },
                { icon: Gauge, wrap: 'bg-violet-100 dark:bg-violet-900/30', colour: 'text-violet-600', value: `${number(data.p95Ms)} ms`, label: t('dash.p95'), sub: t('mon.p50', { ms: number(data.p50Ms) }) },
                { icon: AlertTriangle, wrap: 'bg-amber-100 dark:bg-amber-900/30', colour: 'text-amber-600', value: number(data.refused30d), label: t('dash.refused'), valueColour: 'text-amber-600' },
                { icon: XCircle, wrap: 'bg-red-100 dark:bg-red-900/30', colour: 'text-red-600', value: number(data.errors30d), label: t('dash.errors'), valueColour: 'text-red-600' },
                { icon: KeyRound, wrap: 'bg-cyan-100 dark:bg-cyan-900/30', colour: 'text-cyan-600', value: number(data.activeKeys), label: t('dash.keys'), sub: t('dash.users', { n: data.users }) },
              ] satisfies Kpi[]
            ).map((k) => (
              <KpiCard key={k.label} k={k} />
            ))}
          </div>

          <div className="card p-4">
            <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">{t('dash.perDay')}</h3>
            <div className="h-64">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart
                  data={data.days.map((d) => ({ ...d, label: new Date(`${d.date}T12:00:00Z`).toLocaleDateString(locale === 'en' ? 'en-GB' : locale, { day: '2-digit', month: '2-digit' }) }))}
                  margin={{ top: 4, right: 8, left: -16, bottom: 0 }}
                >
                  <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
                  <XAxis dataKey="label" tick={axis} interval={4} />
                  <YAxis tick={axis} allowDecimals={false} />
                  <Tooltip contentStyle={{ borderRadius: 8, fontSize: 12 }} />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  <Bar dataKey="ok" name={t('history.ok')} stackId="d" fill="#10b981" />
                  <Bar dataKey="refused" name={t('history.refused')} stackId="d" fill="#f59e0b" />
                  <Bar dataKey="errors" name={t('history.error')} stackId="d" fill="#ef4444" />
                </BarChart>
              </ResponsiveContainer>
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-3">
            <Ranking title={t('dash.topTemplates')} icon={<FileText className="w-4 h-4 text-emerald-500" />} items={data.topTemplates} empty={t('history.none')} />
            <Ranking title={t('dash.topActors')} icon={<Users className="w-4 h-4 text-cyan-500" />} items={data.topActors} empty={t('history.none')} />
            <Ranking
              title={t('dash.sources')}
              icon={<Activity className="w-4 h-4 text-indigo-500" />}
              items={data.sources.map((s) => ({ ...s, name: sourceLabel(s.name) }))}
              empty={t('history.none')}
            />
          </div>

          <div className="card p-4">
            <div className="flex items-center mb-3">
              <h3 className="text-sm font-semibold text-gray-700 dark:text-gray-300 flex-1">{t('dash.recent')}</h3>
              <Link to="/documents" className="text-sm text-brand-600 hover:underline">{t('dash.all')}</Link>
            </div>
            {data.recent.length === 0 ? (
              <p className="text-sm text-gray-400">{t('history.none')}</p>
            ) : (
              <ul className="divide-y divide-gray-100 dark:divide-gray-700">
                {data.recent.map((r) => (
                  <li key={r.id} className="py-2 flex items-center gap-3 text-sm">
                    {r.status === 'ok' ? (
                      <CheckCircle2 className="w-4 h-4 text-emerald-500 shrink-0" />
                    ) : r.status === 'refused' ? (
                      <AlertTriangle className="w-4 h-4 text-amber-500 shrink-0" />
                    ) : (
                      <XCircle className="w-4 h-4 text-red-500 shrink-0" />
                    )}
                    <span className="font-medium truncate flex-1">{r.template || '—'}</span>
                    <span className="text-gray-500 truncate hidden sm:inline">{sourceLabel(r.source)} · {r.actor}</span>
                    <span className="inline-flex items-center gap-1 text-gray-400 whitespace-nowrap tabular-nums">
                      <Clock className="w-3.5 h-3.5" /> {number(r.durationMs)} ms
                    </span>
                    <span className="text-gray-400 whitespace-nowrap hidden md:inline">{date(r.createdAt)}</span>
                  </li>
                ))}
              </ul>
            )}
          </div>

          <HomePage embedded />
        </div>
      )}
    </div>
  )
}
