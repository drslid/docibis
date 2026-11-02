import { useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, BarChart3, BellRing, ChevronDown, ChevronRight, Cpu, KeyRound, Network, Pause, Play, RefreshCw, Trash2, X } from 'lucide-react'
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import {
  getGetMetricsTokenQueryKey,
  useCreateMetricsToken,
  useGetAlerts,
  useGetMetricsToken,
  useGetMonitoring,
  useRevokeMetricsToken,
  type MetricsToken,
  type Monitoring,
} from '@/api/generated/docibis'
import { CopyButton } from '@/components/common/CopyButton'
import { PageTitle } from '@/components/layout/PageTitle'
import { AlertsPanel, useRuleValue } from '@/components/monitoring/AlertsPanel'
import { ArchitectureMap, HEALTH_COLOURS, type Health, type MapEdge, type MapNode, type NodeId } from '@/components/monitoring/ArchitectureMap'
import { tag, useI18n } from '@/i18n/useI18n'

/** Prometheus: a token made here, shown once, and the scrape configuration. */
function PrometheusPanel() {
  const { t, date } = useI18n()
  const queryClient = useQueryClient()
  const { data: state } = useGetMetricsToken()
  const [token, setToken] = useState('')
  const done = (s: MetricsToken) => {
    queryClient.setQueryData(getGetMetricsTokenQueryKey(), { ...s, token: undefined })
    setToken(s.token ?? '')
  }
  const create = useCreateMetricsToken({ mutation: { onSuccess: done } })
  const revoke = useRevokeMetricsToken({ mutation: { onSuccess: done } })
  const scrape = `scrape_configs:
  - job_name: docibis
    scheme: ${window.location.protocol.replace(':', '')}
    metrics_path: /metrics
    authorization:
      type: Bearer
      credentials: ${token || 'dim_…'}
    static_configs:
      - targets: ['${window.location.host}']`
  const failure = create.error ?? revoke.error

  return (
    <div id="prometheus">
      <p className="text-sm text-gray-600 dark:text-gray-300 mb-3">{t('prom.intro')}</p>
      <div className="flex flex-wrap items-center gap-3 text-sm mb-4">
        <span className={`inline-flex items-center gap-1.5 font-medium ${state?.configured ? 'text-emerald-700 dark:text-emerald-400' : 'text-gray-500'}`}>
          <span className={`w-2 h-2 rounded-full ${state?.configured ? 'bg-emerald-500' : 'bg-gray-400'}`} />
          {state?.fromEnv ? t('prom.fromEnv') : state?.configured ? t('prom.on', { date: date(state.createdAt), name: state.createdBy ?? '' }) : t('prom.off')}
        </span>
        {!state?.fromEnv && (
          <div className="ml-auto flex gap-2">
            <button className="btn-primary inline-flex items-center gap-2 text-sm py-1.5" disabled={create.isPending} onClick={() => create.mutate()}>
              <KeyRound className="w-4 h-4" /> {state?.configured ? t('prom.regenerate') : t('prom.create')}
            </button>
            {state?.configured && (
              <button className="btn-ghost inline-flex items-center gap-2 text-sm py-1.5 hover:text-red-600" disabled={revoke.isPending} onClick={() => window.confirm(t('prom.revokeConfirm')) && revoke.mutate()}>
                <Trash2 className="w-4 h-4" /> {t('keys.revoke')}
              </button>
            )}
          </div>
        )}
      </div>
      {failure && <p className="text-sm text-red-700 mb-3" role="alert">{failure.message}</p>}
      {token && (
        <div className="rounded-lg border border-amber-300 bg-amber-50 dark:bg-amber-900/20 dark:border-amber-700 p-3 mb-4">
          <p className="text-sm font-medium text-amber-800 dark:text-amber-300 mb-2">{t('prom.copyNow')}</p>
          <div className="flex flex-wrap items-center gap-2">
            <code className="flex-1 min-w-0 break-all bg-white dark:bg-gray-900 border border-amber-200 dark:border-amber-800 rounded px-2 py-1 text-xs" data-testid="metrics-token">{token}</code>
            <CopyButton text={token} />
          </div>
        </div>
      )}
      <div className="label">{t('prom.scrape')}</div>
      <div className="relative">
        <pre className="bg-gray-900 text-gray-100 rounded-lg p-4 pr-24 text-xs leading-relaxed overflow-x-auto">{scrape}</pre>
        <CopyButton text={scrape} className="absolute top-2 right-2 py-1 px-2 text-xs" />
      </div>
      <p className="text-xs text-gray-500 dark:text-gray-400 mt-2">{t('prom.metrics')}</p>
    </div>
  )
}

