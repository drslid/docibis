import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { keepPreviousData, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, CalendarClock, CircleAlert, CircleCheck, CircleX, Download, FileStack, GitCompare, Plus, Rocket, Sparkles, Trash2, Undo2, Upload, X, Zap } from 'lucide-react'
import {
  getGetTemplateQueryKey, getListTemplatesQueryKey, useAddTemplateVersion, useCheckAllTemplates, useCheckTemplateVersion, useCreateTemplate,
  useDeleteTemplate, useGetTemplate, useImportTemplates, useListTemplates, useListTemplateUses, usePublishTemplate, useScheduleTemplate,
  useSetTemplateReference, useUnscheduleTemplate, useUpdateTemplate,
  type CheckResult, type LibraryTemplate, type LibraryVersion, type TemplateDetail, type TemplateUpdateDataCheck,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { CopyButton } from '@/components/common/CopyButton'
import { EmptyState } from '@/components/common/EmptyState'
import { SearchBox, Table, td, th, useDebounced } from '@/components/common/ListTools'
import { PageTitle } from '@/components/layout/PageTitle'
import { HelpTip } from '@/components/help/HelpTip'
import { TagCheatSheet } from '@/components/help/TagCheatSheet'
import { CompareVersions } from '@/components/library/CompareVersions'
import { FastPanel } from '@/components/library/FastPanel'
import { LintBadge, LintList } from '@/components/library/LintList'
import { NamesPanel } from '@/components/library/NamesPanel'
import { NativePanel } from '@/components/library/NativePanel'
import { PromotePanel } from '@/components/library/PromotePanel'
import { TemplateStats } from '@/components/library/TemplateStats'
import { atLeast, useMe } from '@/hooks/useMe'
import { useI18n, type Key } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}


const badge = 'inline-flex items-center gap-1 whitespace-nowrap text-xs font-medium px-2 py-0.5 rounded-full'

// The kind of a template: Word, fixed PDF, or Typst (a product name, not translated).
function kindLabel(kind: string, t: ReturnType<typeof useI18n>['t']): string {
  if (kind === 'typst') return 'Typst'
  return t(kind === 'pdf' ? 'lib.kind.pdf' : 'lib.kind.template')
}
const tones = {
  ok: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300',
  ko: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
  warn: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  grey: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
}

/** The result of the comparison with the reference PDF. */
/** The engine that produces the documents of a template: the overlay, else the Typst copy, once qualified; else LibreOffice. */
function EngineBadge({ tpl }: { tpl: LibraryTemplate }) {
  const { t } = useI18n()
  if (tpl.fast?.state === 'qualified') {
    return <span className="inline-flex items-center gap-1 text-green-700 dark:text-green-400 font-medium"><Zap className="w-3 h-3" />{t('fast.active')}</span>
  }
  if (tpl.native?.state === 'qualified') {
    return <span className="inline-flex items-center gap-1 text-green-700 dark:text-green-400 font-medium"><Rocket className="w-3 h-3" />{t('native.active')}</span>
  }
  return <span className="text-gray-500 dark:text-gray-400" title={tpl.native?.reason ?? tpl.fast?.reason}>{t('try.engine.standard')}</span>
}

function CheckBadge({ check, hasReference }: { check?: CheckResult; hasReference?: boolean }) {
  const { t } = useI18n()
  if (!hasReference) return <span className={`${badge} ${tones.grey}`}>{t('lib.check.none')}</span>
  if (!check) return <span className={`${badge} ${tones.warn}`}>{t('lib.check.pending')}</span>
  if (check.error) return <span className={`${badge} ${tones.ko}`} title={check.error}><CircleAlert className="w-3 h-3" /> {t('lib.check.error')}</span>
  return check.ok
    ? <span className={`${badge} ${tones.ok}`}><CircleCheck className="w-3 h-3" /> {t('lib.check.ok')}</span>
    : <span className={`${badge} ${tones.ko}`}><CircleX className="w-3 h-3" /> {t('lib.check.ko')}</span>
}

function checkText(t: ReturnType<typeof useI18n>['t'], check: CheckResult): string {
  if (check.error) return check.error
  if (check.referencePages !== undefined && check.referencePages !== check.pages) return t('lib.pagesDiffer', { pages: check.pages, ref: check.referencePages })
  const max = Math.max(0, ...(check.permille ?? []))
  return t('lib.compareResult', { pages: check.pages, max: max.toLocaleString(), threshold: check.threshold })
}

