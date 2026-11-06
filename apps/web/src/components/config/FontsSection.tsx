import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, Trash2, Upload } from 'lucide-react'
import { addFont, getListFontsQueryKey, useDeleteFont, useListFonts, type AddedFont } from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { SettingsCard } from '@/components/common/SettingsCard'
import { useI18n } from '@/i18n/useI18n'

function message(error: unknown) {
  if (error instanceof ApiError) return error.problem?.detail ?? error.message
  return error instanceof Error ? error.message : String(error)
}

const th = 'px-3 py-2 text-left text-xs font-semibold text-gray-500 dark:text-gray-400'
const td = 'px-3 py-2 align-top'

/** The fonts of the server: those added by an administrator, those the published templates lack, those installed. */
export function FontsSection() {
  const { t, date, number } = useI18n()
  const queryClient = useQueryClient()
  const { data, error } = useListFonts()
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState('')
  const [failure, setFailure] = useState('')
  const [filter, setFilter] = useState('')
  const refresh = () => queryClient.invalidateQueries({ queryKey: getListFontsQueryKey() })
  const remove = useDeleteFont({ mutation: { onSuccess: () => { setNotice(t('fonts.removed')); void refresh() } } })

  if (error) return <div className="card text-red-700">{t('common.unavailable', { message: message(error) })}</div>
  if (!data) return <Loader2 className="w-5 h-5 animate-spin text-gray-400" />

  // Several files at once: one after the other, each reported.
  const send = async (files: FileList | null) => {
    if (!files?.length) return
    setBusy(true)
    setNotice('')
    setFailure('')
    const added: string[] = []
    try {
      for (const file of Array.from(files)) {
        try {
          const f = await addFont({ file })
          added.push(`${f.family} ${f.style}`.trim())
        } catch (e) {
          setFailure(`${file.name} : ${message(e)}`)
          break
        }
      }
    } finally {
      setBusy(false)
      if (added.length) setNotice(t('fonts.added', { fonts: added.join(', ') }))
      void refresh()
    }
  }
  const drop = (f: AddedFont) => {
    if (window.confirm(t('fonts.confirmRemove', { font: `${f.family} ${f.style}`.trim() }))) remove.mutate({ id: f.id })
  }
  const installed = data.installed.filter((f) => f.toLowerCase().includes(filter.trim().toLowerCase()))

  return (
    <div className="space-y-6">
      <SettingsCard
        title={t('fonts.title')}
        actions={
          <label className={`btn-primary inline-flex items-center gap-2 text-sm cursor-pointer ${busy ? 'opacity-60 pointer-events-none' : ''}`}>
            {busy ? <Loader2 className="w-4 h-4 animate-spin" /> : <Upload className="w-4 h-4" />} {t('fonts.add')}
            <input
              type="file"
              multiple
              accept=".ttf,.otf,.ttc,font/ttf,font/otf,font/collection"
              className="hidden"
              aria-label={t('fonts.add')}
              onChange={(e) => { void send(e.target.files); e.target.value = '' }}
            />
          </label>
        }
      >
        <p className="mb-4 max-w-3xl">{t('fonts.help')}</p>
        {failure && <p className="text-sm text-red-700 mb-3" role="alert">{failure}</p>}
        {remove.error && <p className="text-sm text-red-700 mb-3" role="alert">{message(remove.error)}</p>}
        {notice && !failure && <p className="text-sm text-emerald-700 dark:text-emerald-400 mb-3" role="status">{notice}</p>}
        {data.added.length === 0 ? (
          <p className="text-gray-500 dark:text-gray-400">{t('fonts.none')}</p>
        ) : (
          <div className="overflow-x-auto -mx-3">
            <table className="w-full text-sm">
              <thead>
                <tr>
                  <th className={th}>{t('fonts.family')}</th>
                  <th className={th}>{t('fonts.style')}</th>
                  <th className={th}>{t('fonts.file')}</th>
                  <th className={th}>{t('fonts.addedBy')}</th>
                  <th className={th}><span className="sr-only">{t('common.delete')}</span></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                {data.added.map((f) => (
                  <tr key={f.id}>
                    <td className={`${td} font-medium text-gray-900 dark:text-gray-100`}>{f.family}</td>
                    <td className={td}>{f.style || '—'}</td>
                    <td className={td}><span className="break-all">{f.file}</span> <span className="text-xs text-gray-400">· {number(f.size / 1024)} Ko</span></td>
                    <td className={`${td} text-gray-500 dark:text-gray-400`}>{f.createdBy} · {date(f.createdAt)}</td>
                    <td className={`${td} text-right`}>
                      <button type="button" className="p-1.5 text-gray-400 hover:text-red-600" aria-label={t('common.delete')} disabled={remove.isPending} onClick={() => drop(f)}>
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </SettingsCard>

      <SettingsCard title={t('fonts.missing')}>
        <p className="mb-4 max-w-3xl">{t('fonts.missingHelp')}</p>
        {data.installed.length === 0 ? (
          <p className="text-gray-500 dark:text-gray-400">{t('fonts.unknown')}</p>
        ) : data.missing.length === 0 ? (
          <p className="text-emerald-700 dark:text-emerald-400">{t('fonts.noneMissing')}</p>
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-700 -my-2">
            {data.missing.map((m) => (
              <li key={m.family} className="py-2">
                <details>
                  <summary className="cursor-pointer select-none">
                    <span className="font-medium text-gray-900 dark:text-gray-100">{m.family}</span>
                    <span className="text-gray-500 dark:text-gray-400"> · {t('fonts.usedBy', { n: m.templates.length })}</span>
                    {m.substitute
                      ? <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300">{t('fonts.substitute', { font: m.substitute })}</span>
                      : <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{t('fonts.absent')}</span>}
                  </summary>
                  <ul className="mt-1 ml-4 text-xs text-gray-500 dark:text-gray-400 font-mono space-y-0.5">
                    {m.templates.map((p) => <li key={p} className="break-all">{p}</li>)}
                  </ul>
                </details>
              </li>
            ))}
          </ul>
        )}
      </SettingsCard>

      <SettingsCard title={t('fonts.installed', { n: data.installed.length })}>
        <input className="input max-w-xs mb-3" placeholder={t('fonts.filter')} aria-label={t('fonts.filter')} value={filter} onChange={(e) => setFilter(e.target.value)} />
        <div className="flex flex-wrap gap-1.5 max-h-72 overflow-y-auto">
          {installed.map((f) => <span key={f} className="text-xs px-2 py-1 rounded-md bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-200">{f}</span>)}
        </div>
      </SettingsCard>
    </div>
  )
}
