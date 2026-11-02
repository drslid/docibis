import { useState } from 'react'
import { Link } from 'react-router'
import { CheckCircle2, Circle, Rocket, X } from 'lucide-react'
import { useListTemplates, type Dashboard } from '@/api/generated/docibis'
import { atLeast, useMe } from '@/hooks/useMe'
import { useI18n, type Key } from '@/i18n/useI18n'

const HIDDEN = 'docibis_first_steps_hidden'
export const READ_SYNTAX = 'docibis_read_syntax'

function stored(key: string): boolean {
  try {
    return localStorage.getItem(key) === '1'
  } catch {
    return false
  }
}

/** The first steps, ticked as they are done, until all are done or the list is closed. */
export function FirstSteps({ dashboard }: { dashboard: Dashboard }) {
  const { t } = useI18n()
  const me = useMe()
  const { data: templates } = useListTemplates(undefined, { query: { retry: false } })
  const [hidden, setHidden] = useState(() => stored(HIDDEN))
  const [readSyntax] = useState(() => stored(READ_SYNTAX))
  const used = (source: string) => (dashboard.sources.find((s) => s.name === source)?.count ?? 0) > 0
  const steps: { label: Key; done: boolean; to: string; role?: 'editor' | 'admin' }[] = [
    { label: 'first.templates', done: (templates?.length ?? 0) > 0, to: '/modeles' },
    { label: 'first.try', done: used('console'), to: '/essai', role: 'editor' },
    { label: 'first.syntax', done: readSyntax, to: '/aide/syntaxe' },
    { label: 'first.key', done: dashboard.activeKeys > 0, to: '/cles-api', role: 'admin' },
    { label: 'first.api', done: used('api'), to: '/aide/api' },
  ]
  const mine = steps.filter((s) => !s.role || atLeast(me, s.role))
  const done = mine.filter((s) => s.done).length
  if (hidden || !templates || done === mine.length) return null
  const hide = () => {
    try {
      localStorage.setItem(HIDDEN, '1')
    } catch {
      // Hidden until the page is closed.
    }
    setHidden(true)
  }

  return (
    <section className="card p-4" aria-label={t('first.title')}>
      <div className="flex items-start gap-3">
        <Rocket className="w-5 h-5 mt-0.5 text-brand-600 shrink-0" />
        <div className="flex-1 min-w-0">
          <div className="flex flex-wrap items-baseline gap-2">
            <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{t('first.title')}</h2>
            <span className="text-xs text-gray-500">{t('first.progress', { done, total: mine.length })}</span>
          </div>
          <div className="h-1.5 rounded-full bg-gray-100 dark:bg-gray-700 mt-2 mb-3 max-w-md">
            <div className="h-1.5 rounded-full bg-brand-500 transition-all" style={{ width: `${(done / mine.length) * 100}%` }} />
          </div>
          <ol className="space-y-1.5">
            {mine.map((s) => (
              <li key={s.label} className="flex items-center gap-2 text-sm">
                {s.done ? <CheckCircle2 className="w-4 h-4 text-green-600 shrink-0" /> : <Circle className="w-4 h-4 text-gray-300 shrink-0" />}
                <span className={s.done ? 'text-gray-400 line-through' : 'text-gray-700 dark:text-gray-300'}>{t(s.label)}</span>
                {!s.done && <Link to={s.to} className="text-brand-700 dark:text-brand-400 hover:underline whitespace-nowrap">{t('first.go')}</Link>}
              </li>
            ))}
          </ol>
        </div>
        <button onClick={hide} className="p-1 text-gray-400 hover:text-gray-700" aria-label={t('first.hide')} title={t('first.hide')}><X className="w-4 h-4" /></button>
      </div>
    </section>
  )
}