/** A section that folds. */
function Section({ title, icon, open, onToggle, right, children }: { title: string; icon: ReactNode; open: boolean; onToggle: () => void; right?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      <div className="flex items-center justify-between gap-2">
        <button type="button" onClick={onToggle} className="flex items-center gap-2 text-left min-w-0" aria-expanded={open}>
          {open ? <ChevronDown className="w-4 h-4 text-gray-400 shrink-0" /> : <ChevronRight className="w-4 h-4 text-gray-400 shrink-0" />}
          {icon}
          <h3 className="text-base font-semibold text-gray-900 dark:text-gray-100 truncate">{title}</h3>
        </button>
        {right}
      </div>
      {open && <div className="mt-4">{children}</div>}
    </section>
  )
}

function Sparkline({ values, colour }: { values: number[]; colour: string }) {
  if (values.length < 2) return null
  const max = Math.max(...values, 1)
  const points = values.map((v, i) => `${(i / (values.length - 1)) * 80},${22 - (v / max) * 20}`).join(' ')
  return (
    <svg viewBox="0 0 80 24" className="w-20 h-6 shrink-0" aria-hidden>
      <polyline points={points} fill="none" stroke={colour} strokeWidth={1.5} strokeLinejoin="round" />
    </svg>
  )
}

function Tile({ label, value, sub, health, spark }: { label: string; value: string; sub?: string; health?: Health; spark?: number[] }) {
  const colour = health ? HEALTH_COLOURS[health] : '#2563eb'
  return (
    <div className="rounded-lg border border-gray-200 dark:border-gray-700 bg-white dark:bg-gray-800 p-3 min-w-0">
      <div className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400 truncate">
        {health && <span className="w-2 h-2 rounded-full shrink-0" style={{ backgroundColor: colour }} />}
        {label}
      </div>
      <div className="flex items-end justify-between gap-2 mt-1">
        <div className="text-xl font-bold text-gray-900 dark:text-gray-100 tabular-nums truncate">{value}</div>
        {spark && <Sparkline values={spark} colour={colour} />}
      </div>
      {sub && <div className="text-[11px] text-gray-400 mt-0.5 truncate">{sub}</div>}
    </div>
  )
}

function Chart({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rounded-lg border border-gray-200 dark:border-gray-700 p-3">
      <h4 className="text-sm font-semibold text-gray-700 dark:text-gray-300 mb-3">{title}</h4>
      <div className="h-56">{children}</div>
    </div>
  )
}

