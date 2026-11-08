import { useEffect, useState } from 'react'
import { CircleAlert, CircleCheck, FileText, Info, KeyRound, Loader2, Search, TriangleAlert } from 'lucide-react'
import type { LintIssue, TryTemplate } from '@/api/generated/docibis'
import { TAGS } from '@/components/help/TagCheatSheet'
import { useI18n } from '@/i18n/useI18n'
import { currentDocument, findInDocument, inWord, insertAtSelection } from './office'

const TOKEN = 'docibis_word_token'
const LEVELS = {
  error: { icon: CircleAlert, tone: 'text-red-700' },
  warning: { icon: TriangleAlert, tone: 'text-amber-700' },
  info: { icon: Info, tone: 'text-sky-700' },
}

/** The text of the document an issue is about: its tag, or what its message quotes. */
export function issueText(message: string): string {
  return message.match(/<<[^>]*>>/)?.[0] ?? message.match(/«\s*([^»]+?)\s*»/)?.[1] ?? ''
}

/** Calls the API with the personal token; a refused call throws its message. */
async function call(path: string, token: string, form: FormData): Promise<Response> {
  const res = await fetch(path, { method: 'POST', headers: { Authorization: `Bearer ${token}` }, body: form })
  if (!res.ok) {
    const problem = (await res.json().catch(() => null)) as { detail?: string } | null
    throw Object.assign(new Error(problem?.detail ?? `HTTP ${res.status}`), { status: res.status })
  }
  return res
}

