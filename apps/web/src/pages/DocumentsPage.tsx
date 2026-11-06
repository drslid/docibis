import { useEffect, useState } from 'react'
import { keepPreviousData } from '@tanstack/react-query'
import { Ban, CheckCircle2, Download, ExternalLink, FileText, Loader2, RefreshCw, XCircle } from 'lucide-react'
import { useListRenders, type ListRendersFormat, type ListRendersSource, type ListRendersStatus, type RenderEntry } from '@/api/generated/docibis'
import { EmptyState } from '@/components/common/EmptyState'
import { Pager, ResetFilters, SearchBox, Select, Table, td, th, useDebounced } from '@/components/common/ListTools'
import { Modal } from '@/components/common/Modal'
import { PageTitle } from '@/components/layout/PageTitle'
import { atLeast, useMe } from '@/hooks/useMe'
import { useI18n, type Key, type Vars } from '@/i18n/useI18n'

const LIMIT = 50

function size(bytes: number, number: (v: number, digits?: number) => string, t: (key: Key, vars?: Vars) => string) {
  if (bytes <= 0) return '—'
  if (bytes < 1024 * 1024) return `${number(bytes / 1024, bytes < 10 * 1024 ? 1 : 0)} ${t('unit.kb')}`
  return `${number(bytes / 1024 / 1024, 1)} ${t('unit.mb')}`
}

type Produced = { url: string; name: string; pdf: boolean }

