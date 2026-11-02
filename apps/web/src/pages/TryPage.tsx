import { useEffect, useMemo, useRef, useState, type DragEvent } from 'react'
import { Link } from 'react-router'
import { Code2, Download, ExternalLink, FileStack, FileText, Loader2, RotateCcw, Upload, Zap } from 'lucide-react'
import {
  getTemplate, useListTemplates, useTryAnalyze, useTryRender, type LibraryTemplate, type TryRenderForm, type TryRenderFormPdfa, type TryResult, type TryTemplate,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { PageTitle } from '@/components/layout/PageTitle'
import { HelpTip } from '@/components/help/HelpTip'
import { TagCheatSheet } from '@/components/help/TagCheatSheet'
import { LintList } from '@/components/library/LintList'
import { DataForm, type Json } from '@/components/try/DataForm'
import { useI18n, type Key } from '@/i18n/useI18n'

const ENGINES: Record<string, { label: Key; fast: boolean }> = {
  overlay: { label: 'try.engine.overlay', fast: true },
  native: { label: 'try.engine.native', fast: true },
  standard: { label: 'try.engine.standard', fast: false },
}

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

function pdfURL(result: TryResult) {
  const bytes = Uint8Array.from(atob(result.pdf), (c) => c.charCodeAt(0))
  return URL.createObjectURL(new Blob([bytes], { type: 'application/pdf' }))
}

export function TryPage() {
  const { t } = useI18n()
  const [file, setFile] = useState<File>()
  const [library, setLibrary] = useState<LibraryTemplate>()
  const [data, setData] = useState('')
  const [dragging, setDragging] = useState(false)
  const [pdf, setPdf] = useState<{ url: string; result: TryResult }>()
  const [pdfa, setPdfa] = useState('')
  const [accessible, setAccessible] = useState(false)
  const [devMode, setDevMode] = useState(true)
  const [watermark, setWatermark] = useState('')
  const [colour, setColour] = useState('#c0c0c0')
  const [tiled, setTiled] = useState(false)
  const [mode, setMode] = useState<'form' | 'json'>('form')
  const [live, setLive] = useState(true)
  const input = useRef<HTMLInputElement>(null)
  const analyze = useTryAnalyze()
  const render = useTryRender()
  const { data: templates } = useListTemplates(undefined, { query: { retry: false } })
  const published = (templates ?? []).filter((x) => x.kind === 'template' && x.published > 0)
  const template: TryTemplate | undefined = library ? undefined : analyze.data

  useEffect(() => () => { if (pdf) URL.revokeObjectURL(pdf.url) }, [pdf])

  const dataError = useMemo(() => {
    if (!data.trim()) return t('try.dataEmpty')
    try {
      const value: unknown = JSON.parse(data)
      return value && typeof value === 'object' && !Array.isArray(value) ? '' : t('try.dataObject')
    } catch (e) {
      return t('try.dataInvalid', { message: (e as Error).message })
    }
  }, [data, t])
  const formValue = useMemo(() => (dataError ? undefined : (JSON.parse(data) as Record<string, Json>)), [data, dataError])

  function choose(f: File, preset?: string) {
    setFile(f)
    setLibrary(undefined)
    setPdf(undefined)
    render.reset()
    analyze.mutate({ data: { template: f } }, { onSuccess: (tpl) => setData(preset ?? JSON.stringify(tpl.sample, null, 2)) })
  }

  function pick(path: string) {
    const chosen = published.find((x) => x.path === path)
    setLibrary(chosen)
    setFile(undefined)
    setPdf(undefined)
    render.reset()
    analyze.reset()
    if (chosen) getTemplate(chosen.id).then((d) => setData(JSON.stringify(d.sample ?? {}, null, 2)))
  }

  async function loadExample() {
    const [docx, json] = await Promise.all([fetch('/exemple-docibis.docx'), fetch('/exemple-docibis.json')])
    choose(new File([await docx.blob()], 'exemple-docibis.docx'), await json.text())
  }

  function produce() {
    const source = library ? { templateName: library.path } : file ? { template: file } : undefined
    if (!source) return
    const options: Partial<TryRenderForm> = {
      ...(pdfa && { pdfa: pdfa as TryRenderFormPdfa }),
      ...(accessible && { accessible: true }),
      ...(devMode && { devMode: true }),
      ...(watermark.trim() && { watermark: watermark.trim(), watermarkColor: colour, ...(tiled && { watermarkTiled: true }) }),
    }
    render.mutate({ data: { ...source, ...options, data } }, { onSuccess: (result) => setPdf({ url: pdfURL(result), result }) })
  }

  // Live preview: the document is produced again a moment after each change.
  const latest = useRef(produce)
  useEffect(() => {
    latest.current = produce
  })
  const ready = !!(template || library) && !dataError
  useEffect(() => {
    if (!live || !ready) return
    const timer = setTimeout(() => latest.current(), 900)
    return () => clearTimeout(timer)
  }, [live, ready, data, pdfa, accessible, devMode, watermark, colour, tiled])

  function drop(e: DragEvent) {
    e.preventDefault()
    setDragging(false)
    const f = e.dataTransfer.files[0]
    if (f) choose(f)
  }

  const engine = template ? ENGINES[template.analysis.engine] : undefined
  const pdfName = ((library ? library.path.split('/').pop() : file?.name) ?? 'document').replace(/\.(docx|odt)$/i, '') + '.pdf'

  return (
    <div>
      <PageTitle>{t('try.title')}</PageTitle>
      <p className="text-sm text-gray-500 dark:text-gray-400 mb-6">{t('try.intro')}</p>

      <div className="grid gap-6 lg:grid-cols-2">
        <div className="space-y-6">
          <section className="card">
            <h2 className="text-lg font-semibold mb-4">{t('try.template')}</h2>
            <div
              onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
              onDragLeave={() => setDragging(false)}
              onDrop={drop}
              className={`border-2 border-dashed rounded-xl p-6 text-center transition-colors ${dragging ? 'border-brand-500 bg-brand-50 dark:bg-brand-900/20' : 'border-gray-300 dark:border-gray-600'}`}
            >
              <Upload className="w-8 h-8 mx-auto text-gray-400 mb-2" />
              <p className="text-sm text-gray-600 dark:text-gray-300 mb-3">{t('try.drop')}</p>
              <div className="flex flex-wrap justify-center gap-2">
                <button className="btn-primary" onClick={() => input.current?.click()}>{t('try.choose')}</button>
                <button className="btn-secondary" onClick={loadExample}>{t('try.useExample')}</button>
              </div>
              <input
                ref={input}
                type="file"
                accept=".docx,.odt"
                className="hidden"
                aria-label={t('try.templateLabel')}
                onChange={(e) => { const f = e.target.files?.[0]; if (f) choose(f); e.target.value = '' }}
              />
              <p className="text-xs text-gray-400 mt-3">
                <a href="/exemple-docibis.docx" download className="underline hover:text-gray-600">{t('try.downloadExample')}</a> {t('try.downloadHint')}
              </p>
            </div>

            {published.length > 0 && (
              <div className="mt-4">
                <label className="label" htmlFor="try-library">{t('try.fromLibrary')}</label>
                <select id="try-library" className="input" value={library?.path ?? ''} onChange={(e) => pick(e.target.value)}>
                  <option value="">{t('try.pickTemplate')}</option>
                  {published.map((x) => <option key={x.id} value={x.path}>{x.path}</option>)}
                </select>
                {library && (
                  <p className="flex items-center gap-2 text-sm mt-2">
                    <FileStack className="w-4 h-4 text-blue-600" />
                    <Link to={`/modeles/${library.id}`} className="font-mono text-xs text-brand-700 dark:text-brand-400 hover:underline break-all">{library.path}</Link>
                    <span className="text-gray-400">v{library.published}</span>
                  </p>
                )}
              </div>
            )}

            {analyze.isPending && (
              <p className="flex items-center gap-2 text-sm text-gray-500 mt-4"><Loader2 className="w-4 h-4 animate-spin" /> {t('try.reading')}</p>
            )}
            {analyze.error && <p className="text-sm text-red-700 dark:text-red-400 mt-4">{message(analyze.error)}</p>}
            {template && engine && (
              <div className="mt-4 text-sm space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  <FileText className="w-4 h-4 text-sky-500" />
                  <span className="font-medium">{template.name}</span>
                  <span className="text-gray-400">{Math.ceil(template.size / 1024)} {t('unit.kb')}</span>
                  <span className={`text-xs px-2 py-0.5 rounded-full inline-flex items-center gap-1 ${engine.fast ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300' : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'}`}>
                    {engine.fast && <Zap className="w-3 h-3" />} {t(engine.label)}
                  </span>
                </div>
                <p className="text-gray-500 dark:text-gray-400">
                  {[
                    t('try.fields', { n: template.analysis.fields }),
                    t('try.checkboxes', { n: template.analysis.checkboxes }),
                    t('try.sections', { n: template.analysis.sections }),
                    t('try.repeats', { n: template.analysis.repeats }),
                    t('try.images', { n: template.analysis.images }),
                  ].join(' · ')}
                </p>
                <p className="text-gray-500 dark:text-gray-400">{template.analysis.reason}.{template.prepareMs > 0 && ` ${t('try.prepared', { ms: template.prepareMs })}`}</p>
                {template.analysis.unsupported && (
                  <p className="text-amber-700 dark:text-amber-400">{t('try.unsupported', { list: template.analysis.unsupported.join(', ') })}</p>
                )}
                {template.lint && template.lint.length > 0 && (
                  <details className="pt-1" open={template.lint.some((i) => i.level === 'error')}>
                    <summary className="cursor-pointer font-medium text-gray-700 dark:text-gray-300">{t('try.checks', { n: template.lint.length })}</summary>
                    <div className="mt-2"><LintList issues={template.lint} /></div>
                  </details>
                )}
                {template.lint && template.lint.length === 0 && <LintList issues={template.lint} />}
              </div>
            )}
          </section>

          <section className="card">
            <div className="flex flex-wrap items-center justify-between gap-2 mb-4">
              <h2 className="text-lg font-semibold">{t('try.data')}</h2>
              <div className="flex items-center gap-1 rounded-lg bg-gray-100 dark:bg-gray-700 p-0.5 text-sm" role="tablist">
                {(['form', 'json'] as const).map((m) => (
                  <button key={m} type="button" role="tab" aria-selected={mode === m} onClick={() => setMode(m)}
                    className={`px-3 py-1 rounded-md ${mode === m ? 'bg-white dark:bg-gray-800 shadow-sm font-medium' : 'text-gray-500'}`}>
                    {t(m === 'form' ? 'try.tabForm' : 'try.tabJson')}
                  </button>
                ))}
              </div>
              {template && (
                <button className="btn-ghost text-sm inline-flex items-center gap-1" onClick={() => setData(JSON.stringify(template.sample, null, 2))}>
                  <RotateCcw className="w-4 h-4" /> {t('try.resetData')}
                </button>
              )}
            </div>
            {mode === 'form' ? (
              formValue ? (
                <div className="max-h-[36rem] overflow-y-auto pr-1"><DataForm value={formValue} onChange={(v) => setData(JSON.stringify(v, null, 2))} /></div>
              ) : (
                <p className="text-sm text-gray-500">{data.trim() ? t('try.formNeedsJson') : t('try.formEmpty')}</p>
              )
            ) : (
              <textarea
                className="input font-mono text-sm min-h-80"
                value={data}
                onChange={(e) => setData(e.target.value)}
                placeholder={t('try.dataPlaceholder')}
                aria-label={t('try.dataLabel')}
                spellCheck={false}
              />
            )}
            {data && dataError && mode === 'json' && <p className="text-sm text-red-700 dark:text-red-400 mt-2">{dataError}</p>}
            <details className="mt-4">
              <summary className="text-sm font-medium cursor-pointer text-gray-700 dark:text-gray-300">{t('try.options')}</summary>
              <div className="grid gap-3 sm:grid-cols-2 mt-3">
                <div>
                  <label className="label" htmlFor="try-pdfa">{t('try.pdfa')} <HelpTip>{t('tip.pdfa')}</HelpTip></label>
                  <select id="try-pdfa" className="input" value={pdfa} onChange={(e) => setPdfa(e.target.value)}>
                    <option value="">{t('try.byDefault')}</option>
                    <option value="none">{t('try.none')}</option>
                    <option value="PDF/A-1b">PDF/A-1b</option>
                    <option value="PDF/A-2b">PDF/A-2b</option>
                    <option value="PDF/A-3b">PDF/A-3b</option>
                  </select>
                </div>
                <label className="flex items-center gap-2 text-sm sm:pt-6"><input type="checkbox" checked={accessible} onChange={(e) => setAccessible(e.target.checked)} /> {t('try.accessible')}</label>
                <label className="flex items-center gap-2 text-sm sm:col-span-2" title={t('try.devModeHelp')}><input type="checkbox" checked={devMode} onChange={(e) => setDevMode(e.target.checked)} /> {t('try.devMode')}</label>
                <div className="sm:col-span-2 grid gap-3 grid-cols-[1fr_auto_auto] items-end">
                  <div>
                    <label className="label" htmlFor="try-watermark">{t('try.watermark')}</label>
                    <input id="try-watermark" className="input" value={watermark} onChange={(e) => setWatermark(e.target.value)} maxLength={100} placeholder="SPÉCIMEN" />
                  </div>
                  <div>
                    <label className="label" htmlFor="try-colour">{t('try.watermarkColor')}</label>
                    <input id="try-colour" type="color" className="h-10 w-14 rounded border border-gray-300 dark:border-gray-600" value={colour} onChange={(e) => setColour(e.target.value)} />
                  </div>
                  <label className="flex items-center gap-2 text-sm pb-2"><input type="checkbox" checked={tiled} onChange={(e) => setTiled(e.target.checked)} /> {t('try.watermarkTiled')}</label>
                </div>
                <p className="sm:col-span-2 text-xs text-gray-400">{t('try.watermarkHint')}</p>
              </div>
            </details>
            <div className="flex flex-wrap items-center gap-3 mt-4">
              <label className="flex items-center gap-2 text-sm" title={t('try.liveHint')}>
                <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} /> {t('try.live')}
              </label>
              <button className="btn-primary flex-1 inline-flex items-center justify-center gap-2" disabled={!(template || library) || !!dataError || render.isPending} onClick={produce}>
                {render.isPending && <Loader2 className="w-4 h-4 animate-spin" />} {t('try.produce')}
              </button>
            </div>
          </section>
          <TagCheatSheet />
        </div>

        <section className="card flex flex-col min-h-[36rem]">
          <h2 className="text-lg font-semibold mb-4">{t('try.document')}</h2>
          {render.error && <p className="text-sm text-red-700 dark:text-red-400">{message(render.error)}</p>}
          {pdf ? (
            <>
              <p className="text-sm mb-1">
                <span className="font-semibold">{t('try.producedIn', { ms: pdf.result.totalMs })}</span>
                <span className="text-gray-500 dark:text-gray-400"> : {t('try.breakdown', { merge: pdf.result.mergeMs, render: pdf.result.renderMs })}{pdf.result.prepareMs > 0 && ` · ${t('try.preparation', { ms: pdf.result.prepareMs })}`}</span>
              </p>
              <p className="text-xs text-gray-400 mb-3">{t('try.size', { kb: Math.ceil(pdf.result.size / 1024) })}</p>
              <div className="flex gap-2 mb-4">
                <a className="btn-secondary inline-flex items-center gap-2 text-sm" href={pdf.url} download={pdfName}><Download className="w-4 h-4" /> {t('try.download')}</a>
                <a className="btn-ghost inline-flex items-center gap-2 text-sm" href={pdf.url} target="_blank" rel="noreferrer"><ExternalLink className="w-4 h-4" /> {t('try.openTab')}</a>
                <Link className="btn-ghost inline-flex items-center gap-2 text-sm ml-auto" to="/developpeurs"><Code2 className="w-4 h-4" /> {t('try.byApi')}</Link>
              </div>
              <iframe title={t('try.preview')} src={pdf.url} className="w-full flex-1 min-h-[32rem] rounded-lg border border-gray-200 dark:border-gray-700 bg-gray-100" />
            </>
          ) : (
            !render.error && (
              <div className="flex-1 flex items-center justify-center text-sm text-gray-400 text-center px-6">
                {render.isPending ? t('try.producing') : t('try.empty')}
              </div>
            )
          )}
        </section>
      </div>
    </div>
  )
}