/** The task pane of the Word add-in: tags, checks and PDF preview of the document being written. */
export function WordPane() {
  const { t } = useI18n()
  const [word, setWord] = useState<boolean>()
  // Kept in localStorage so that Word reopens signed in; /word/ runs only its own scripts (CSP).
  const [token, setToken] = useState(() => localStorage.getItem(TOKEN) ?? '')
  const [draft, setDraft] = useState('')
  const [tab, setTab] = useState<'tags' | 'check' | 'preview'>('tags')
  const [field, setField] = useState('')
  const [template, setTemplate] = useState<TryTemplate>()
  const [data, setData] = useState('')
  const [pdf, setPdf] = useState<string>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [missing, setMissing] = useState('')

  useEffect(() => {
    inWord().then(setWord, () => setWord(false))
  }, [])
  useEffect(() => () => { if (pdf) URL.revokeObjectURL(pdf) }, [pdf])

  async function run(job: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await job()
    } catch (e) {
      const err = e as Error & { status?: number }
      setError(err.status === 401 ? t('word.tokenRefused') : err.message)
    } finally {
      setBusy(false)
    }
  }

  const check = () =>
    run(async () => {
      const form = new FormData()
      form.append('template', await currentDocument())
      const out = (await (await call('/v1/templates/analyze', token, form)).json()) as TryTemplate
      setTemplate(out)
      if (!data.trim()) setData(JSON.stringify(out.sample, null, 2))
    })

  const produce = () =>
    run(async () => {
      const form = new FormData()
      form.append('template', await currentDocument())
      form.append('data', data)
      form.append('devMode', 'true')
      const blob = await (await call('/v1/render', token, form)).blob()
      setPdf(URL.createObjectURL(blob))
    })

  async function find(text: string) {
    setMissing(text && (await findInDocument(text)) ? '' : text)
  }

  if (word === undefined) return <Loader2 className="w-5 h-5 animate-spin m-4 text-gray-400" />

  return (
    <div className="p-3 space-y-3 text-sm">
      <h1 className="flex items-center gap-2 font-semibold text-base">
        <FileText className="w-5 h-5 text-brand-600" /> {t('word.title')}
      </h1>
      {!word && <p className="text-gray-600">{t('word.outside')}</p>}
      {word && !token && (
        <form className="space-y-2" onSubmit={(e) => { e.preventDefault(); localStorage.setItem(TOKEN, draft.trim()); setToken(draft.trim()) }}>
          <label className="label" htmlFor="word-token">{t('word.tokenTitle')}</label>
          <input id="word-token" type="password" className="input" value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="dit_…" autoComplete="off" />
          <p className="text-xs text-gray-500">{t('word.tokenHelp')}</p>
          <div className="flex gap-2">
            <button className="btn-primary" disabled={!draft.trim().startsWith('dit_')}>{t('word.tokenSave')}</button>
            <a className="btn-ghost" href="/profil?section=tokens" target="_blank" rel="noopener noreferrer">{t('word.tokenProfile')}</a>
          </div>
        </form>
      )}
      {word && token && (
        <>
          <div role="tablist" className="flex gap-1 border-b border-gray-200">
            {(['tags', 'check', 'preview'] as const).map((name) => (
              <button key={name} role="tab" aria-selected={tab === name} onClick={() => setTab(name)}
                className={`px-3 py-1.5 -mb-px border-b-2 ${tab === name ? 'border-brand-600 text-brand-700 font-medium' : 'border-transparent text-gray-500'}`}>
                {t(name === 'tags' ? 'word.tabTags' : name === 'check' ? 'word.tabCheck' : 'word.tabPreview')}
              </button>
            ))}
          </div>
          {error && <p role="alert" className="text-red-700">{error}</p>}

          {tab === 'tags' && (
            <div className="space-y-3">
              <form className="flex gap-2 items-end" onSubmit={(e) => { e.preventDefault(); if (field.trim()) void insertAtSelection(`<<${field.trim()}>>`) }}>
                <div className="flex-1">
                  <label className="label" htmlFor="word-field">{t('word.field')}</label>
                  <input id="word-field" className="input" value={field} onChange={(e) => setField(e.target.value)} placeholder="client.nom" />
                </div>
                <button className="btn-primary" disabled={!field.trim()}>{t('word.insert')}</button>
              </form>
              {template?.fields && template.fields.length > 0 && (
                <div>
                  <div className="label">{t('word.fields')}</div>
                  <div className="flex flex-wrap gap-1">
                    {template.fields.filter((f) => f.kind === 'text' || f.kind === 'flag').map((f) => (
                      <button key={f.path} className="btn-secondary py-0.5 px-2 text-xs font-mono" onClick={() => void insertAtSelection(`<<${f.path.split('[].').pop()}>>`)}>{f.path}</button>
                    ))}
                  </div>
                </div>
              )}
              <ul className="divide-y divide-gray-100">
                {TAGS.map(([tag, label]) => (
                  <li key={tag} className="flex items-center gap-2 py-1.5">
                    <div className="flex-1 min-w-0">
                      <code className="text-xs font-mono text-brand-700 break-all">{tag}</code>
                      <div className="text-xs text-gray-500">{t(label)}</div>
                    </div>
                    <button className="btn-secondary shrink-0 py-0.5 px-2 text-xs" onClick={() => void insertAtSelection(tag)}>{t('word.insert')}</button>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {tab === 'check' && (
            <div className="space-y-3">
              <button className="btn-primary inline-flex items-center gap-2" disabled={busy} onClick={check}>
                {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Search className="w-4 h-4" />} {t('word.check')}
              </button>
              {template && (
                <>
                  <p className="text-gray-600">{template.analysis.reason}.</p>
                  <p className="text-xs text-gray-500">
                    {[t('try.fields', { n: template.analysis.fields }), t('try.sections', { n: template.analysis.sections }),
                      t('try.repeats', { n: template.analysis.repeats }), t('try.images', { n: template.analysis.images })].join(' · ')}
                  </p>
                  <Issues issues={template.lint ?? []} onFind={find} />
                  {missing && <p className="text-xs text-amber-700">{t('word.notFound', { text: missing })}</p>}
                </>
              )}
            </div>
          )}

          {tab === 'preview' && (
            <div className="space-y-2">
              {!data.trim() && <p className="text-xs text-gray-500">{t('word.sampleHint')}</p>}
              <label className="label" htmlFor="word-data">{t('word.data')}</label>
              <textarea id="word-data" className="input font-mono text-xs h-40" value={data} onChange={(e) => setData(e.target.value)} spellCheck={false} />
              <button className="btn-primary inline-flex items-center gap-2" disabled={busy || !data.trim()} onClick={produce}>
                {busy && <Loader2 className="w-4 h-4 animate-spin" />} {t('word.produce')}
              </button>
              {pdf && (
                <>
                  <iframe title={t('word.tabPreview')} src={pdf} className="w-full h-[28rem] border border-gray-200 rounded" />
                  <a className="btn-ghost text-xs" href={pdf} download="apercu.pdf">{t('word.download')}</a>
                </>
              )}
            </div>
          )}

          <button className="btn-ghost text-xs inline-flex items-center gap-1" onClick={() => { localStorage.removeItem(TOKEN); setToken(''); setDraft('') }}>
            <KeyRound className="w-3.5 h-3.5" /> {t('word.tokenForget')}
          </button>
        </>
      )}
    </div>
  )
}

function Issues({ issues, onFind }: { issues: LintIssue[]; onFind: (text: string) => void }) {
  const { t } = useI18n()
  if (issues.length === 0) {
    return <p className="text-green-700 inline-flex items-center gap-2"><CircleCheck className="w-4 h-4" /> {t('lint.clean')}</p>
  }
  return (
    <ul className="space-y-1.5">
      {issues.map((i) => {
        const Icon = LEVELS[i.level].icon
        const text = issueText(i.message)
        return (
          <li key={`${i.code}-${i.message}`} className="flex items-start gap-2">
            <Icon className={`w-4 h-4 mt-0.5 shrink-0 ${LEVELS[i.level].tone}`} />
            <span className="flex-1 text-gray-700">{i.message}</span>
            {text && <button className="btn-ghost py-0 px-1.5 text-xs" onClick={() => onFind(text)}>{t('word.find')}</button>}
          </li>
        )
      })}
    </ul>
  )
}
