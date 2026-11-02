import { useState } from 'react'
import { Link } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, Send } from 'lucide-react'
import { getListPromotionsQueryKey, useListEnvironments, useListPromotions, usePromoteTemplate, type LibraryVersion } from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { HelpTip } from '@/components/help/HelpTip'
import { atLeast, useMe } from '@/hooks/useMe'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

/** Sends a version of the template to another DocIbis (INT, then REC, then PROD), with its history. */
export function PromotePanel({ id, versions, published }: { id: number; versions: LibraryVersion[]; published: number }) {
  const { t, date } = useI18n()
  const me = useMe()
  const queryClient = useQueryClient()
  const { data: environments } = useListEnvironments()
  const { data: history } = useListPromotions(id)
  const [environment, setEnvironment] = useState('')
  const [version, setVersion] = useState(published || versions[0]?.number || 1)
  const [publish, setPublish] = useState(true)
  const promote = usePromoteTemplate({ mutation: { onSuccess: () => queryClient.invalidateQueries({ queryKey: getListPromotionsQueryKey(id) }) } })
  const target = environment || environments?.[0]?.name || ''
  const editor = atLeast(me, 'editor')

  return (
    <div className="space-y-4">
      {environments && environments.length === 0 ? (
        <p className="text-sm text-gray-500">
          {t('promo.none')}{' '}
          {atLeast(me, 'admin') && <Link to="/configuration/environnements" className="text-brand-700 dark:text-brand-400 underline">{t('promo.configure')}</Link>}
        </p>
      ) : editor && environments && (
        <div className="grid gap-3 md:grid-cols-[10rem_8rem_auto_auto] md:items-end">
          <div>
            <label className="label" htmlFor="promo-env">{t('promo.environment')}</label>
            <select id="promo-env" className="input" value={target} onChange={(e) => setEnvironment(e.target.value)}>
              {environments.map((e) => <option key={e.name} value={e.name}>{e.name}</option>)}
            </select>
          </div>
          <div>
            <label className="label" htmlFor="promo-version">{t('promo.version')}</label>
            <select id="promo-version" className="input" value={version} onChange={(e) => setVersion(Number(e.target.value))}>
              {versions.map((v) => <option key={v.number} value={v.number}>v{v.number}{v.number === published ? ` · ${t('lib.served')}` : ''}</option>)}
            </select>
          </div>
          <label className="flex items-center gap-2 text-sm pb-2"><input type="checkbox" checked={publish} onChange={(e) => setPublish(e.target.checked)} /> {t('promo.publish')} <HelpTip>{t('tip.promote')}</HelpTip></label>
          <button className="btn-primary inline-flex items-center gap-2 justify-center" disabled={!target || promote.isPending}
            onClick={() => window.confirm(t('promo.confirm', { v: version, env: target })) && promote.mutate({ id, data: { environment: target, version, publish } })}>
            {promote.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Send className="w-4 h-4" />} {t('promo.send')}
          </button>
        </div>
      )}
      {promote.error && <p className="text-sm text-red-700 dark:text-red-400" role="alert">{message(promote.error)}</p>}
      {promote.data && (
        <p className={`text-sm ${promote.data.status === 'ok' ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`} role="status">
          {promote.data.environment} : {promote.data.detail}
        </p>
      )}
      {history && history.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs uppercase tracking-wider text-gray-400">
              <tr><th className="py-1.5 pr-3">{t('promo.when')}</th><th className="py-1.5 pr-3">{t('promo.version')}</th><th className="py-1.5 pr-3">{t('promo.environment')}</th><th className="py-1.5 pr-3">{t('promo.result')}</th><th className="py-1.5">{t('promo.by')}</th></tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
              {history.map((p) => (
                <tr key={p.id}>
                  <td className="py-1.5 pr-3 whitespace-nowrap text-gray-500">{date(p.createdAt)}</td>
                  <td className="py-1.5 pr-3">v{p.version}</td>
                  <td className="py-1.5 pr-3 font-medium">{p.environment}</td>
                  <td className={`py-1.5 pr-3 ${p.status === 'ok' ? 'text-green-700 dark:text-green-400' : 'text-red-700 dark:text-red-400'}`}>{p.detail}</td>
                  <td className="py-1.5 text-gray-500">{p.actor}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
