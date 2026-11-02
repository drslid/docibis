import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ArrowDown, ArrowUp, Download, Layers, Loader2, Pencil, Play, Plus, Trash2 } from 'lucide-react'
import {
  getListBundlesQueryKey, useCreateBundle, useDeleteBundle, useListBundles, useListTemplates, useUpdateBundle,
  type Bundle, type BundleInput, type BundleItem,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { CopyButton } from '@/components/common/CopyButton'
import { EmptyState } from '@/components/common/EmptyState'
import { Modal } from '@/components/common/Modal'
import { HelpTip } from '@/components/help/HelpTip'
import { PageTitle } from '@/components/layout/PageTitle'
import { atLeast, useMe } from '@/hooks/useMe'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

function Editor({ bundle, onClose }: { bundle?: Bundle; onClose: () => void }) {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const { data: templates } = useListTemplates()
  const [draft, setDraft] = useState<BundleInput>(() => bundle
    ? { name: bundle.name, description: bundle.description, output: bundle.output, items: bundle.items }
    : { name: '', description: '', output: 'pdf', items: [{ template: '' }] })
  const done = () => { void queryClient.invalidateQueries({ queryKey: getListBundlesQueryKey() }); onClose() }
  const create = useCreateBundle({ mutation: { onSuccess: done } })
  const update = useUpdateBundle({ mutation: { onSuccess: done } })
  const saving = create.isPending || update.isPending
  const failure = create.error ?? update.error
  const setItem = (i: number, item: BundleItem) => setDraft((d) => ({ ...d, items: d.items.map((x, j) => (j === i ? item : x)) }))
  const move = (i: number, by: number) => setDraft((d) => {
    const items = [...d.items]
    const [x] = items.splice(i, 1)
    items.splice(i + by, 0, x)
    return { ...d, items }
  })
  const save = () => {
    const data = { ...draft, items: draft.items.map((x) => ({ template: x.template, ...(x.condition?.trim() && { condition: x.condition.trim() }), ...(x.dataPath?.trim() && { dataPath: x.dataPath.trim() }) })) }
    if (bundle) update.mutate({ id: bundle.id, data })
    else create.mutate({ data })
  }
  const choices = (templates ?? []).filter((x) => x.published > 0)

  return (
    <Modal title={bundle ? t('bundles.edit', { name: bundle.name }) : t('bundles.new')} onClose={onClose} size="xl"
      footer={
        <div className="flex items-center justify-end gap-2">
          {failure && <span className="text-sm text-red-700 mr-auto" role="alert">{message(failure)}</span>}
          <button className="btn-ghost" onClick={onClose}>{t('common.cancel')}</button>
          <button className="btn-primary inline-flex items-center gap-2" disabled={saving || !draft.name.trim() || draft.items.some((x) => !x.template)} onClick={save}>
            {saving && <Loader2 className="w-4 h-4 animate-spin" />} {t('common.save')}
          </button>
        </div>
      }>
      <div className="space-y-4">
        <div className="grid gap-3 md:grid-cols-[1fr_10rem]">
          <div>
            <label className="label" htmlFor="bundle-name">{t('bundles.name')}</label>
            <input id="bundle-name" className="input font-mono" value={draft.name} maxLength={100} placeholder="souscription-habitation"
              onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
            <p className="text-xs text-gray-400 mt-1">{t('bundles.nameHint')}</p>
          </div>
          <div>
            <label className="label" htmlFor="bundle-output">{t('bundles.output')} <HelpTip>{t('tip.output')}</HelpTip></label>
            <select id="bundle-output" className="input" value={draft.output} onChange={(e) => setDraft({ ...draft, output: e.target.value as BundleInput['output'] })}>
              <option value="pdf">{t('bundles.outputPdf')}</option>
              <option value="zip">{t('bundles.outputZip')}</option>
            </select>
          </div>
        </div>
        <div>
          <label className="label" htmlFor="bundle-description">{t('lib.description')}</label>
          <input id="bundle-description" className="input" value={draft.description ?? ''} maxLength={500} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
        </div>
        <div>
          <div className="label">
            {t('bundles.items')} <HelpTip label={t('tip.conditionLabel')}><span className="block mb-1.5">{t('tip.condition')}</span><span className="block">{t('tip.dataPath')}</span></HelpTip>
          </div>
          <p className="text-xs text-gray-500 mb-2">{t('bundles.itemsHint')}</p>
          <ol className="space-y-2">
            {draft.items.map((item, i) => (
              <li key={i} className="rounded-lg border border-gray-200 dark:border-gray-700 p-3 grid gap-2 md:grid-cols-[1.5rem_minmax(0,2fr)_minmax(0,1.5fr)_minmax(0,1fr)_auto] md:items-center">
                <span className="text-sm text-gray-400 tabular-nums">{i + 1}</span>
                <select className="input py-1.5 text-sm font-mono" aria-label={t('bundles.template', { n: i + 1 })} value={item.template} onChange={(e) => setItem(i, { ...item, template: e.target.value })}>
                  <option value="">{t('bundles.pick')}</option>
                  {choices.map((x) => <option key={x.id} value={x.path}>{x.path}{x.kind === 'pdf' ? ` (${t('lib.kind.pdf')})` : ''}</option>)}
                </select>
                <input className="input py-1.5 text-sm font-mono" aria-label={t('bundles.condition', { n: i + 1 })} placeholder={t('bundles.conditionHint')} value={item.condition ?? ''} onChange={(e) => setItem(i, { ...item, condition: e.target.value })} />
                <input className="input py-1.5 text-sm font-mono" aria-label={t('bundles.dataPath', { n: i + 1 })} placeholder={t('bundles.dataPathHint')} value={item.dataPath ?? ''} onChange={(e) => setItem(i, { ...item, dataPath: e.target.value })} />
                <div className="flex gap-1 justify-end">
                  <button type="button" className="p-1.5 text-gray-400 hover:text-gray-700 disabled:opacity-30" disabled={i === 0} aria-label={t('bundles.up')} onClick={() => move(i, -1)}><ArrowUp className="w-4 h-4" /></button>
                  <button type="button" className="p-1.5 text-gray-400 hover:text-gray-700 disabled:opacity-30" disabled={i === draft.items.length - 1} aria-label={t('bundles.down')} onClick={() => move(i, 1)}><ArrowDown className="w-4 h-4" /></button>
                  <button type="button" className="p-1.5 text-gray-400 hover:text-red-600 disabled:opacity-30" disabled={draft.items.length === 1} aria-label={t('common.delete')} onClick={() => setDraft({ ...draft, items: draft.items.filter((_, j) => j !== i) })}><Trash2 className="w-4 h-4" /></button>
                </div>
              </li>
            ))}
          </ol>
          <button type="button" className="btn-ghost text-sm inline-flex items-center gap-1 mt-2" disabled={draft.items.length >= 30} onClick={() => setDraft({ ...draft, items: [...draft.items, { template: '' }] })}>
            <Plus className="w-4 h-4" /> {t('bundles.addItem')}
          </button>
        </div>
      </div>
    </Modal>
  )
}

function TryBundle({ bundle, onClose }: { bundle: Bundle; onClose: () => void }) {
  const { t } = useI18n()
  const [data, setData] = useState('{\n  \n}')
  const [result, setResult] = useState<{ url: string; zip: boolean }>()
  const [failure, setFailure] = useState('')
  const [busy, setBusy] = useState(false)
  const invalid = (() => {
    try {
      const v: unknown = JSON.parse(data)
      return !v || typeof v !== 'object' || Array.isArray(v)
    } catch {
      return true
    }
  })()
  const run = async () => {
    setBusy(true)
    setFailure('')
    try {
      const response = await fetch(`/api/bundles/${bundle.id}/try`, {
        method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ data: JSON.parse(data) }),
      })
      if (!response.ok) {
        const problem = (await response.json().catch(() => ({}))) as { detail?: string }
        throw new Error(problem.detail ?? `HTTP ${response.status}`)
      }
      if (result) URL.revokeObjectURL(result.url)
      const blob = await response.blob()
      setResult({ url: URL.createObjectURL(blob), zip: blob.type.includes('zip') })
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const close = () => { if (result) URL.revokeObjectURL(result.url); onClose() }
  return (
    <Modal title={t('bundles.tryTitle', { name: bundle.name })} onClose={close} size="xl">
      <div className="grid gap-4 lg:grid-cols-2">
        <div>
          <label className="label" htmlFor="bundle-data">{t('bundles.data')}</label>
          <textarea id="bundle-data" className="input font-mono text-sm min-h-72" spellCheck={false} value={data} onChange={(e) => setData(e.target.value)} />
          {invalid && <p className="text-sm text-red-700 mt-1">{t('cmp.invalid')}</p>}
          <button className="btn-primary inline-flex items-center gap-2 mt-3" disabled={invalid || busy} onClick={() => void run()}>
            {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />} {t('bundles.produce')}
          </button>
          {failure && <p className="text-sm text-red-700 dark:text-red-400 mt-2" role="alert">{failure}</p>}
        </div>
        <div className="min-h-72 flex flex-col">
          {result ? (
            <>
              <a className="btn-secondary inline-flex items-center gap-2 text-sm self-start mb-2" href={result.url} download={`${bundle.name}.${result.zip ? 'zip' : 'pdf'}`}><Download className="w-4 h-4" /> {t('try.download')}</a>
              {!result.zip && <iframe title={t('try.preview')} src={result.url} className="w-full flex-1 min-h-96 rounded-lg border border-gray-200 dark:border-gray-700" />}
            </>
          ) : <p className="text-sm text-gray-400 m-auto">{t('bundles.tryEmpty')}</p>}
        </div>
      </div>
    </Modal>
  )
}

/** Bundles: documents produced together in one call, defined by the business. */
export function BundlesPage() {
  const { t, date } = useI18n()
  const me = useMe()
  const queryClient = useQueryClient()
  const { data, error, isPending } = useListBundles()
  const [editing, setEditing] = useState<Bundle | 'new'>()
  const [trying, setTrying] = useState<Bundle>()
  const remove = useDeleteBundle({ mutation: { onSuccess: () => queryClient.invalidateQueries({ queryKey: getListBundlesQueryKey() }) } })
  const editor = atLeast(me, 'editor')
  const origin = window.location.origin

  return (
    <div>
      <PageTitle>{t('nav.bundles')}</PageTitle>
      <p className="text-sm text-gray-600 dark:text-gray-400 mb-5 max-w-4xl">{t('bundles.intro')}</p>
      {editor && (
        <button className="btn-primary inline-flex items-center gap-2 mb-6" onClick={() => setEditing('new')}><Plus className="w-4 h-4" /> {t('bundles.new')}</button>
      )}
      {error ? (
        <p className="text-sm text-red-700" role="alert">{message(error)}</p>
      ) : isPending ? (
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      ) : data.length === 0 ? (
        <div className="card"><EmptyState icon={<Layers className="w-10 h-10" />} title={t('bundles.none')} description={t('bundles.noneHint')} /></div>
      ) : (
        <div className="grid gap-4 xl:grid-cols-2">
          {data.map((b) => {
            const curl = `curl -H "Authorization: Bearer $DOCIBIS_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '{"data": {…}}' \\\n  -o ${b.name}.${b.output} ${origin}/v1/bundles/${b.name}/render`
            return (
              <section key={b.id} className="card space-y-3">
                <div className="flex flex-wrap items-start gap-2">
                  <div className="min-w-0">
                    <h2 className="text-lg font-semibold font-mono break-all">{b.name}</h2>
                    {b.description && <p className="text-sm text-gray-500">{b.description}</p>}
                  </div>
                  <span className="ml-auto text-xs px-2 py-0.5 rounded-full bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300">{b.output === 'zip' ? t('bundles.outputZip') : t('bundles.outputPdf')}</span>
                </div>
                <ol className="text-sm space-y-1 list-decimal pl-5">
                  {b.items.map((x, i) => (
                    <li key={i}>
                      <span className="font-mono text-xs break-all">{x.template}</span>
                      {x.condition && <span className="text-xs text-sky-700 dark:text-sky-300"> · {t('bundles.if', { condition: x.condition })}</span>}
                      {x.dataPath && <span className="text-xs text-gray-500"> · {t('bundles.with', { path: x.dataPath })}</span>}
                    </li>
                  ))}
                </ol>
                <div className="relative">
                  <pre className="text-xs bg-gray-50 dark:bg-gray-900 rounded-lg p-3 pr-20 overflow-x-auto">{curl}</pre>
                  <CopyButton text={curl} className="absolute top-2 right-2 py-1 px-2 text-xs" />
                </div>
                <div className="flex flex-wrap items-center gap-3 text-sm">
                  <span className="text-xs text-gray-400">{t('bundles.updated', { date: date(b.updatedAt), by: b.updatedBy })}</span>
                  {editor && (
                    <div className="ml-auto flex gap-2">
                      <button className="btn-secondary py-1 inline-flex items-center gap-1" onClick={() => setTrying(b)}><Play className="w-4 h-4" /> {t('bundles.try')}</button>
                      <button className="btn-ghost py-1 inline-flex items-center gap-1" onClick={() => setEditing(b)}><Pencil className="w-4 h-4" /> {t('common.edit')}</button>
                      <button className="btn-ghost py-1 inline-flex items-center gap-1 hover:text-red-600" disabled={remove.isPending}
                        onClick={() => window.confirm(t('bundles.deleteConfirm', { name: b.name })) && remove.mutate({ id: b.id })}>
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  )}
                </div>
              </section>
            )
          })}
        </div>
      )}
      {editing && <Editor bundle={editing === 'new' ? undefined : editing} onClose={() => setEditing(undefined)} />}
      {trying && <TryBundle bundle={trying} onClose={() => setTrying(undefined)} />}
    </div>
  )
}
