import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { GitMerge, X } from 'lucide-react'
import {
  getGetTemplateQueryKey, getListTemplatesQueryKey, useDeleteTemplateAlias, useListTemplates, useMergeTemplate, useRenameTemplate,
  type LibraryTemplate, type TemplateDetail,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { HelpTip } from '@/components/help/HelpTip'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

/** The path of a template, its former names, which still find it, and the merge of another template into it. */
export function NamesPanel({ tpl, aliases, editor, admin, onChange }: {
  tpl: LibraryTemplate; aliases: string[]; editor: boolean; admin: boolean; onChange: (d: TemplateDetail) => void
}) {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const [path, setPath] = useState<string>()
  const [from, setFrom] = useState('')
  const rename = useRenameTemplate({ mutation: { onSuccess: (d) => { setPath(undefined); onChange(d) } } })
  const merge = useMergeTemplate({ mutation: { onSuccess: (d) => { setFrom(''); onChange(d) } } })
  const unalias = useDeleteTemplateAlias({
    mutation: {
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey: getGetTemplateQueryKey(tpl.id) })
        queryClient.invalidateQueries({ queryKey: getListTemplatesQueryKey() })
      },
    },
  })
  const { data: library } = useListTemplates(undefined, { query: { enabled: admin } })
  const others = (library ?? []).filter((x) => x.id !== tpl.id && x.kind === tpl.kind)
  const error = rename.error ?? merge.error ?? unalias.error

  return (
    <section className="card space-y-4">
      <div>
        <label className="label" htmlFor="tpl-path">{t('lib.path')} <HelpTip>{t('tip.rename')}</HelpTip></label>
        <div className="flex gap-2">
          <input id="tpl-path" className="input font-mono" disabled={!editor} value={path ?? tpl.path} onChange={(e) => setPath(e.target.value)} maxLength={300} />
          {path !== undefined && path.trim() !== tpl.path && (
            <button className="btn-primary" disabled={rename.isPending} onClick={() => rename.mutate({ id: tpl.id, data: { path: path.trim() } })}>{t('lib.rename')}</button>
          )}
        </div>
      </div>
      {aliases.length > 0 && (
        <div>
          <p className="label">{t('lib.aliases')} <HelpTip>{t('tip.aliases')}</HelpTip></p>
          <ul className="space-y-1">
            {aliases.map((a) => (
              <li key={a} className="flex items-center gap-2 text-sm">
                <span className="font-mono break-all text-gray-600 dark:text-gray-300">{a}</span>
                {admin && (
                  <button className="btn-ghost py-0.5 text-xs ml-auto inline-flex items-center gap-1" disabled={unalias.isPending} aria-label={t('lib.aliasRemove', { name: a })}
                    onClick={() => window.confirm(t('lib.aliasRemoveConfirm', { name: a })) && unalias.mutate({ id: tpl.id, params: { name: a } })}>
                    <X className="w-3.5 h-3.5" /> {t('lib.aliasRemoveShort')}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
      {admin && others.length > 0 && (
        <div>
          <label className="label" htmlFor="tpl-merge">{t('lib.merge')} <HelpTip>{t('tip.merge')}</HelpTip></label>
          <div className="flex gap-2">
            <select id="tpl-merge" className="input font-mono" value={from} onChange={(e) => setFrom(e.target.value)}>
              <option value="">{t('lib.mergeChoose')}</option>
              {others.map((x) => <option key={x.id} value={x.id}>{x.path}</option>)}
            </select>
            <button className="btn-secondary inline-flex items-center gap-2" disabled={!from || merge.isPending}
              onClick={() => {
                const other = others.find((x) => String(x.id) === from)
                if (other && window.confirm(t('lib.mergeConfirm', { from: other.path, into: tpl.path }))) merge.mutate({ id: tpl.id, data: { from: other.id } })
              }}>
              <GitMerge className="w-4 h-4" /> {t('lib.mergeDo')}
            </button>
          </div>
        </div>
      )}
      {error && <p className="text-sm text-red-700" role="alert">{message(error)}</p>}
    </section>
  )
}