function uptime(seconds: number, day: string): string {
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d} ${day} ${h} h`
  if (h > 0) return `${h} h ${m} min`
  return `${m} min`
}

const axis = { fontSize: 11, fill: '#9ca3af' }

// The rule that colours each component when it fires.
const RULE_NODE: Record<string, NodeId> = {
  engine_down: 'workers', queue: 'workers', latency_p95: 'workers', db_down: 'database', lease_lost: 's3',
  error_rate: 'api', http_5xx: 'api', reference_differs: 'api', key_expiring: 'users', cpu_high: 'api', memory_high: 'api',
}

/** Processor and memory of the task (API and LibreOffice), one point per minute over 24 hours. */
function ResourcesPanel({ system }: { system?: Monitoring['system'] }) {
  const { t, number, locale } = useI18n()
  if (!system) return <p className="text-sm text-gray-500">{t('res.none')}</p>
  const clock = (iso: string) => new Date(iso).toLocaleTimeString(tag(locale), { hour: '2-digit', minute: '2-digit' })
  const points = system.series.map((p) => ({ t: clock(p.t), avg: p.cpuAvg, max: p.cpuMax, mem: p.memMb }))
  const memPct = system.memLimitMb ? Math.round((system.memMb / system.memLimitMb) * 100) : undefined
  const tooltip = { contentStyle: { borderRadius: 8, fontSize: 12 } }
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Tile label={t('res.cpu')} value={`${number(system.cpuPct)} %`} sub={system.cpuLimit ? t('res.cpuLimit', { n: system.cpuLimit }) : undefined} health={system.cpuPct >= 85 ? 'warning' : undefined} />
        <Tile label={t('res.memory')} value={`${number(Math.round(system.memMb))} ${t('unit.mb')}`} sub={system.memLimitMb ? t('res.memLimit', { pct: memPct ?? 0, limit: number(Math.round(system.memLimitMb)) }) : undefined} health={memPct !== undefined && memPct >= 90 ? 'critical' : undefined} />
        <Tile label={t('res.cpuMax')} value={`${number(Math.max(0, ...system.series.map((p) => p.cpuMax)))} %`} sub={t('res.window', { n: system.series.length })} />
        <Tile label={t('res.source')} value={system.source === 'ecs' ? 'ECS' : system.source === 'cgroup' ? 'cgroup' : '—'} sub={t('res.every')} />
      </div>
      {points.length > 1 && (
        <div className="grid lg:grid-cols-2 gap-3">
          <Chart title={t('res.chart.cpu')}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={points} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
                <XAxis dataKey="t" tick={axis} interval={Math.max(0, Math.floor(points.length / 8) - 1)} />
                <YAxis tick={axis} domain={[0, 100]} unit=" %" />
                <Tooltip {...tooltip} />
                <Legend wrapperStyle={{ fontSize: 12 }} />
                <Area type="monotone" dataKey="max" name={t('res.series.max')} stroke="#f59e0b" fill="#f59e0b" fillOpacity={0.1} />
                <Area type="monotone" dataKey="avg" name={t('res.series.avg')} stroke="#6366f1" fill="#6366f1" fillOpacity={0.2} />
              </AreaChart>
            </ResponsiveContainer>
          </Chart>
          <Chart title={t('res.chart.memory')}>
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={points} margin={{ top: 4, right: 8, left: -4, bottom: 0 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
                <XAxis dataKey="t" tick={axis} interval={Math.max(0, Math.floor(points.length / 8) - 1)} />
                <YAxis tick={axis} domain={[0, system.memLimitMb ? Math.round(system.memLimitMb) : 'auto']} />
                <Tooltip {...tooltip} />
                <Area type="monotone" dataKey="mem" name={t('res.series.mem')} stroke="#10b981" fill="#10b981" fillOpacity={0.2} />
              </AreaChart>
            </ResponsiveContainer>
          </Chart>
        </div>
      )}
    </div>
  )
}

/** Live state of the service: map, tiles, charts, alerts. */
export function MonitoringPage() {
  const { t, tr, locale, number, date } = useI18n()
  const format = useRuleValue()
  const [live, setLive] = useState(true)
  const [selected, setSelected] = useState<NodeId | null>(null)
  const [closed, setClosed] = useState<Set<string>>(new Set())
  const toggle = (id: string) => setClosed((s) => { const n = new Set(s); if (n.has(id)) n.delete(id); else n.add(id); return n })
  const { data, error, isPending, dataUpdatedAt, refetch, isFetching } = useGetMonitoring({ query: { refetchInterval: live ? 5_000 : false, staleTime: 0 } })
  const { data: alerts } = useGetAlerts({ query: { refetchInterval: live ? 30_000 : false } })
  // The last measures, for the small curves of the tiles (kept while the page is open).
  const [history, setHistory] = useState<Monitoring[]>([])
  const [seen, setSeen] = useState(0)
  if (data && dataUpdatedAt !== seen) {
    setSeen(dataUpdatedAt)
    setHistory((h) => [...h.slice(-59), data])
  }
  const sparks = (pick: (m: Monitoring) => number) => history.map(pick)
  const clock = (iso: string | number) => new Date(iso).toLocaleTimeString(tag(locale), { hour: '2-digit', minute: '2-digit' })

  const titles: Record<NodeId, string> = {
    users: t('mon.node.users'), assistants: t('mon.node.assistants'), cloudfront: 'Proxy', front: t('mon.node.front'),
    api: t('mon.node.api'), workers: 'LibreOffice', database: t('mon.node.database'), s3: t('mon.node.s3'),
  }
  const firing = (alerts?.states ?? []).filter((s) => s.state === 'firing')

  const map = (m: Monitoring): { nodes: MapNode[]; edges: MapEdge[] } => {
    const fires = (id: NodeId) => firing.some((f) => RULE_NODE[f.rule] === id)
    const apiHealth: Health = m.health === 'down' ? 'critical' : m.health === 'degraded' || m.traffic.errorsPerMinute > 0 ? 'warning' : 'ok'
    const workersHealth: Health = !m.workers.ready ? 'critical' : m.workers.queued > 0 ? 'warning' : 'ok'
    const rep = m.database.replication
    const nodes: MapNode[] = [
      { id: 'users', health: 'neutral', lines: [t('mon.line.rpm', { n: m.traffic.requestsPerMinute })] },
      { id: 'assistants', health: 'neutral', lines: ['MCP · /v1/mcp'] },
      { id: 'cloudfront', health: 'neutral', lines: ['HTTPS', window.location.host.slice(0, 24)] },
      { id: 'front', health: 'ok', lines: [t('mon.line.console')] },
      { id: 'api', health: apiHealth, lines: [`v${m.version.replace(/^v/, '')}`.slice(0, 22), m.system ? t('mon.line.resources', { cpu: number(m.system.cpuPct), mem: number(Math.round(m.system.memMb)) }) : t('mon.line.uptime', { d: uptime(m.uptimeSeconds, t('unit.day')) }), t('mon.line.errors', { n: m.traffic.errorsPerMinute })], firing: fires('api') },
      { id: 'workers', health: workersHealth, lines: [t('mon.line.busy', { busy: m.workers.busy, total: m.workers.total }), t('mon.line.queued', { n: m.workers.queued }), `p95 ${number(m.renders.p95Ms)} ms`], firing: fires('workers') },
      { id: 'database', health: m.database.ok ? 'ok' : 'critical', lines: [m.database.engine, m.database.ok ? t('mon.dbOk') : t('mon.dbDown')], firing: fires('database') },
      {
        id: 's3',
        health: !rep.enabled ? 'neutral' : rep.leaseLost ? 'critical' : 'ok',
        lines: rep.enabled ? ['Litestream', rep.locked ? t('mon.line.locked') : t('mon.line.copy')] : [t('mon.line.noCopy')],
        firing: fires('s3'),
      },
    ]
    const busy = m.traffic.requestsPerMinute > 0
    const edges: MapEdge[] = [
      { from: 'users', to: 'cloudfront', active: busy, label: busy ? `${m.traffic.requestsPerMinute}/min` : undefined },
      { from: 'cloudfront', to: 'api', active: busy },
      { from: 'cloudfront', to: 'front', active: busy },
      { from: 'assistants', to: 'cloudfront', active: false },
      { from: 'api', to: 'workers', active: m.workers.busy > 0, label: m.workers.busy > 0 ? t('mon.line.busyShort', { n: m.workers.busy }) : undefined },
      { from: 'api', to: 'database', active: busy },
      { from: 'database', to: 's3', active: rep.enabled && !rep.leaseLost },
    ]
    return { nodes, edges }
  }

  const details = (m: Monitoring, id: NodeId): [string, string][] => {
    switch (id) {
      case 'api':
        return [[t('mon.health'), t(`mon.health.${m.health}`)], [t('mon.version'), m.version], [t('mon.instance'), m.instance], [t('mon.uptime'), uptime(m.uptimeSeconds, t('unit.day'))], [t('mon.requests'), number(m.traffic.requestsPerMinute)], [t('mon.series.errors'), number(m.traffic.errorsPerMinute)], [t('mon.memory'), `${number(m.memory.heapMb)} / ${number(m.memory.systemMb)} ${t('unit.mb')}`], ['Goroutines', number(m.memory.goroutines)]]
      case 'workers':
        return [[t('mon.workers'), `${m.workers.busy} / ${m.workers.total}`], [t('mon.queuedLabel'), number(m.workers.queued)], [t('mon.renders'), number(m.renders.lastHour)], [t('mon.failures'), number(m.renders.errorsLastHour)], [t('mon.refusedLabel'), number(m.renders.refusedLastHour)], ['p50', `${number(m.renders.p50Ms)} ms`], ['p95', `${number(m.renders.p95Ms)} ms`]]
      case 'database':
        return [[t('mon.database'), m.database.engine], [t('mon.health'), m.database.ok ? t('mon.dbOk') : t('mon.dbDown')], ['Litestream', m.database.replication.enabled ? t('mon.yes') : t('mon.no')]]
      case 's3':
        return [['Litestream', m.database.replication.enabled ? t('mon.yes') : t('mon.no')], [t('mon.target'), m.database.replication.target ?? '—'], [t('mon.lock'), m.database.replication.locked ? t('mon.yes') : t('mon.no')], [t('mon.leaseLost'), m.database.replication.leaseLost ? t('mon.yes') : t('mon.no')]]
      case 'users':
        return [[t('mon.requests'), number(m.traffic.requestsPerMinute)], [t('mon.last24hLabel'), number(m.renders.last24h)]]
      default:
        return [[t('mon.nodeNoData'), '—']]
    }
  }

  const renders = (data?.renders.series ?? []).map((p) => ({ t: clock(p.t), ok: p.count - p.errors, errors: p.errors, p95: p.p95Ms }))
  const traffic = (data?.traffic.series ?? []).map((p) => ({ t: clock(p.t), requests: p.requests, errors: p.errors }))
  const tooltip = { contentStyle: { borderRadius: 8, fontSize: 12 } }
  const graph = map
  const health: Health | undefined = data ? (data.health === 'down' ? 'critical' : data.health === 'degraded' ? 'warning' : 'ok') : undefined

  return (
    <div className="space-y-4">
      <PageTitle>{t('nav.monitoring')}</PageTitle>

      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="inline-flex items-center gap-2 font-medium">
          <span className={`w-2.5 h-2.5 rounded-full ${live ? 'bg-emerald-500 animate-pulse' : 'bg-gray-400'}`} />
          {live ? t('mon.live') : t('mon.paused')}
        </span>
        {dataUpdatedAt > 0 && <span className="text-gray-500">{t('mon.updated', { time: new Date(dataUpdatedAt).toLocaleTimeString(tag(locale)) })}</span>}
        <div className="ml-auto flex gap-2">
          <button className="btn-secondary inline-flex items-center gap-2 py-1.5" onClick={() => setLive((l) => !l)}>
            {live ? <Pause className="w-4 h-4" /> : <Play className="w-4 h-4" />} {live ? t('mon.pause') : t('mon.resume')}
          </button>
          <button className="btn-ghost inline-flex items-center gap-2 py-1.5" onClick={() => void refetch()} aria-label={t('common.refresh')}>
            <RefreshCw className={`w-4 h-4 ${isFetching ? 'animate-spin' : ''}`} /> {t('common.refresh')}
          </button>
        </div>
      </div>

      {error ? (
        <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
      ) : isPending ? (
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      ) : (
        <>
          {data.problems && data.problems.length > 0 && (
            <div className="p-3 rounded-lg bg-amber-50 dark:bg-amber-900/20 text-amber-800 dark:text-amber-300 text-sm">
              <div className="font-medium mb-1">{t('mon.problems')}</div>
              <ul className="list-disc ml-4 space-y-0.5">
                {data.problems.map((p) => (
                  <li key={p}>{p}</li>
                ))}
              </ul>
            </div>
          )}
          {firing.length > 0 && (
            <div className="p-3 rounded-lg bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-900/40 text-sm">
              <div className="flex items-center gap-2 font-semibold text-red-700 dark:text-red-300">
                <AlertTriangle className="w-4 h-4" /> {t('mon.firing', { n: firing.length })}
              </div>
              <ul className="mt-1 space-y-0.5">
                {firing.map((a) => (
                  <li key={a.rule} className="text-gray-700 dark:text-gray-300">
                    <button type="button" className="hover:underline" onClick={() => setSelected(RULE_NODE[a.rule] ?? null)}>
                      {tr(`alerts.rule.${a.rule}`, a.rule)} — {format(a.value, a.unit)} &gt; {format(a.threshold, a.unit)}
                      {a.since ? ` · ${t('mon.since', { time: date(a.since) })}` : ''}
                    </button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          <Section title={t('mon.map')} icon={<Network className="w-4 h-4 text-indigo-500" />} open={!closed.has('map')} onToggle={() => toggle('map')} right={<span className="text-xs text-gray-400 hidden md:inline">{t('mon.mapHint')}</span>}>
            <div className="overflow-x-auto">
              <div className="min-w-[640px]">
                <ArchitectureMap {...graph(data)} titles={titles} selected={selected} onSelect={setSelected} />
              </div>
            </div>
            {selected && (
              <div className="mt-3 rounded-lg border border-gray-200 dark:border-gray-700 p-4">
                <div className="flex items-center justify-between mb-2">
                  <h4 className="font-semibold text-gray-900 dark:text-gray-100">{titles[selected]}</h4>
                  <button className="p-1 rounded text-gray-400 hover:text-gray-600" onClick={() => setSelected(null)} aria-label={t('common.close')}>
                    <X className="w-4 h-4" />
                  </button>
                </div>
                <dl className="grid grid-cols-2 sm:grid-cols-4 gap-x-6 gap-y-2">
                  {details(data, selected).map(([k, v]) => (
                    <div key={k} className="min-w-0">
                      <dt className="text-xs text-gray-500 dark:text-gray-400">{k}</dt>
                      <dd className="text-sm text-gray-900 dark:text-gray-100 break-all">{v}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}
          </Section>

          <div className="grid grid-cols-2 md:grid-cols-4 2xl:grid-cols-8 gap-3">
            <Tile label={t('mon.health')} value={t(`mon.health.${data.health}`)} health={health} sub={data.instance} />
            <Tile label={t('mon.workers')} value={`${data.workers.busy} / ${data.workers.total}`} sub={t('mon.queued', { n: data.workers.queued })} health={data.workers.ready ? undefined : 'critical'} spark={sparks((m) => m.workers.busy)} />
            <Tile label={t('mon.requests')} value={number(data.traffic.requestsPerMinute)} sub={t('mon.errorsPerMinute', { n: data.traffic.errorsPerMinute })} spark={sparks((m) => m.traffic.requestsPerMinute)} />
            <Tile label={t('mon.renders')} value={number(data.renders.lastHour)} sub={t('mon.last24h', { n: number(data.renders.last24h) })} spark={sparks((m) => m.renders.lastHour)} />
            <Tile label={t('mon.p95')} value={`${number(data.renders.p95Ms)} ms`} sub={t('mon.p50', { ms: number(data.renders.p50Ms) })} spark={sparks((m) => m.renders.p95Ms)} />
            <Tile label={t('mon.failures')} value={number(data.renders.errorsLastHour)} sub={t('mon.refused', { n: number(data.renders.refusedLastHour) })} health={data.renders.errorsLastHour > 0 ? 'warning' : undefined} />
            <Tile label={t('mon.memory')} value={`${number(data.memory.heapMb)} ${t('unit.mb')}`} sub={`${number(data.memory.systemMb)} ${t('unit.mb')}`} spark={sparks((m) => m.memory.heapMb)} />
            <Tile label={t('mon.uptime')} value={uptime(data.uptimeSeconds, t('unit.day'))} sub={data.version} />
          </div>

          <Section title={t('res.title')} icon={<Cpu className="w-4 h-4 text-indigo-500" />} open={!closed.has('resources')} onToggle={() => toggle('resources')} right={<span className="text-xs text-gray-400 hidden md:inline">{t('res.hint')}</span>}>
            <ResourcesPanel system={data.system} />
          </Section>

          <Section title={t('mon.documents')} icon={<BarChart3 className="w-4 h-4 text-emerald-500" />} open={!closed.has('docs')} onToggle={() => toggle('docs')}>
            <div className="grid lg:grid-cols-2 gap-3">
              <Chart title={t('mon.chart.renders')}>
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={renders} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
                    <XAxis dataKey="t" tick={axis} interval={9} />
                    <YAxis tick={axis} allowDecimals={false} />
                    <Tooltip {...tooltip} />
                    <Legend wrapperStyle={{ fontSize: 12 }} />
                    <Bar dataKey="ok" name={t('mon.series.documents')} stackId="r" fill="#10b981" />
                    <Bar dataKey="errors" name={t('mon.series.errors')} stackId="r" fill="#ef4444" />
                  </BarChart>
                </ResponsiveContainer>
              </Chart>
              <Chart title={t('mon.chart.duration')}>
                <ResponsiveContainer width="100%" height="100%">
                  <AreaChart data={renders} margin={{ top: 4, right: 8, left: -8, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
                    <XAxis dataKey="t" tick={axis} interval={9} />
                    <YAxis tick={axis} allowDecimals={false} />
                    <Tooltip {...tooltip} />
                    <Area type="monotone" dataKey="p95" name="p95 (ms)" stroke="#8b5cf6" fill="#8b5cf6" fillOpacity={0.15} />
                  </AreaChart>
                </ResponsiveContainer>
              </Chart>
            </div>
          </Section>

          <Section title={t('mon.chart.traffic')} icon={<BarChart3 className="w-4 h-4 text-sky-500" />} open={!closed.has('traffic')} onToggle={() => toggle('traffic')}>
            <Chart title={t('mon.chart.traffic')}>
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={traffic} margin={{ top: 4, right: 8, left: -16, bottom: 0 }}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" vertical={false} />
                  <XAxis dataKey="t" tick={axis} interval={4} />
                  <YAxis tick={axis} allowDecimals={false} />
                  <Tooltip {...tooltip} />
                  <Legend wrapperStyle={{ fontSize: 12 }} />
                  <Area type="monotone" dataKey="requests" name={t('mon.series.requests')} stroke="#0ea5e9" fill="#0ea5e9" fillOpacity={0.15} />
                  <Area type="monotone" dataKey="errors" name={t('mon.series.errors')} stroke="#ef4444" fill="#ef4444" fillOpacity={0.2} />
                </AreaChart>
              </ResponsiveContainer>
            </Chart>
          </Section>

          <Section title={t('alerts.title')} icon={<BellRing className="w-4 h-4 text-red-500" />} open={!closed.has('alerts')} onToggle={() => toggle('alerts')} right={<span className="text-xs text-gray-400 hidden md:inline">{t('alerts.hint')}</span>}>
            <AlertsPanel />
          </Section>

          <Section title="Prometheus" icon={<BarChart3 className="w-4 h-4 text-orange-500" />} open={!closed.has('prom')} onToggle={() => toggle('prom')}>
            <PrometheusPanel />
          </Section>
        </>
      )}
    </div>
  )
}