function AddTemplate({ onDone }: { onDone: () => void }) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const [file, setFile] = useState<File>()
  const [path, setPath] = useState('')
  const [description, setDescription] = useState('')
  const [publish, setPublish] = useState(true)
  const create = useCreateTemplate({ mutation: { onSuccess: (d) => { onDone(); navigate(`/modeles/${d.template.id}`) } } })

  function submit(e: FormEvent) {
    e.preventDefault()
    if (file) create.mutate({ data: { file, path: path.trim() || file.name, publish, ...(description.trim() && { description: description.trim() }) } })
  }

  return (
    <form onSubmit={submit} className="card mb-6 grid gap-4 md:grid-cols-2">
      <div>
        <label className="label" htmlFor="lib-file">{t('lib.file')}</label>
        <input id="lib-file" type="file" accept=".docx,.odt,.pdf" className="input" onChange={(e) => { const f = e.target.files?.[0]; setFile(f); if (f && !path) setPath(f.name) }} />
      </div>
      <div>
        <label className="label" htmlFor="lib-path">{t('lib.path')} <HelpTip>{t('tip.path')}</HelpTip></label>
        <input id="lib-path" className="input font-mono text-sm" value={path} onChange={(e) => setPath(e.target.value)} placeholder="acme/contracts/fiche_conseil.docx" maxLength={300} />
        <p className="text-xs text-gray-400 mt-1">{t('lib.pathHint')}</p>
      </div>
      <div className="md:col-span-2">
        <label className="label" htmlFor="lib-description">{t('lib.description')}</label>
        <input id="lib-description" className="input" value={description} onChange={(e) => setDescription(e.target.value)} maxLength={500} />
      </div>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={publish} onChange={(e) => setPublish(e.target.checked)} /> {t('lib.publishNow')}</label>
      <div className="flex gap-2 md:justify-end">
        <button type="button" className="btn-ghost" onClick={onDone}>{t('common.cancel')}</button>
        <button className="btn-primary inline-flex items-center gap-2" disabled={!file || create.isPending}><Plus className="w-4 h-4" /> {t('lib.add')}</button>
      </div>
      {create.error && <p className="text-sm text-red-700 dark:text-red-400 md:col-span-2" role="alert">{message(create.error)}</p>}
    </form>
  )
}

function ImportTemplates({ onDone }: { onDone: () => void }) {
  const { t } = useI18n()
  const [file, setFile] = useState<File>()
  const [publish, setPublish] = useState(true)
  const run = useImportTemplates({ mutation: { onSuccess: onDone } })
  const report = run.data

  return (
    <form
      onSubmit={(e) => { e.preventDefault(); if (file) run.mutate({ data: { file, publish } }) }}
      className="card mb-6 space-y-4"
    >
      <div>
        <label className="label" htmlFor="lib-zip">{t('lib.zip')} <HelpTip>{t('tip.zip')}</HelpTip></label>
        <input id="lib-zip" type="file" accept=".zip" className="input" onChange={(e) => setFile(e.target.files?.[0])} />
        <p className="text-xs text-gray-400 mt-1">{t('lib.zipHint')}</p>
      </div>
      <div className="flex flex-wrap items-center gap-4">
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={publish} onChange={(e) => setPublish(e.target.checked)} /> {t('lib.publishNow')}</label>
        <button className="btn-primary inline-flex items-center gap-2 ml-auto" disabled={!file || run.isPending}><Upload className="w-4 h-4" /> {t('lib.import')}</button>
      </div>
      {run.error && <p className="text-sm text-red-700 dark:text-red-400" role="alert">{message(run.error)}</p>}
      {report && (
        <div className="text-sm" aria-live="polite">
          <p className="font-medium">{t('lib.importReport', { created: report.created, updated: report.updated, unchanged: report.unchanged, references: report.references })}</p>
          {!!report.aliases && <p>{t('lib.importAliases', { n: report.aliases })}</p>}
          {report.errors.length > 0 && (
            <>
              <p className="text-red-700 dark:text-red-400 mt-2">{t('lib.importErrors')}</p>
              <ul className="list-disc pl-5 text-red-700 dark:text-red-400">{report.errors.map((e) => <li key={e}>{e}</li>)}</ul>
            </>
          )}
        </div>
      )}
    </form>
  )
}

