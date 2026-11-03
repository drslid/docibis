import { useRef } from 'react'
import { Download, FileCode2, RefreshCw, Rocket, Trash2, Upload } from 'lucide-react'
import { useQueryClient } from '@tanstack/react-query'
import {
  getGetTemplateQueryKey, getListTemplatesQueryKey, useCheckTemplateNative, useDeleteTemplateNative, useSetTemplateNative,
  type LibraryVersion,
} from '@/api/generated/docibis'
import { ApiError } from '@/api/fetcher'
import { HelpTip } from '@/components/help/HelpTip'
import { useI18n } from '@/i18n/useI18n'

const TONES = {
  pending: 'bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300',
  qualified: 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300',
  refused: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  error: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300',
}

function problem(error: unknown): string {
  return error instanceof ApiError ? (error.problem?.detail ?? error.message) : String(error)
}

/** The native engine of the served version: its Typst copy, the comparison with LibreOffice, and for administrators the copy to attach or remove. */
export function NativePanel({ id, version, editor, admin }: { id: number; version?: LibraryVersion; editor: boolean; admin: boolean }) {
  const { t, date, number } = useI18n()
  const queryClient = useQueryClient()
  const file = useRef<HTMLInputElement>(null)
  const refresh = {
    mutation: {
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey: getGetTemplateQueryKey(id) })
        queryClient.invalidateQueries({ queryKey: getListTemplatesQueryKey() })
      },
    },
  }
  const attach = useSetTemplateNative(refresh)
  const check = useCheckTemplateNative(refresh)
  const remove = useDeleteTemplateNative(refresh)
  const native = version?.native
  const error = attach.error ?? check.error ?? remove.error
  const busy = attach.isPending || check.isPending || remove.isPending
  return (
    <section className="card">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-1">
        <h2 className="text-lg font-semibold inline-flex items-center gap-2">
          <FileCode2 className="w-5 h-5 text-brand-600" /> {t('native.title')} <HelpTip>{t('tip.native')}</HelpTip>
        </h2>
        {version && (
          <div className="flex flex-wrap gap-1">
            {native && editor && (
              <a className="btn-ghost py-1 text-sm inline-flex items-center gap-1" download={`version-${version.number}.typ`}
                href={`/api/templates/${id}/versions/${version.number}/native`}>
                <Download className="w-4 h-4" /> {t('native.download')}
              </a>
            )}
            {native && editor && (
              <button className="btn-ghost py-1 text-sm inline-flex items-center gap-1" disabled={busy}
                onClick={() => check.mutate({ id, number: version.number })}>
                <RefreshCw className={`w-4 h-4 ${check.isPending ? 'animate-spin' : ''}`} /> {t('native.check')}
              </button>
            )}
            {admin && (
              <>
                <input ref={file} type="file" accept=".typ" className="hidden" aria-label={t('native.attach')}
                  onChange={(e) => {
                    const source = e.target.files?.[0]
                    if (source) attach.mutate({ id, number: version.number, data: { source } })
                    e.target.value = ''
                  }} />
                <button className="btn-ghost py-1 text-sm inline-flex items-center gap-1" disabled={busy} onClick={() => file.current?.click()}>
                  <Upload className={`w-4 h-4 ${attach.isPending ? 'animate-pulse' : ''}`} /> {native ? t('native.replace') : t('native.attach')}
                </button>
              </>
            )}
            {native && admin && (
              <button className="btn-ghost py-1 text-sm inline-flex items-center gap-1 text-red-700 dark:text-red-400" disabled={busy}
                onClick={() => { if (window.confirm(t('native.removeConfirm'))) remove.mutate({ id, number: version.number }) }}>
                <Trash2 className="w-4 h-4" /> {t('native.remove')}
              </button>
            )}
          </div>
        )}
      </div>
      <p className="text-xs text-gray-500 mb-3">{t('native.intro')}</p>
      {!version ? (
        <p className="text-sm text-gray-500">{t('native.unpublished')}</p>
      ) : !native ? (
        <p className="text-sm text-gray-500">{t('native.none')}</p>
      ) : (
        <div className="space-y-1 text-sm">
          <span className={`inline-flex items-center gap-1 text-xs font-medium px-2 py-0.5 rounded-full ${TONES[native.state]}`}>
            {native.state === 'qualified' && <Rocket className="w-3 h-3" />} {t(`native.state.${native.state}`)}
          </span>
          {native.reason && <p className="text-gray-600 dark:text-gray-300">{native.reason}</p>}
          {native.permille !== undefined && native.tests !== undefined && (
            <p className="text-gray-600 dark:text-gray-300">{t('native.measure', { permille: number(native.permille, 2), tests: native.tests })}</p>
          )}
          {native.nativeMs !== undefined && native.standardMs !== undefined && (
            <p className="text-gray-600 dark:text-gray-300">{t('native.speed', { native: number(native.nativeMs, 1), standard: number(native.standardMs) })}</p>
          )}
          <p className="text-xs text-gray-400">
            {t('native.attached', { sha: native.sourceSha256.slice(0, 12), by: native.by ?? '—', date: native.attachedAt ? date(native.attachedAt) : '—' })}
            {native.at && <> · {t('native.at', { date: date(native.at) })}</>}
          </p>
        </div>
      )}
      {error && <p className="text-sm text-red-700 dark:text-red-400 mt-2" role="alert">{problem(error)}</p>}
    </section>
  )
}
