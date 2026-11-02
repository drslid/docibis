import { useEffect, useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Megaphone, Pencil, Plus, Trash2 } from 'lucide-react'
import {
  getListReleasesQueryKey, useCreateRelease, useDeleteRelease, useListReleases, useUpdateRelease, type Release,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { EmptyState } from '@/components/common/EmptyState'
import { PageTitle } from '@/components/layout/PageTitle'
import { atLeast, useMe } from '@/hooks/useMe'
import { useReleaseSeen } from '@/hooks/useReleaseSeen'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

// Notes are written by administrators; raw HTML is never rendered.
const components: Components = {
  h1: ({ children }) => <h3 className="text-base font-semibold mt-4 mb-2">{children}</h3>,
  h2: ({ children }) => <h3 className="text-base font-semibold mt-4 mb-2">{children}</h3>,
  h3: ({ children }) => <h4 className="text-sm font-semibold mt-3 mb-1">{children}</h4>,
  p: ({ children }) => <p className="my-2 leading-relaxed">{children}</p>,
  ul: ({ children }) => <ul className="my-2 list-disc pl-6 space-y-1">{children}</ul>,
  ol: ({ children }) => <ol className="my-2 list-decimal pl-6 space-y-1">{children}</ol>,
  a: ({ href, children }) => <a href={href} className="text-brand-600 underline" target="_blank" rel="noreferrer">{children}</a>,
  code: ({ children }) => <code className="px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-700 text-[0.85em]">{children}</code>,
}

function Markdown({ text }: { text: string }) {
  return <div className="text-sm text-gray-700 dark:text-gray-300"><ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>{text}</ReactMarkdown></div>
}

function Editor({ release, onDone }: { release?: Release; onDone: () => void }) {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const [version, setVersion] = useState(release?.version ?? '')
  const [title, setTitle] = useState(release?.title ?? '')
  const [content, setContent] = useState(release?.content ?? '')
  const [preview, setPreview] = useState(false)
  const done = () => { queryClient.invalidateQueries({ queryKey: getListReleasesQueryKey() }); onDone() }
  const create = useCreateRelease({ mutation: { onSuccess: done } })
  const update = useUpdateRelease({ mutation: { onSuccess: done } })
  const error = create.error ?? update.error

  function submit(e: FormEvent) {
    e.preventDefault()
    const data = { version, title, content }
    if (release) update.mutate({ id: release.id, data })
    else create.mutate({ data })
  }

  return (
    <form onSubmit={submit} className="card mb-6 space-y-4">
      <div className="grid gap-4 md:grid-cols-[10rem_1fr]">
        <div>
          <label className="label" htmlFor="rel-version">{t('rel.version')}</label>
          <input id="rel-version" className="input" value={version} onChange={(e) => setVersion(e.target.value)} placeholder="1.4.0" maxLength={40} />
        </div>
        <div>
          <label className="label" htmlFor="rel-title">{t('rel.titleField')}</label>
          <input id="rel-title" className="input" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} />
        </div>
      </div>
      <div>
        <div className="flex items-center justify-between">
          <label className="label" htmlFor="rel-content">{t('rel.content')}</label>
          <div className="flex gap-1 text-sm">
            <button type="button" className={`px-2 py-0.5 rounded ${!preview ? 'bg-gray-200 dark:bg-gray-700' : ''}`} onClick={() => setPreview(false)}>{t('rel.write')}</button>
            <button type="button" className={`px-2 py-0.5 rounded ${preview ? 'bg-gray-200 dark:bg-gray-700' : ''}`} onClick={() => setPreview(true)}>{t('rel.preview')}</button>
          </div>
        </div>
        {preview ? (
          <div className="border border-gray-200 dark:border-gray-700 rounded-lg p-4 min-h-48"><Markdown text={content} /></div>
        ) : (
          <textarea id="rel-content" className="input font-mono text-sm min-h-48" value={content} onChange={(e) => setContent(e.target.value)} placeholder={'- Bibliothèque de modèles\n- Route compatible Docmosis'} />
        )}
      </div>
      {error && <p className="text-sm text-red-700 dark:text-red-400" role="alert">{message(error)}</p>}
      <div className="flex justify-end gap-2">
        <button type="button" className="btn-ghost" onClick={onDone}>{t('common.cancel')}</button>
        <button className="btn-primary" disabled={!version.trim() || !title.trim() || !content.trim() || create.isPending || update.isPending}>{t('common.save')}</button>
      </div>
    </form>
  )
}

/** "Nouveautés": the release notes, newest first. */
export function ReleasesPage() {
  const { t, date } = useI18n()
  const me = useMe()
  const queryClient = useQueryClient()
  const { data, error, isPending } = useListReleases({ limit: 100 })
  const markSeen = useReleaseSeen((s) => s.markSeen)
  const [editing, setEditing] = useState<Release | 'new'>()
  const remove = useDeleteRelease({ mutation: { onSuccess: () => queryClient.invalidateQueries({ queryKey: getListReleasesQueryKey() }) } })
  const admin = atLeast(me, 'admin')
  const latest = data?.items[0]?.id

  useEffect(() => {
    if (latest) markSeen(latest)
  }, [latest, markSeen])

  return (
    <div>
      <PageTitle>{t('nav.releases')}</PageTitle>
      <div className="flex items-start justify-between gap-4 mb-6">
        <p className="text-sm text-gray-600 dark:text-gray-400">{t('rel.intro')}</p>
        {admin && !editing && (
          <button className="btn-primary inline-flex items-center gap-2 shrink-0" onClick={() => setEditing('new')}><Plus className="w-4 h-4" /> {t('rel.new')}</button>
        )}
      </div>
      {editing && <Editor key={editing === 'new' ? 'new' : editing.id} release={editing === 'new' ? undefined : editing} onDone={() => setEditing(undefined)} />}

      {error ? (
        <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
      ) : isPending ? (
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      ) : data.items.length === 0 ? (
        <EmptyState icon={<Megaphone className="w-10 h-10" />} title={t('rel.none')} />
      ) : (
        <ol className="relative border-l-2 border-orange-200 dark:border-orange-900/60 ml-3 space-y-8">
          {data.items.map((r, i) => (
            <li key={r.id} className="ml-6">
              <span className={`absolute -left-[9px] mt-1.5 w-4 h-4 rounded-full border-2 border-white dark:border-gray-900 ${i === 0 ? 'bg-orange-500' : 'bg-orange-200 dark:bg-orange-800'}`} />
              <div className="card">
                <div className="flex flex-wrap items-center gap-2 mb-1">
                  <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300">v{r.version.replace(/^v/i, '')}</span>
                  {i === 0 && <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300">{t('rel.latest')}</span>}
                  <h2 className="font-semibold text-gray-900 dark:text-gray-100">{r.title}</h2>
                  {admin && (
                    <span className="ml-auto flex gap-1">
                      <button className="btn-ghost p-1.5" aria-label={t('common.edit')} onClick={() => setEditing(r)}><Pencil className="w-4 h-4" /></button>
                      <button className="btn-ghost p-1.5 text-red-600" aria-label={t('common.delete')} disabled={remove.isPending}
                        onClick={() => window.confirm(t('rel.deleteConfirm', { version: r.version })) && remove.mutate({ id: r.id })}><Trash2 className="w-4 h-4" /></button>
                    </span>
                  )}
                </div>
                <p className="text-xs text-gray-400 mb-2">{date(r.createdAt, false)} · {t('rel.by', { name: r.createdBy })}</p>
                <Markdown text={r.content} />
              </div>
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}