/** Installs the example templates (DocIbis logo, sample data) under exemples/. */
function InstallExamples({ onDone }: { onDone: (text: string) => void }) {
  const { t } = useI18n()
  const run = useImportTemplates({
    mutation: { onSuccess: (r) => onDone(t('lib.examplesDone', { created: r.created, updated: r.updated, unchanged: r.unchanged })) },
  })
  const [failure, setFailure] = useState('')
  const install = async () => {
    setFailure('')
    try {
      const response = await fetch('/exemples-docibis.zip')
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      const file = new File([await response.blob()], 'exemples-docibis.zip', { type: 'application/zip' })
      run.mutate({ data: { file, publish: true } })
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e))
    }
  }
  return (
    <>
      <button className="btn-secondary inline-flex items-center gap-2" disabled={run.isPending} onClick={() => void install()} title={t('lib.examplesHint')}>
        <Sparkles className="w-4 h-4 text-amber-500" /> {t('lib.examples')}
      </button>
      {(failure || run.error) && <span className="text-sm text-red-700 dark:text-red-400 self-center" role="alert">{failure || message(run.error)}</span>}
    </>
  )
}

/** The library: the templates applications call by name. */
export function LibraryPage() {
  const { t, date } = useI18n()
  const me = useMe()
  const queryClient = useQueryClient()
  const [text, setText] = useState('')
  const [panel, setPanel] = useState<'add' | 'import'>()
  const [notice, setNotice] = useState('')
  const [unused, setUnused] = useState(false)
  const q = useDebounced(text.trim(), 300)
  const { data, error, isPending } = useListTemplates(q ? { q } : undefined, { query: { placeholderData: keepPreviousData } })
  const { data: uses } = useListTemplateUses({ days: 30 }, { query: { retry: false } })
  const used = new Map((uses ?? []).map((u) => [u.id, u]))
  const rows = (data ?? []).filter((tpl) => !unused || (tpl.kind === 'template' && tpl.published > 0 && !used.has(tpl.id)))
  const refresh = () => queryClient.invalidateQueries({ queryKey: getListTemplatesQueryKey() })
  const checkAll = useCheckAllTemplates({ mutation: { onSuccess: (r) => setNotice(t('lib.checkAllStarted', { n: r.templates })) } })
  const editor = atLeast(me, 'editor')

  return (
    <div>
      <PageTitle>{t('nav.library')}</PageTitle>
      <p className="text-sm text-gray-600 dark:text-gray-400 mb-5 max-w-4xl">{t('lib.intro')}</p>

      {editor && (
        <div className="flex flex-wrap gap-2 mb-6">
          <button className="btn-primary inline-flex items-center gap-2" onClick={() => setPanel(panel === 'add' ? undefined : 'add')}><Plus className="w-4 h-4" /> {t('lib.add')}</button>
          <button className="btn-secondary inline-flex items-center gap-2" onClick={() => setPanel(panel === 'import' ? undefined : 'import')}><Upload className="w-4 h-4" /> {t('lib.import')}</button>
          <a className="btn-secondary inline-flex items-center gap-2" href="/api/templates/export" download><Download className="w-4 h-4" /> {t('lib.export')}</a>
          <InstallExamples onDone={(text) => { setNotice(text); void refresh() }} />
          <a className="btn-ghost inline-flex items-center gap-2" href="/exemples-docibis.zip" download><Download className="w-4 h-4" /> {t('lib.examplesZip')}</a>
          {atLeast(me, 'admin') && (
            <button className="btn-ghost inline-flex items-center gap-2" disabled={checkAll.isPending} onClick={() => checkAll.mutate()}><GitCompare className="w-4 h-4" /> {t('lib.checkAll')}</button>
          )}
        </div>
      )}
      {notice && <p className="text-sm text-sky-700 dark:text-sky-300 mb-4" aria-live="polite">{notice}</p>}
      {panel === 'add' && <AddTemplate onDone={() => { setPanel(undefined); refresh() }} />}
      {panel === 'import' && <ImportTemplates onDone={refresh} />}

      <div className="card">
        <div className="flex flex-wrap items-center gap-3 mb-4">
          <SearchBox value={text} onChange={setText} placeholder={t('lib.search')} />
          {data && <span className="text-sm text-gray-500">{rows.length}</span>}
          <label className="flex items-center gap-2 text-sm ml-auto" title={t('lib.unusedHint')}>
            <input type="checkbox" checked={unused} onChange={(e) => setUnused(e.target.checked)} /> {t('lib.unused')}
          </label>
        </div>
        {error ? (
          <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
        ) : isPending ? (
          <p className="text-sm text-gray-500">{t('common.loading')}</p>
        ) : rows.length === 0 ? (
          <EmptyState icon={<FileStack className="w-10 h-10" />} title={unused ? t('lib.noneUnused') : t('lib.none')} description={unused ? '' : t('lib.noneHint')} />
        ) : (
          <Table
            head={
              <>
                <th className={th}>{t('lib.col.path')}</th>
                <th className={th}>{t('lib.col.kind')}</th>
                <th className={th}>{t('lib.col.version')}</th>
                <th className={th}>{t('lib.col.checks')}</th>
                <th className={th}>{t('lib.col.engine')}</th>
                <th className={th}>{t('lib.col.reference')}</th>
                <th className={th} title={t('lib.col.usesHint')}>{t('lib.col.uses')}</th>
                <th className={th}>{t('lib.col.updated')}</th>
              </>
            }
          >
            {rows.map((tpl) => (
              <tr key={tpl.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/30">
                <td className={`${td} font-mono text-xs break-all`}>
                  <Link to={`/modeles/${tpl.id}`} className="text-brand-700 dark:text-brand-400 hover:underline">{tpl.path}</Link>
                  {tpl.description && <div className="font-sans text-gray-500 mt-0.5">{tpl.description}</div>}
                </td>
                <td className={td}><span className={`${badge} ${tones.grey}`}>{kindLabel(tpl.kind, t)}</span></td>
                <td className={`${td} whitespace-nowrap`}>
                  {tpl.published > 0 ? t('lib.published', { n: tpl.published }) : <span className="text-amber-700 dark:text-amber-400">{t('lib.unpublished')}</span>}
                  {tpl.versions > 1 && <span className="text-gray-400"> · {t('lib.versionsCount', { n: tpl.versions })}</span>}
                  {tpl.scheduled && <div className="text-xs text-sky-700 dark:text-sky-300 inline-flex items-center gap-1"><CalendarClock className="w-3 h-3" /> {t('lib.scheduledShort', { n: tpl.scheduled.version, date: date(tpl.scheduled.at) })}</div>}
                </td>
                <td className={td}>{tpl.kind === 'template' && <LintBadge lint={tpl.lint} />}</td>
                <td className={`${td} whitespace-nowrap`}>
                  {tpl.kind === 'template' && tpl.published > 0 && <EngineBadge tpl={tpl} />}
                </td>
                <td className={td}>{tpl.kind === 'template' && <CheckBadge check={tpl.check} hasReference={tpl.hasReference} />}</td>
                <td className={`${td} whitespace-nowrap tabular-nums`}>
                  {tpl.kind === 'template' && (used.get(tpl.id) ? (
                    <span title={t('lib.usesTitle', { errors: used.get(tpl.id)?.errors ?? 0 })}>{used.get(tpl.id)?.count.toLocaleString()}</span>
                  ) : <span className="text-gray-400">0</span>)}
                </td>
                <td className={`${td} whitespace-nowrap text-gray-500`}>{date(tpl.updatedAt)}</td>
              </tr>
            ))}
          </Table>
        )}
      </div>
    </div>
  )
}

function ReferenceForm({ id, number, onDone }: { id: number; number: number; onDone: (d: TemplateDetail) => void }) {
  const { t } = useI18n()
  const [pdf, setPdf] = useState<File>()
  const [data, setData] = useState<File>()
  const upload = useSetTemplateReference({ mutation: { onSuccess: onDone } })
  return (
    <form
      className="grid gap-3 md:grid-cols-[1fr_1fr_auto] md:items-end mt-3"
      onSubmit={async (e) => {
        e.preventDefault()
        if (pdf && data) upload.mutate({ id, number, data: { reference: pdf, data: await data.text() } })
      }}
    >
      <div>
        <label className="label" htmlFor={`ref-pdf-${number}`}>{t('lib.referencePdf')}</label>
        <input id={`ref-pdf-${number}`} type="file" accept=".pdf" className="input" onChange={(e) => setPdf(e.target.files?.[0])} />
      </div>
      <div>
        <label className="label" htmlFor={`ref-data-${number}`}>{t('lib.referenceData')}</label>
        <input id={`ref-data-${number}`} type="file" accept=".json" className="input" onChange={(e) => setData(e.target.files?.[0])} />
      </div>
      <button className="btn-primary" disabled={!pdf || !data || upload.isPending}>{t('lib.setReference')}</button>
      {upload.error && <p className="text-sm text-red-700 dark:text-red-400 md:col-span-3" role="alert">{message(upload.error)}</p>}
    </form>
  )
}

function VersionRow({ tpl, v, editor, onChange }: { tpl: TemplateDetail['template']; v: LibraryVersion; editor: boolean; onChange: (d: TemplateDetail) => void }) {
  const { t, date } = useI18n()
  const queryClient = useQueryClient()
  const [reference, setReference] = useState(false)
  const [planning, setPlanning] = useState(false)
  const [when, setWhen] = useState(() => {
    const d = new Date(Date.now() + 86_400_000)
    d.setHours(0, 0, 0, 0)
    return new Date(d.getTime() - d.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
  })
  const publish = usePublishTemplate({ mutation: { onSuccess: onChange } })
  const schedule = useScheduleTemplate({ mutation: { onSuccess: (d) => { setPlanning(false); onChange(d) } } })
  const check = useCheckTemplateVersion({ mutation: { onSuccess: () => queryClient.invalidateQueries({ queryKey: getGetTemplateQueryKey(tpl.id) }) } })
  const served = v.number === tpl.published
  return (
    <tr className={served ? 'bg-brand-50/50 dark:bg-brand-900/10' : undefined}>
      <td className={`${td} whitespace-nowrap font-semibold`}>
        v{v.number} {served && <span className={`${badge} ${tones.ok} ml-1`}>{t('lib.served')}</span>}
      </td>
      <td className={`${td} whitespace-nowrap text-gray-500`}>{date(v.createdAt)} · {v.createdBy}</td>
      <td className={`${td} text-gray-600 dark:text-gray-300`}>{v.note || '—'}</td>
      <td className={`${td} whitespace-nowrap text-gray-500`}>{Math.ceil(v.size / 1024)} {t('unit.kb')}</td>
      <td className={td}>
        {tpl.kind === 'template' && (
          <>
            <CheckBadge check={v.check} hasReference={v.hasReference} />
            {v.check && <div className="text-xs text-gray-500 mt-1">{checkText(t, v.check)}</div>}
            {check.error && <div className="text-xs text-red-700 mt-1">{message(check.error)}</div>}
            {reference && <ReferenceForm id={tpl.id} number={v.number} onDone={(d) => { setReference(false); onChange(d) }} />}
          </>
        )}
      </td>
      <td className={`${td} text-right whitespace-nowrap space-x-3`}>
        <a className="text-sm text-brand-700 dark:text-brand-400 hover:underline" href={`/api/templates/${tpl.id}/versions/${v.number}/file`} download>{t('lib.download')}</a>
        {editor && !served && (
          <button className="text-sm text-brand-700 dark:text-brand-400 hover:underline" disabled={publish.isPending}
            onClick={() => window.confirm(t('lib.publishConfirm', { n: v.number })) && publish.mutate({ id: tpl.id, data: { version: v.number } })}>
            {v.number < tpl.published ? t('lib.rollback') : t('lib.publish')}
          </button>
        )}
        {editor && !served && (
          <button className="text-sm text-brand-700 dark:text-brand-400 hover:underline" onClick={() => setPlanning(!planning)}>{t('lib.schedule')}</button>
        )}
        {editor && tpl.kind === 'template' && (
          <button className="text-sm text-brand-700 dark:text-brand-400 hover:underline" onClick={() => setReference(!reference)}>{t('lib.setReference')}</button>
        )}
        {editor && v.hasReference && (
          <button className="text-sm text-brand-700 dark:text-brand-400 hover:underline" disabled={check.isPending} onClick={() => check.mutate({ id: tpl.id, number: v.number })}>
            {t('lib.compare')}
          </button>
        )}
        {planning && (
          <form className="flex flex-wrap items-center justify-end gap-2 mt-2" onSubmit={(e) => { e.preventDefault(); schedule.mutate({ id: tpl.id, data: { version: v.number, at: new Date(when).toISOString() } }) }}>
            <label className="sr-only" htmlFor={`plan-${v.number}`}>{t('lib.scheduleAt')}</label>
            <input id={`plan-${v.number}`} type="datetime-local" className="input w-auto py-1 text-sm" value={when} onChange={(e) => setWhen(e.target.value)} required />
            <button className="btn-primary py-1 text-sm" disabled={!when || schedule.isPending}>{t('lib.scheduleSave')}</button>
            {schedule.error && <span className="basis-full text-right text-xs text-red-700" role="alert">{message(schedule.error)}</span>}
          </form>
        )}
      </td>
    </tr>
  )
}

function NewVersion({ id, onDone }: { id: number; onDone: (d: TemplateDetail) => void }) {
  const { t } = useI18n()
  const [file, setFile] = useState<File>()
  const [note, setNote] = useState('')
  const [publish, setPublish] = useState(true)
  const add = useAddTemplateVersion({ mutation: { onSuccess: (d) => { setFile(undefined); setNote(''); onDone(d) } } })
  return (
    <form
      className="grid gap-3 md:grid-cols-[1fr_1fr_auto_auto] md:items-end"
      onSubmit={(e) => { e.preventDefault(); if (file) add.mutate({ id, data: { file, publish, ...(note.trim() && { note: note.trim() }) } }) }}
    >
      <div>
        <label className="label" htmlFor="version-file">{t('lib.file')}</label>
        <input id="version-file" type="file" accept=".docx,.odt,.pdf" className="input" onChange={(e) => setFile(e.target.files?.[0])} />
      </div>
      <div>
        <label className="label" htmlFor="version-note">{t('lib.note')}</label>
        <input id="version-note" className="input" value={note} onChange={(e) => setNote(e.target.value)} maxLength={300} />
      </div>
      <label className="flex items-center gap-2 text-sm pb-2"><input type="checkbox" checked={publish} onChange={(e) => setPublish(e.target.checked)} /> {t('lib.publishNow')}</label>
      <button className="btn-primary inline-flex items-center gap-2" disabled={!file || add.isPending}><Upload className="w-4 h-4" /> {t('lib.newVersion')}</button>
      {add.error && <p className="text-sm text-red-700 dark:text-red-400 md:col-span-4" role="alert">{message(add.error)}</p>}
    </form>
  )
}

/** One template: its versions, what it reads and its reference. */
export function TemplatePage() {
  const { t, date } = useI18n()
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const id = Number(useParams().id)
  const { data, error, isPending } = useGetTemplate(id)
  const [description, setDescription] = useState<string>()
  const set = (d: TemplateDetail) => {
    queryClient.setQueryData(getGetTemplateQueryKey(id), d)
    queryClient.invalidateQueries({ queryKey: getListTemplatesQueryKey() })
  }
  const update = useUpdateTemplate({ mutation: { onSuccess: (d) => { setDescription(undefined); set(d) } } })
  const remove = useDeleteTemplate({ mutation: { onSuccess: () => { queryClient.invalidateQueries({ queryKey: getListTemplatesQueryKey() }); navigate('/modeles') } } })
  const unschedule = useUnscheduleTemplate({ mutation: { onSuccess: set } })
  const back = usePublishTemplate({ mutation: { onSuccess: set } })
  const editor = atLeast(me, 'editor')
  const admin = atLeast(me, 'admin')

  if (error) return <p className="text-sm text-red-700" role="alert">{message(error)}</p>
  if (isPending) return <p className="text-sm text-gray-500">{t('common.loading')}</p>
  const tpl = data.template
  const origin = window.location.origin
  const curl = `curl -H "Authorization: Bearer $DOCIBIS_KEY" \\\n  -F templateName=${tpl.path} \\\n  -F data=@donnees.json \\\n  -o document.pdf ${origin}/v1/render`

  return (
    <div className="space-y-6">
      <div>
        <Link to="/modeles" className="text-sm text-gray-500 hover:text-gray-700 inline-flex items-center gap-1 mb-2"><ArrowLeft className="w-4 h-4" /> {t('lib.back')}</Link>
        <PageTitle crumbs={[{ label: t('nav.library'), to: '/modeles' }, { label: <span className="font-mono">{tpl.path}</span> }]}>
          <span className="font-mono text-xl break-all">{tpl.path}</span>
        </PageTitle>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className={`${badge} ${tones.grey}`}>{kindLabel(tpl.kind, t)}</span>
          {tpl.published > 0 ? <span>{t('lib.published', { n: tpl.published })}</span> : <span className="text-amber-700">{t('lib.unpublished')}</span>}
          {tpl.kind === 'template' && tpl.published > 0 && <span className="text-gray-500">· <EngineBadge tpl={tpl} /></span>}
          {tpl.kind === 'template' && <CheckBadge check={tpl.check} hasReference={tpl.hasReference} />}
          {tpl.kind === 'template' && <LintBadge lint={tpl.lint} />}
          {editor && tpl.previousPublished && (
            <button className="btn-ghost py-1 text-sm inline-flex items-center gap-1" disabled={back.isPending}
              onClick={() => window.confirm(t('lib.backConfirm', { n: tpl.previousPublished ?? 0 })) && back.mutate({ id, data: { version: tpl.previousPublished ?? 0 } })}>
              <Undo2 className="w-4 h-4" /> {t('lib.backTo', { n: tpl.previousPublished })}
            </button>
          )}
          <CopyButton text={tpl.path} className="ml-auto" />
        </div>
        {tpl.scheduled && (
          <div className="mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-sky-50 dark:bg-sky-900/20 text-sky-800 dark:text-sky-200 px-3 py-2 text-sm" role="status">
            <CalendarClock className="w-4 h-4" />
            {t('lib.scheduled', { n: tpl.scheduled.version, date: date(tpl.scheduled.at), by: tpl.scheduled.by ?? '' })}
            {editor && (
              <button className="ml-auto inline-flex items-center gap-1 hover:underline" disabled={unschedule.isPending} onClick={() => unschedule.mutate({ id })}>
                <X className="w-4 h-4" /> {t('lib.unschedule')}
              </button>
            )}
          </div>
        )}
        {(back.error || unschedule.error) && <p className="text-sm text-red-700 mt-2" role="alert">{message(back.error ?? unschedule.error)}</p>}
      </div>

      <section className="card grid gap-4 md:grid-cols-[1fr_24rem]">
        <div>
          <label className="label" htmlFor="tpl-description">{t('lib.description')}</label>
          <div className="flex gap-2">
            <input id="tpl-description" className="input" disabled={!editor} value={description ?? tpl.description} onChange={(e) => setDescription(e.target.value)} maxLength={500} />
            {description !== undefined && description !== tpl.description && (
              <button className="btn-primary" disabled={update.isPending} onClick={() => update.mutate({ id, data: { description } })}>{t('common.save')}</button>
            )}
          </div>
        </div>
        {tpl.kind === 'template' && (
          <div>
            <label className="label" htmlFor="tpl-check">{t('lib.dataCheck')} <HelpTip>{t('tip.dataCheck')}</HelpTip></label>
            <select id="tpl-check" className="input" disabled={!editor || update.isPending} value={tpl.dataCheck}
              onChange={(e) => update.mutate({ id, data: { dataCheck: e.target.value as TemplateUpdateDataCheck } })}>
              <option value="off">{t('lib.dataCheck.off')}</option>
              <option value="warn">{t('lib.dataCheck.warn')}</option>
              <option value="strict">{t('lib.dataCheck.strict')}</option>
            </select>
          </div>
        )}
        {update.error && <p className="text-sm text-red-700 md:col-span-2" role="alert">{message(update.error)}</p>}
      </section>

      <NamesPanel tpl={tpl} aliases={data.aliases} editor={editor} admin={admin} onChange={set} />

      {tpl.kind === 'template' && (
        <section className="card">
          <div className="flex items-center justify-between mb-2">
            <h2 className="text-lg font-semibold">{t('lib.useIt')}</h2>
            <CopyButton text={curl} />
          </div>
          <pre className="text-xs bg-gray-50 dark:bg-gray-900 rounded-lg p-3 overflow-x-auto">{curl}</pre>
        </section>
      )}

      {tpl.kind === 'template' && (
        <FastPanel id={id} version={data.versions.find((v) => v.number === tpl.published)} editor={editor} />
      )}

      {tpl.kind === 'template' && (
        <NativePanel id={id} version={data.versions.find((v) => v.number === tpl.published)} editor={editor} admin={admin} />
      )}

      {tpl.kind === 'template' && (
        <section className="card">
          <h2 className="text-lg font-semibold mb-3">{t('lib.fields')} <span className="text-sm font-normal text-gray-400">({data.fields.length})</span></h2>
          {data.fields.length === 0 ? (
            <p className="text-sm text-gray-500">{t('lib.fieldsNone')}</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {data.fields.map((f) => (
                <span key={f.path} className={`${badge} ${f.optional ? tones.grey : 'bg-sky-50 text-sky-800 dark:bg-sky-900/30 dark:text-sky-300'} font-mono`}
                  title={`${t(`lib.field.${f.kind}` as Key)}${f.optional ? ` · ${t('lib.optional')}` : ''}`}>
                  {f.path}{f.kind !== 'text' && <span className="font-sans text-gray-500">· {t(`lib.field.${f.kind}` as Key)}</span>}
                </span>
              ))}
            </div>
          )}
          {data.sample && (
            <details className="mt-4">
              <summary className="text-sm cursor-pointer text-brand-700 dark:text-brand-400">{t('lib.sample')}</summary>
              <div className="flex justify-end mt-2"><CopyButton text={JSON.stringify(data.sample, null, 2)} /></div>
              <pre className="text-xs bg-gray-50 dark:bg-gray-900 rounded-lg p-3 overflow-x-auto mt-2 max-h-96">{JSON.stringify(data.sample, null, 2)}</pre>
            </details>
          )}
        </section>
      )}

      <section className="card">
        <h2 className="text-lg font-semibold mb-3">{t('lib.versions')}</h2>
        <Table head={<><th className={th}>{t('lib.col.version')}</th><th className={th}>{t('lib.col.updated')}</th><th className={th}>{t('lib.note')}</th><th className={th} /><th className={th}>{t('lib.col.reference')}</th><th className={th} /></>}>
          {data.versions.map((v) => <VersionRow key={v.number} tpl={tpl} v={v} editor={editor} onChange={set} />)}
        </Table>
        {editor && <div className="border-t border-gray-100 dark:border-gray-700 pt-4 mt-4"><NewVersion id={id} onDone={set} /></div>}
      </section>

      {tpl.kind === 'template' && (
        <section className="card">
          <h2 className="text-lg font-semibold mb-1">{t('lint.title')}</h2>
          <p className="text-xs text-gray-500 mb-3">{t('lint.of', { n: data.versions[0]?.number ?? 1 })}</p>
          <LintList issues={data.versions[0]?.lint} />
          <div className="mt-4"><TagCheatSheet /></div>
        </section>
      )}

      {tpl.kind === 'template' && (
        <section className="card">
          <h2 className="text-lg font-semibold mb-3">{t('stats.title')}</h2>
          <TemplateStats id={id} />
        </section>
      )}

      {editor && data.versions.length > 1 && (
        <section className="card">
          <h2 className="text-lg font-semibold mb-1">{t('cmp.title')}</h2>
          <p className="text-xs text-gray-500 mb-3">{t('cmp.intro')}</p>
          <CompareVersions id={id} versions={data.versions} fixed={tpl.kind !== 'template'} />
        </section>
      )}

      <section className="card">
        <h2 className="text-lg font-semibold mb-1">{t('promo.title')}</h2>
        <p className="text-xs text-gray-500 mb-3">{t('promo.intro')}</p>
        <PromotePanel id={id} versions={data.versions} published={tpl.published} />
      </section>

      {atLeast(me, 'admin') && (
        <div className="flex justify-end">
          <button className="btn-danger inline-flex items-center gap-2" disabled={remove.isPending}
            onClick={() => window.confirm(t('lib.deleteConfirm', { path: tpl.path, n: tpl.versions })) && remove.mutate({ id })}>
            <Trash2 className="w-4 h-4" /> {t('common.delete')}
          </button>
        </div>
      )}
    </div>
  )
}
