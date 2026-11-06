import { Fragment, useState } from 'react'
import { Link } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, KeyRound, Pencil, Plus, Save, X } from 'lucide-react'
import {
  getListApiKeysQueryKey, useCreateApiKey, useListApiKeys, useListSpaces, useRevokeApiKey, useUpdateApiKey, type ApiKey, type ApiKeyCreated,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { EmptyState } from '@/components/common/EmptyState'
import { QuotaBar } from '@/components/config/SpacesSection'
import { HelpTip } from '@/components/help/HelpTip'
import { PageTitle } from '@/components/layout/PageTitle'
import { useI18n, type Key } from '@/i18n/useI18n'
import { CopyButton } from '@/components/common/CopyButton'

const Row = Fragment

const STATUS: Record<ApiKey['status'], { label: Key; colour: string }> = {
  active: { label: 'keys.status.active', colour: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300' },
  expired: { label: 'keys.status.expired', colour: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300' },
  revoked: { label: 'keys.status.revoked', colour: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300' },
}

function message(error: unknown) {
  return error instanceof ApiError ? (error.problem?.detail ?? error.message) : String(error)
}

type Limits = { space: string; daily: string; monthly: string }
const NO_LIMITS: Limits = { space: '0', daily: '0', monthly: '0' }
const limitsBody = (l: Limits) => ({ spaceId: Number(l.space), dailyQuota: Number(l.daily) || 0, monthlyQuota: Number(l.monthly) || 0 })

/** The space and the quotas of a key. */
function LimitsFields({ value, onChange, id }: { value: Limits; onChange: (l: Limits) => void; id: string }) {
  const { t } = useI18n()
  const { data: spaces } = useListSpaces()
  return (
    <>
      <div>
        <label className="label" htmlFor={`${id}-space`}>{t('keys.space')}</label>
        <select id={`${id}-space`} className="input" value={value.space} onChange={(e) => onChange({ ...value, space: e.target.value })}>
          <option value="0">{t('spaces.whole')}</option>
          {spaces?.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
      </div>
      <div>
        <label className="label" htmlFor={`${id}-daily`}>{t('spaces.daily')}</label>
        <input id={`${id}-daily`} className="input" type="number" min={0} value={value.daily} onChange={(e) => onChange({ ...value, daily: e.target.value })} />
      </div>
      <div>
        <label className="label" htmlFor={`${id}-monthly`}>{t('spaces.monthly')}</label>
        <input id={`${id}-monthly`} className="input" type="number" min={0} value={value.monthly} onChange={(e) => onChange({ ...value, monthly: e.target.value })} />
      </div>
    </>
  )
}

export function ApiKeysPage() {
  const { t, date: when } = useI18n()
  const queryClient = useQueryClient()
  const { data: keys, isPending, error } = useListApiKeys()
  const [name, setName] = useState('')
  const [days, setDays] = useState('')
  const [receives, setReceives] = useState(false)
  const [limits, setLimits] = useState<Limits>(NO_LIMITS)
  const [editing, setEditing] = useState<{ id: number; limits: Limits }>()
  const [created, setCreated] = useState<ApiKeyCreated>()
  const { data: spaces } = useListSpaces()
  const refresh = () => queryClient.invalidateQueries({ queryKey: getListApiKeysQueryKey() })
  const create = useCreateApiKey({ mutation: { onSuccess: (r) => { setCreated(r); setName(''); setLimits(NO_LIMITS); refresh() } } })
  const revoke = useRevokeApiKey({ mutation: { onSuccess: refresh } })
  const update = useUpdateApiKey({ mutation: { onSuccess: () => { setEditing(undefined); refresh() } } })
  const spaceName = (id?: number) => (id ? (spaces?.find((s) => s.id === id)?.name ?? `#${id}`) : t('spaces.whole'))

  function changeRate(k: ApiKey) {
    const answer = window.prompt(t('keys.ratePrompt', { name: k.name }), String(k.ratePerMinute ?? 0))
    if (answer === null) return
    const n = Number(answer.trim())
    if (Number.isInteger(n) && n >= 0) update.mutate({ id: k.id, data: { ratePerMinute: n } })
  }
  const durations: [string, string][] = [
    ['', t('keys.default')],
    ['30', t('keys.days', { n: 30 })],
    ['90', t('keys.days', { n: 90 })],
    ['365', t('keys.year')],
    ['730', t('keys.years', { n: 2 })],
  ]

  function submit(e: React.FormEvent) {
    e.preventDefault()
    create.mutate({ data: { name, ...(days ? { expiresInDays: Number(days) } : {}), ...(receives && { receivesTemplates: true }), ...limitsBody(limits) } })
  }

  return (
    <div>
      <PageTitle>{t('nav.keys')}</PageTitle>
      <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">
        {t('keys.intro')} <Link to="/developpeurs" className="text-brand-600 underline">{t('keys.examplesLink')}</Link>.
      </p>

      <form onSubmit={submit} className="card mb-6 grid gap-4 md:grid-cols-[1fr_16rem_auto] md:items-end">
        <div>
          <label className="label" htmlFor="key-name">{t('keys.application')}</label>
          <input id="key-name" className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="billing-app INT" maxLength={100} />
        </div>
        <div>
          <label className="label" htmlFor="key-days">{t('keys.lifetime')}</label>
          <select id="key-days" className="input" value={days} onChange={(e) => setDays(e.target.value)}>
            {durations.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </div>
        <button className="btn-primary inline-flex items-center gap-2 justify-center" disabled={!name.trim() || create.isPending}>
          <Plus className="w-4 h-4" /> {t('keys.create')}
        </button>
        {create.error && <p className="text-sm text-red-700 dark:text-red-400 md:col-span-3">{message(create.error)}</p>}
        <div className="md:col-span-3 grid gap-4 sm:grid-cols-3">
          <LimitsFields id="key" value={limits} onChange={setLimits} />
        </div>
        <label className="flex items-center gap-2 text-sm md:col-span-3">
          <input type="checkbox" checked={receives} onChange={(e) => setReceives(e.target.checked)} /> {t('keys.receives')} <HelpTip>{t('keys.receivesHint')}</HelpTip>
        </label>
      </form>

      {created && (
        <section className="card mb-6 border-amber-300 bg-amber-50 dark:bg-amber-900/20 dark:border-amber-700" aria-live="polite">
          <h2 className="flex items-center gap-2 font-semibold text-amber-800 dark:text-amber-300 mb-2">
            <AlertTriangle className="w-5 h-5" /> {t('keys.copyNow', { name: created.key.name })}
          </h2>
          <div className="flex flex-wrap items-center gap-2">
            <code className="flex-1 min-w-0 break-all bg-white dark:bg-gray-900 border border-amber-200 dark:border-amber-800 rounded-lg px-3 py-2 text-sm" data-testid="secret">{created.secret}</code>
            <CopyButton text={created.secret} label={t('keys.copyKey')} />
          </div>
          <p className="text-sm text-amber-800 dark:text-amber-300 mt-3">{t('keys.store')}</p>
          <button className="btn-ghost text-sm mt-2" onClick={() => setCreated(undefined)}>{t('keys.done')}</button>
        </section>
      )}

      <section className="card">
        <h2 className="text-lg font-semibold mb-4">{t('keys.list')}</h2>
        {isPending && <LoadingSpinner />}
        {error && <p className="text-sm text-red-700">{message(error)}</p>}
        {update.error && <p className="text-sm text-red-700" role="alert">{message(update.error)}</p>}
        {keys && keys.length === 0 && (
          <EmptyState icon={<KeyRound className="w-10 h-10" />} title={t('keys.none')} description={t('keys.noneHint')} />
        )}
        {keys && keys.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="text-left text-xs uppercase tracking-wider text-gray-400">
                <tr>
                  <th className="py-2 pr-3">{t('keys.application')}</th><th className="py-2 pr-3">{t('keys.col.key')}</th><th className="py-2 pr-3">{t('keys.col.created')}</th>
                  <th className="py-2 pr-3">{t('keys.col.used')}</th><th className="py-2 pr-3">{t('keys.col.expires')}</th><th className="py-2 pr-3">{t('keys.rate')}</th><th className="py-2 pr-3 min-w-48">{t('keys.space')}</th><th className="py-2 pr-3">{t('keys.col.status')}</th><th />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                {keys.map((k) => (
                  <Row key={k.id}>
                  <tr>
                    <td className="py-2 pr-3 font-medium">
                      {k.name}
                      {k.scopes.includes('templates') && <span className="ml-2 text-[11px] px-2 py-0.5 rounded-full bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-900/40 dark:text-fuchsia-300">{t('keys.receivesBadge')}</span>}
                    </td>
                    <td className="py-2 pr-3 font-mono text-xs">{k.prefix}_…</td>
                    <td className="py-2 pr-3 text-gray-500">{when(k.createdAt)} · {k.createdBy}</td>
                    <td className="py-2 pr-3 text-gray-500">{when(k.lastUsedAt)}</td>
                    <td className="py-2 pr-3 text-gray-500">{when(k.expiresAt)}</td>
                    <td className="py-2 pr-3 text-gray-500 whitespace-nowrap">
                      {k.ratePerMinute ? k.ratePerMinute : t('keys.rateDefault')}
                      {k.status === 'active' && (
                        <button className="ml-1 align-middle text-gray-400 hover:text-gray-700" aria-label={t('common.edit')} disabled={update.isPending} onClick={() => changeRate(k)}>
                          <Pencil className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </td>
                    <td className="py-2 pr-3">
                      <div className="flex items-center gap-1 text-gray-700 dark:text-gray-200">
                        {spaceName(k.spaceId)}
                        {k.status === 'active' && (
                          <button
                            className="text-gray-400 hover:text-gray-700"
                            aria-label={t('keys.editLimits', { name: k.name })}
                            onClick={() => setEditing({ id: k.id, limits: { space: String(k.spaceId ?? 0), daily: String(k.dailyQuota ?? 0), monthly: String(k.monthlyQuota ?? 0) } })}
                          >
                            <Pencil className="w-3.5 h-3.5" />
                          </button>
                        )}
                      </div>
                      {k.usage && (
                        <div className="mt-1 space-y-1">
                          <QuotaBar used={k.usage.today} quota={k.dailyQuota ?? 0} label={t('spaces.today')} />
                          <QuotaBar used={k.usage.month} quota={k.monthlyQuota ?? 0} label={t('spaces.month')} />
                        </div>
                      )}
                    </td>
                    <td className="py-2 pr-3"><span className={`text-xs px-2 py-0.5 rounded-full ${STATUS[k.status].colour}`}>{t(STATUS[k.status].label)}</span></td>
                    <td className="py-2 text-right">
                      {k.status === 'active' && (
                        <button
                          className="text-sm text-red-600 hover:underline"
                          disabled={revoke.isPending}
                          onClick={() => window.confirm(t('keys.revokeConfirm', { name: k.name })) && revoke.mutate({ id: k.id })}
                        >
                          {t('keys.revoke')}
                        </button>
                      )}
                    </td>
                  </tr>
                  {editing?.id === k.id && (
                    <tr>
                      <td colSpan={9} className="pb-4">
                        <form
                          className="grid gap-3 sm:grid-cols-[1fr_1fr_1fr_auto] sm:items-end rounded-lg bg-gray-50 dark:bg-gray-900/40 p-3"
                          onSubmit={(e) => { e.preventDefault(); update.mutate({ id: k.id, data: limitsBody(editing.limits) }) }}
                        >
                          <LimitsFields id={`edit-${k.id}`} value={editing.limits} onChange={(l) => setEditing({ id: k.id, limits: l })} />
                          <div className="flex gap-2">
                            <button type="button" className="btn-ghost p-2" aria-label={t('common.cancel')} onClick={() => setEditing(undefined)}><X className="w-4 h-4" /></button>
                            <button className="btn-primary inline-flex items-center gap-2 text-sm" disabled={update.isPending}><Save className="w-4 h-4" /> {t('common.save')}</button>
                          </div>
                        </form>
                      </td>
                    </tr>
                  )}
                  </Row>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