/** One document of the history: what produced it, and the button that produces it again. */
function RenderDialog({ entry, onClose }: { entry: RenderEntry; onClose: () => void }) {
  const { t, date, number } = useI18n()
  const me = useMe()
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState('')
  const [doc, setDoc] = useState<Produced | null>(null)
  useEffect(() => () => { if (doc) URL.revokeObjectURL(doc.url) }, [doc])

  const regenerate = async () => {
    setBusy(true)
    setFailure('')
    try {
      const response = await fetch(`/api/renders/${entry.id}/regenerate`, { method: 'POST', credentials: 'same-origin' })
      if (!response.ok) {
        const problem = (await response.json().catch(() => null)) as { detail?: string; title?: string } | null
        throw new Error(problem?.detail ?? problem?.title ?? `HTTP ${response.status}`)
      }
      const blob = await response.blob()
      const disposition = response.headers.get('Content-Disposition') ?? ''
      const name = /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? `${entry.template}.${entry.format}`
      setDoc({ url: URL.createObjectURL(blob), name, pdf: blob.type === 'application/pdf' })
    } catch (e) {
      setFailure(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const options = ((): Record<string, string> => {
    try {
      return entry.options ? (JSON.parse(entry.options) as Record<string, string>) : {}
    } catch {
      return {}
    }
  })()
  const canRegenerate = atLeast(me, 'editor') && !!entry.hasData
  const facts: [string, React.ReactNode][] = [
    [t('history.col.date'), date(entry.createdAt)],
    [t('history.col.template'), <span className="break-all">{entry.template}{entry.templateVersion ? ` · v${entry.templateVersion}` : ''}</span>],
    [t('history.col.by'), `${entry.source === 'api' ? t('history.api') : entry.source === 'mcp' ? t('history.mcp') : t('history.console')} · ${entry.actor || '—'}`],
    [t('history.col.format'), entry.format.toUpperCase()],
    [t('history.col.size'), size(entry.size, number, t)],
    [t('history.col.duration'), `${number(entry.durationMs)} ms`],
    [t('history.options'), Object.keys(options).length ? Object.entries(options).map(([k, v]) => `${k} = ${v}`).join(' · ') : t('history.noOptions')],
    [t('history.requestId'), entry.requestId ? <code className="text-xs break-all">{entry.requestId}</code> : '—'],
  ]

  return (
    <Modal onClose={onClose} title={entry.template || t('history.document')} size={doc?.pdf ? 'xl' : 'lg'}>
      <div className="space-y-5">
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-3 rounded-lg bg-gray-50 dark:bg-gray-900/40 px-4 py-3">
          {facts.map(([label, value]) => (
            <div key={label} className="min-w-0">
              <dt className="text-xs font-medium text-gray-500 dark:text-gray-400">{label}</dt>
              <dd className="text-sm text-gray-800 dark:text-gray-200 mt-0.5">{value}</dd>
            </div>
          ))}
        </dl>
        {entry.status !== 'ok' && entry.error && (
          <p className={`text-sm rounded-lg px-3 py-2 ${entry.status === 'refused' ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'}`}>
            {entry.error}
          </p>
        )}
        {canRegenerate ? (
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" className="btn-primary inline-flex items-center gap-2" onClick={regenerate} disabled={busy}>
              {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />} {t('history.regenerate')}
            </button>
            {doc && (
              <>
                <a className="btn-secondary inline-flex items-center gap-2" href={doc.url} download={doc.name}>
                  <Download className="w-4 h-4" /> {t('history.download')}
                </a>
                {doc.pdf && (
                  <a className="btn-ghost inline-flex items-center gap-2" href={doc.url} target="_blank" rel="noreferrer">
                    <ExternalLink className="w-4 h-4" /> {t('history.openTab')}
                  </a>
                )}
              </>
            )}
            <span className="text-xs text-gray-500 dark:text-gray-400">{t('history.regenerateHint')}</span>
          </div>
        ) : (
          <p className="text-sm text-gray-500 dark:text-gray-400">{entry.hasData ? t('history.editorsOnly') : t('history.noData')}</p>
        )}
        {failure && <p className="text-sm text-red-700 dark:text-red-400" role="alert">{failure}</p>}
        {doc?.pdf && <iframe title={doc.name} src={doc.url} className="w-full h-[70vh] rounded-lg border border-gray-200 dark:border-gray-700 bg-white" />}
      </div>
    </Modal>
  )
}

/** History of the documents produced, searched as you type. */
export function DocumentsPage() {
  const { t, date, number } = useI18n()
  const [text, setText] = useState('')
  const [status, setStatus] = useState('')
  const [source, setSource] = useState('')
  const [format, setFormat] = useState('')
  const [offset, setOffset] = useState(0)
  const [open, setOpen] = useState<RenderEntry | null>(null)
  const q = useDebounced(text.trim(), 300)
  const filtered = text !== '' || status !== '' || source !== '' || format !== ''

  const { data, error, isPending } = useListRenders(
    {
      limit: LIMIT,
      offset,
      ...(q && { q }),
      ...(status && { status: status as ListRendersStatus }),
      ...(source && { source: source as ListRendersSource }),
      ...(format && { format: format as ListRendersFormat }),
    },
    { query: { placeholderData: keepPreviousData, refetchInterval: 15_000 } },
  )

  const pick = (set: (v: string) => void) => (v: string) => {
    set(v)
    setOffset(0)
  }

  return (
    <div>
      <PageTitle>{t('nav.history')}</PageTitle>
      <p className="text-sm text-gray-600 dark:text-gray-400 mb-5 max-w-3xl">{t('history.intro')}</p>

      <div className="card">
        <div className="flex flex-wrap items-center gap-3 mb-4">
          <SearchBox value={text} onChange={pick(setText)} placeholder={t('history.search')} />
          <Select value={status} onChange={pick(setStatus)} label={t('history.col.result')}>
            <option value="">{t('history.allResults')}</option>
            <option value="ok">{t('history.ok')}</option>
            <option value="refused">{t('history.refused')}</option>
            <option value="error">{t('history.error')}</option>
          </Select>
          <Select value={source} onChange={pick(setSource)} label={t('history.allSources')}>
            <option value="">{t('history.allSources')}</option>
            <option value="console">{t('history.console')}</option>
            <option value="api">{t('history.api')}</option>
            <option value="mcp">{t('history.mcp')}</option>
          </Select>
          <Select value={format} onChange={pick(setFormat)} label={t('history.col.format')}>
            <option value="">{t('history.allFormats')}</option>
            <option value="pdf">PDF</option>
            <option value="docx">Word</option>
            <option value="doc">Word 97</option>
            <option value="odt">ODT</option>
            <option value="rtf">RTF</option>
            <option value="html">HTML</option>
            <option value="txt">{t('history.text')}</option>
          </Select>
          <ResetFilters
            show={filtered}
            onClick={() => {
              setText('')
              setStatus('')
              setSource('')
              setFormat('')
              setOffset(0)
            }}
          />
        </div>

        {error ? (
          <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
        ) : isPending ? (
          <p className="text-sm text-gray-500">{t('common.loading')}</p>
        ) : data.items.length === 0 ? (
          <EmptyState icon={<FileText className="w-10 h-10" />} title={t('history.none')} />
        ) : (
          <>
            <Table
              head={
                <>
                  <th className={th}>{t('history.col.date')}</th>
                  <th className={th}>{t('history.col.template')}</th>
                  <th className={th}>{t('history.col.by')}</th>
                  <th className={th}>{t('history.col.format')}</th>
                  <th className={`${th} text-right`}>{t('history.col.size')}</th>
                  <th className={`${th} text-right`}>{t('history.col.duration')}</th>
                  <th className={th}>{t('history.col.result')}</th>
                </>
              }
            >
              {data.items.map((r) => (
                <tr
                  key={r.id}
                  className="hover:bg-gray-50 dark:hover:bg-gray-700/30 cursor-pointer"
                  tabIndex={0}
                  onClick={() => setOpen(r)}
                  onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), setOpen(r))}
                  title={t('history.openHint')}
                >
                  <td className={`${td} whitespace-nowrap text-gray-500`}>{date(r.createdAt)}</td>
                  <td className={`${td} font-medium text-gray-900 dark:text-gray-100 break-all`}>
                    {r.template || '—'}
                    {r.hasData && <RefreshCw className="inline w-3.5 h-3.5 ml-1.5 text-gray-400" aria-label={t('history.canRegenerate')} />}
                  </td>
                  <td className={td}>
                    <span
                      className={`inline-block text-[11px] font-semibold uppercase tracking-wide px-1.5 py-0.5 rounded mr-2 ${
                        r.source === 'api'
                          ? 'bg-violet-100 text-violet-700 dark:bg-violet-900/40 dark:text-violet-300'
                          : r.source === 'mcp'
                            ? 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300'
                            : 'bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300'
                      }`}
                    >
                      {r.source === 'api' ? t('history.api') : r.source === 'mcp' ? t('history.mcp') : t('history.console')}
                    </span>
                    {r.actor || '—'}
                  </td>
                  <td className={`${td} uppercase text-gray-500`}>{r.format}</td>
                  <td className={`${td} text-right whitespace-nowrap tabular-nums`}>{size(r.size, number, t)}</td>
                  <td className={`${td} text-right whitespace-nowrap tabular-nums`}>{number(r.durationMs)} ms</td>
                  <td className={td}>
                    {r.status === 'ok' ? (
                      <span className="inline-flex items-center gap-1 text-emerald-700 dark:text-emerald-400">
                        <CheckCircle2 className="w-4 h-4" /> {t('history.ok')}
                      </span>
                    ) : (
                      <span className={`inline-flex items-start gap-1 ${r.status === 'refused' ? 'text-amber-700 dark:text-amber-400' : 'text-red-700 dark:text-red-400'}`} title={r.error}>
                        {r.status === 'refused' ? <Ban className="w-4 h-4 shrink-0 mt-0.5" /> : <XCircle className="w-4 h-4 shrink-0 mt-0.5" />}
                        <span>
                          {r.status === 'refused' ? t('history.refused') : t('history.error')}
                          {r.error && <span className="block text-xs opacity-80 max-w-xs">{r.error}</span>}
                        </span>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </Table>
            <Pager offset={offset} limit={LIMIT} total={data.total} onChange={setOffset} />
          </>
        )}
      </div>
      {open && <RenderDialog key={open.id} entry={open} onClose={() => setOpen(null)} />}
    </div>
  )
}
