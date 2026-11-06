import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, Pencil, Plus, Save, Trash2, X } from 'lucide-react'
import { getListSpacesQueryKey, useCreateSpace, useDeleteSpace, useListSpaces, useUpdateSpace, type Space } from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { SettingsCard } from '@/components/common/SettingsCard'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

type Draft = { id?: number; name: string; prefixes: string; daily: string; monthly: string }
const EMPTY: Draft = { name: '', prefixes: '', daily: '0', monthly: '0' }

/** Documents used against a quota, as a bar when there is one. */
export function QuotaBar({ used, quota, label }: { used: number; quota: number; label: string }) {
  const { t, number } = useI18n()
  const share = quota > 0 ? Math.min(used / quota, 1) : 0
  return (
    <div className="text-xs">
      <div className="flex justify-between gap-2 text-gray-500 dark:text-gray-400">
        <span>{label}</span>
        <span className={share >= 1 ? 'text-red-600 font-medium' : ''}>{number(used)}{quota > 0 ? ` / ${number(quota)}` : ` · ${t('spaces.noLimit')}`}</span>
      </div>
      {quota > 0 && (
        <div className="mt-1 h-1.5 rounded-full bg-gray-100 dark:bg-gray-700 overflow-hidden">
          <div className={`h-full ${share >= 1 ? 'bg-red-500' : share >= 0.8 ? 'bg-amber-500' : 'bg-emerald-500'}`} style={{ width: `${share * 100}%` }} />
        </div>
      )}
    </div>
  )
}

/** The spaces of the teams: their templates by the start of the paths, and the quotas of their keys. */
export function SpacesSection() {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const { data, error } = useListSpaces()
  const [draft, setDraft] = useState<Draft>()
  const refresh = () => queryClient.invalidateQueries({ queryKey: getListSpacesQueryKey() })
  const done = { mutation: { onSuccess: () => { setDraft(undefined); void refresh() } } }
  const create = useCreateSpace(done)
  const update = useUpdateSpace(done)
  const remove = useDeleteSpace({ mutation: { onSuccess: () => void refresh() } })

  if (error) return <div className="card text-red-700">{t('common.unavailable', { message: message(error) })}</div>
  if (!data) return <Loader2 className="w-5 h-5 animate-spin text-gray-400" />

  const edit = (s: Space) => setDraft({ id: s.id, name: s.name, prefixes: s.prefixes.join('\n'), daily: String(s.dailyQuota), monthly: String(s.monthlyQuota) })
  const save = (e: React.FormEvent) => {
    e.preventDefault()
    if (!draft) return
    const body = {
      name: draft.name,
      prefixes: draft.prefixes.split('\n').map((p) => p.trim()).filter(Boolean),
      dailyQuota: Number(draft.daily) || 0,
      monthlyQuota: Number(draft.monthly) || 0,
    }
    if (draft.id) update.mutate({ id: draft.id, data: body })
    else create.mutate({ data: body })
  }
  const failure = create.error ?? update.error ?? remove.error
  const busy = create.isPending || update.isPending

  return (
    <SettingsCard
      title={t('spaces.title')}
      actions={!draft && (
        <button type="button" className="btn-primary inline-flex items-center gap-2 text-sm" onClick={() => setDraft(EMPTY)}>
          <Plus className="w-4 h-4" /> {t('spaces.add')}
        </button>
      )}
    >
      <p className="mb-4 max-w-3xl">{t('spaces.help')}</p>
      {failure && <p className="text-sm text-red-700 mb-3" role="alert">{message(failure)}</p>}

      {draft && (
        <form onSubmit={save} className="mb-6 rounded-xl border border-gray-200 dark:border-gray-700 p-4 grid gap-4 md:grid-cols-2">
          <div>
            <label className="label" htmlFor="space-name">{t('spaces.name')}</label>
            <input id="space-name" className="input" maxLength={60} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="Habitation" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label" htmlFor="space-daily">{t('spaces.daily')}</label>
              <input id="space-daily" className="input" type="number" min={0} value={draft.daily} onChange={(e) => setDraft({ ...draft, daily: e.target.value })} />
            </div>
            <div>
              <label className="label" htmlFor="space-monthly">{t('spaces.monthly')}</label>
              <input id="space-monthly" className="input" type="number" min={0} value={draft.monthly} onChange={(e) => setDraft({ ...draft, monthly: e.target.value })} />
            </div>
          </div>
          <div className="md:col-span-2">
            <label className="label" htmlFor="space-prefixes">{t('spaces.prefixes')}</label>
            <textarea id="space-prefixes" className="input min-h-20 font-mono text-sm" value={draft.prefixes} onChange={(e) => setDraft({ ...draft, prefixes: e.target.value })} placeholder={'habitation/\nclauses/'} />
            <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">{t('spaces.prefixesHelp')}</p>
          </div>
          <div className="md:col-span-2 flex flex-wrap gap-2 justify-end">
            <button type="button" className="btn-ghost inline-flex items-center gap-2 text-sm" onClick={() => setDraft(undefined)}><X className="w-4 h-4" /> {t('common.cancel')}</button>
            <button className="btn-primary inline-flex items-center gap-2 text-sm" disabled={!draft.name.trim() || busy}>
              {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />} {t('common.save')}
            </button>
          </div>
        </form>
      )}

      {data.length === 0 ? (
        <p className="text-gray-500 dark:text-gray-400">{t('spaces.none')}</p>
      ) : (
        <ul className="grid gap-3 lg:grid-cols-2">
          {data.map((s) => (
            <li key={s.id} className="rounded-xl border border-gray-200 dark:border-gray-700 p-4 space-y-3">
              <div className="flex items-start gap-2">
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-gray-900 dark:text-gray-100">{s.name}</div>
                  <div className="text-xs text-gray-500 dark:text-gray-400">{t('spaces.counts', { templates: s.templates, keys: s.keys })}</div>
                </div>
                <button type="button" className="p-1.5 text-gray-400 hover:text-gray-700 dark:hover:text-gray-200" aria-label={t('common.edit')} onClick={() => edit(s)}><Pencil className="w-4 h-4" /></button>
                <button
                  type="button"
                  className="p-1.5 text-gray-400 hover:text-red-600"
                  aria-label={t('common.delete')}
                  disabled={remove.isPending}
                  onClick={() => window.confirm(t('spaces.deleteConfirm', { name: s.name })) && remove.mutate({ id: s.id })}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
              <div className="flex flex-wrap gap-1.5">
                {s.prefixes.length === 0
                  ? <span className="text-xs px-2 py-0.5 rounded-md bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300">{t('spaces.whole')}</span>
                  : s.prefixes.map((p) => <code key={p} className="text-xs px-2 py-0.5 rounded-md bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200">{p}</code>)}
              </div>
              <QuotaBar used={s.usage.today} quota={s.dailyQuota} label={t('spaces.today')} />
              <QuotaBar used={s.usage.month} quota={s.monthlyQuota} label={t('spaces.month')} />
            </li>
          ))}
        </ul>
      )}
    </SettingsCard>
  )
}
