import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, Mail, Save } from 'lucide-react'
import { getGetAlertsQueryKey, useGetAlerts, useSaveAlerts, useTestAlert, type AlertConfig } from '@/api/generated/docibis'
import { useI18n } from '@/i18n/useI18n'

const STATE_STYLE: Record<string, string> = {
  ok: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
  pending: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300',
  firing: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300',
  resolved: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
  disabled: 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400',
}

/** A value of a rule with its unit: 12 %, 2 300 ms, 3 / min, down. */
export function useRuleValue() {
  const { t, number } = useI18n()
  return (value: number, unit: string) => {
    switch (unit) {
      case 'pct':
        return `${number(value, 1)} %`
      case 'ms':
        return `${number(value)} ms`
      case 'per_min':
        return `${number(value)} / min`
      case 'state':
        return value > 0 ? t('alerts.down') : t('alerts.up')
      default:
        return number(value)
    }
  }
}

/** Alert rules, recipients and history,. */
export function AlertsPanel() {
  const { t, tr, date } = useI18n()
  const queryClient = useQueryClient()
  const format = useRuleValue()
  const { data: info, error } = useGetAlerts({ query: { refetchInterval: 30_000 } })
  const [draft, setForm] = useState<AlertConfig | null>(null)
  const [typed, setRecipients] = useState<string | null>(null)
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null)
  const save = useSaveAlerts({
    mutation: {
      onSuccess: (data) => {
        queryClient.setQueryData(getGetAlertsQueryKey(), data)
        setForm(data.config)
        setRecipients(data.config.recipients.join('\n'))
        setMessage({ ok: true, text: t('alerts.saved') })
      },
      onError: (e) => setMessage({ ok: false, text: e.message }),
    },
  })
  const test = useTestAlert({
    mutation: {
      onSuccess: (data) =>
        setMessage(
          data.sent.length > 0
            ? { ok: true, text: t('alerts.testSent', { to: data.sent.join(', ') }) + (data.errors?.length ? ` (${data.errors.join(' ; ')})` : '') }
            : { ok: false, text: (data.errors ?? []).join(' ; ') },
        ),
      onError: (e) => setMessage({ ok: false, text: e.message }),
    },
  })

  if (error) return <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
  if (!info) return <p className="text-sm text-gray-400">{t('common.loading')}</p>
  const form = draft ?? info.config
  const recipients = typed ?? info.config.recipients.join('\n')

  const states = new Map(info.states.map((s) => [s.rule, s]))
  const updateRule = (id: string, key: 'enabled' | 'threshold' | 'forMinutes', value: boolean | number) => {
    const rule = info.rules.find((r) => r.id === id)
    const current = form.rules[id] ?? rule?.defaults
    if (!current) return
    setForm({ ...form, rules: { ...form.rules, [id]: { ...current, [key]: value } } })
  }
  const submit = () => {
    setMessage(null)
    save.mutate({ data: { ...form, recipients: recipients.split(/[\n,;]+/).map((r) => r.trim()).filter(Boolean) } })
  }

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] gap-4">
        <div className="space-y-3">
          <label className="flex items-center gap-2 text-sm font-medium text-gray-800 dark:text-gray-200">
            <input type="checkbox" className="accent-brand-600" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
            {t('alerts.enabled')}
          </label>
          <div>
            <label className="label" htmlFor="alert-recipients">{t('alerts.recipients')}</label>
            <textarea id="alert-recipients" value={recipients} onChange={(e) => setRecipients(e.target.value)} rows={3} className="input font-mono text-sm" placeholder="prenom.nom@exemple.fr" />
            <p className="text-xs text-gray-400 mt-1">{t('alerts.recipientsHint')}</p>
            {!info.emailConfigured && <p className="text-xs text-amber-600 dark:text-amber-400 mt-1">{t('alerts.noEmail')}</p>}
          </div>
          <div>
            <label className="label" htmlFor="alert-teams">{t('alerts.teams')}</label>
            <input id="alert-teams" className="input font-mono text-xs" value={form.teamsWebhook ?? ''} onChange={(e) => setForm({ ...form, teamsWebhook: e.target.value })} placeholder="https://….logic.azure.com/workflows/…" />
            <p className="text-xs text-gray-400 mt-1">{t('alerts.teamsHint')}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button onClick={submit} disabled={save.isPending} className="btn-primary inline-flex items-center gap-2 text-sm">
              {save.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />} {t('common.save')}
            </button>
            <button onClick={() => { setMessage(null); test.mutate() }} disabled={test.isPending} className="btn-secondary inline-flex items-center gap-2 text-sm">
              {test.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Mail className="w-4 h-4" />} {t('alerts.test')}
            </button>
          </div>
          {message && <p className={`text-sm ${message.ok ? 'text-emerald-700 dark:text-emerald-400' : 'text-red-700 dark:text-red-400'}`}>{message.text}</p>}
        </div>

        <div className="min-w-0">
          <div className="text-sm font-medium text-gray-800 dark:text-gray-200 mb-2">{t('alerts.history')}</div>
          {info.events.length === 0 ? (
            <p className="text-sm text-gray-400">{t('alerts.noHistory')}</p>
          ) : (
            <ul className="max-h-64 overflow-y-auto divide-y divide-gray-100 dark:divide-gray-800 text-sm">
              {info.events.map((e) => (
                <li key={e.id} className="py-1.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
                  <span className="tabular-nums text-gray-400 text-xs">{date(e.at)}</span>
                  <span className={`text-[11px] px-1.5 py-0.5 rounded font-medium ${STATE_STYLE[e.state] ?? ''}`}>{t(`alerts.state.${e.state}`)}</span>
                  <span className="text-gray-800 dark:text-gray-200">{tr(`alerts.rule.${e.rule}`, e.rule)}</span>
                  {e.state === 'firing' && (
                    <span className="text-xs text-gray-500 tabular-nums">
                      {format(e.value, e.unit)} &gt; {format(e.threshold, e.unit)}
                    </span>
                  )}
                  <span className="text-xs text-gray-400 ml-auto">{e.notified ? t('alerts.notified', { to: e.notified }) : t('alerts.notNotified')}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-gray-400 border-b border-gray-200 dark:border-gray-700">
              <th className="py-1.5 pr-2 font-medium w-8" />
              <th className="py-1.5 pr-3 font-medium">{t('alerts.rule')}</th>
              <th className="py-1.5 pr-3 font-medium">{t('alerts.threshold')}</th>
              <th className="py-1.5 pr-3 font-medium" title={t('alerts.durationHint')}>{t('alerts.duration')}</th>
              <th className="py-1.5 pr-3 font-medium">{t('alerts.value')}</th>
              <th className="py-1.5 font-medium">{t('alerts.stateCol')}</th>
            </tr>
          </thead>
          <tbody>
            {info.rules.map((rule) => {
              const rc = form.rules[rule.id] ?? rule.defaults
              const st = states.get(rule.id)
              const fixed = rule.min === rule.max
              return (
                <tr key={rule.id} className="border-b border-gray-100 dark:border-gray-800">
                  <td className="py-1.5 pr-2">
                    <input type="checkbox" className="accent-brand-600" checked={rc.enabled} onChange={(e) => updateRule(rule.id, 'enabled', e.target.checked)} aria-label={tr(`alerts.rule.${rule.id}`, rule.id)} />
                  </td>
                  <td className="py-1.5 pr-3">
                    <div className="text-gray-800 dark:text-gray-200">{tr(`alerts.rule.${rule.id}`, rule.id)}</div>
                    <div className={`text-[11px] ${rule.severity === 'critical' ? 'text-red-600 dark:text-red-400' : 'text-amber-600 dark:text-amber-400'}`}>{t(`alerts.severity.${rule.severity}`)}</div>
                  </td>
                  <td className="py-1.5 pr-3 whitespace-nowrap">
                    {fixed ? (
                      <span className="text-xs text-gray-400">{t('alerts.whenDown')}</span>
                    ) : (
                      <input
                        type="number"
                        className="input py-1 px-2 w-24 text-sm"
                        min={rule.min}
                        max={rule.max}
                        value={rc.threshold}
                        onChange={(e) => updateRule(rule.id, 'threshold', Number(e.target.value))}
                        aria-label={`${t('alerts.threshold')} ${tr(`alerts.rule.${rule.id}`, rule.id)}`}
                      />
                    )}
                    {!fixed && <span className="ml-1 text-xs text-gray-400">{rule.unit === 'pct' ? '%' : rule.unit === 'ms' ? 'ms' : rule.unit === 'per_min' ? '/ min' : ''}</span>}
                  </td>
                  <td className="py-1.5 pr-3 whitespace-nowrap">
                    <input
                      type="number"
                      className="input py-1 px-2 w-20 text-sm"
                      min={0}
                      max={1440}
                      value={rc.forMinutes}
                      onChange={(e) => updateRule(rule.id, 'forMinutes', Number(e.target.value))}
                      aria-label={`${t('alerts.duration')} ${tr(`alerts.rule.${rule.id}`, rule.id)}`}
                    />
                    <span className="ml-1 text-xs text-gray-400">min</span>
                  </td>
                  <td className="py-1.5 pr-3 tabular-nums text-xs text-gray-600 dark:text-gray-300">{st ? format(st.value, rule.unit) : '—'}</td>
                  <td className="py-1.5">
                    {st && <span className={`text-[11px] px-1.5 py-0.5 rounded font-medium ${STATE_STYLE[st.state] ?? ''}`}>{t(`alerts.state.${st.state}`)}</span>}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
