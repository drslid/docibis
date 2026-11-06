import { ShieldAlert, ShieldCheck, ShieldQuestion } from 'lucide-react'
import type { ImportItemAction, ImportReport } from '@/api/generated/docibis'
import { useI18n, type Key } from '@/i18n/useI18n'

const ACTIONS: Record<ImportItemAction, { label: Key; colour: string }> = {
  created: { label: 'import.created', colour: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300' },
  updated: { label: 'import.updated', colour: 'bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300' },
  unchanged: { label: 'import.unchanged', colour: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300' },
  failed: { label: 'import.failed', colour: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300' },
}

/** Who signed a zip of templates. */
function SignatureBadge({ report }: { report: ImportReport }) {
  const { t, date } = useI18n()
  const s = report.signature
  if (!s) return null
  if (s.status === 'trusted') {
    return (
      <p className="flex items-center gap-2 text-emerald-700 dark:text-emerald-400">
        <ShieldCheck className="w-4 h-4 shrink-0" /> {t('import.trusted', { signer: s.signer ?? '', date: date(s.signedAt) })}
      </p>
    )
  }
  if (s.status === 'untrusted') {
    return (
      <p className="flex items-center gap-2 text-amber-700 dark:text-amber-400">
        <ShieldAlert className="w-4 h-4 shrink-0" /> {t('import.untrusted', { instance: s.instance ?? '?', date: date(s.signedAt) })}
      </p>
    )
  }
  return (
    <p className="flex items-center gap-2 text-gray-500 dark:text-gray-400">
      <ShieldQuestion className="w-4 h-4 shrink-0" /> {t('import.unsigned')}
    </p>
  )
}

/** What an import did, or would do: its signature, its counts and each template. */
export function ImportSummary({ report }: { report: ImportReport }) {
  const { t } = useI18n()
  const items = report.items ?? []
  return (
    <div className="text-sm space-y-2" aria-live="polite">
      <SignatureBadge report={report} />
      <p className="font-medium">
        {report.dryRun && <span className="mr-1">{t('import.wouldBe')}</span>}
        {t('lib.importReport', { created: report.created, updated: report.updated, unchanged: report.unchanged, references: report.references })}
      </p>
      {!!report.aliases && <p>{t('lib.importAliases', { n: report.aliases })}</p>}
      {report.errors.length > 0 && (
        <>
          <p className="text-red-700 dark:text-red-400">{t('lib.importErrors')}</p>
          <ul className="list-disc pl-5 text-red-700 dark:text-red-400">{report.errors.map((e) => <li key={e}>{e}</li>)}</ul>
        </>
      )}
      {items.length > 0 && (
        <details open={!!report.dryRun && items.length <= 20}>
          <summary className="cursor-pointer select-none text-gray-600 dark:text-gray-300">{t('import.details', { n: items.length })}</summary>
          <ul className="mt-2 max-h-80 overflow-y-auto divide-y divide-gray-100 dark:divide-gray-700">
            {items.map((i) => (
              <li key={i.path} className="py-1.5 flex flex-wrap items-center gap-2">
                <span className={`text-[11px] px-2 py-0.5 rounded-full ${ACTIONS[i.action].colour}`}>{t(ACTIONS[i.action].label)}</span>
                <span className="font-mono text-xs break-all">{i.path}</span>
                {i.reference && <span className="text-xs text-gray-500 dark:text-gray-400">· {t('import.withReference')}</span>}
                {i.native && <span className="text-xs text-gray-500 dark:text-gray-400">· {t('import.withNative')}</span>}
                {!!i.aliases && <span className="text-xs text-gray-500 dark:text-gray-400">· {t('lib.importAliases', { n: i.aliases })}</span>}
              </li>
            ))}
          </ul>
        </details>
      )}
    </div>
  )
}
