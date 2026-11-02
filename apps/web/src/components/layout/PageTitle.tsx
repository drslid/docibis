import { createElement, useState, type ReactNode } from 'react'
import { Link, useLocation } from 'react-router'
import { CircleHelp, Lightbulb, Megaphone, Moon, Sun, X } from 'lucide-react'
import { useGetInfo, useListReleases } from '@/api/generated/docibis'
import { Breadcrumb, type Crumb } from '@/components/common/Breadcrumb'
import { useReleaseSeen } from '@/hooks/useReleaseSeen'
import { useTheme } from '@/hooks/useTheme'
import { LanguagePicker } from '@/i18n/LanguagePicker'
import { useI18n, type Key } from '@/i18n/useI18n'

// Colours of the environment badge.
const ENV_BADGE_COLORS: Record<string, string> = {
  DEV: 'bg-gray-200 dark:bg-gray-700 text-gray-800 dark:text-gray-200',
  INT: 'bg-blue-100 dark:bg-blue-900/40 text-blue-700 dark:text-blue-300',
  REC: 'bg-amber-100 dark:bg-amber-900/40 text-amber-700 dark:text-amber-300',
  PPROD: 'bg-purple-100 dark:bg-purple-900/40 text-purple-700 dark:text-purple-300',
  PROD: 'bg-red-100 dark:bg-red-900/40 text-red-700 dark:text-red-300',
}

const round = 'p-2 rounded-full bg-gray-200 dark:bg-gray-700 text-gray-700 dark:text-gray-300 hover:bg-gray-300 dark:hover:bg-gray-600 transition-colors'

/** Environment badge, what's new, page help, dark mode and language, on the title row. */
export function PageTopTools({ onHelp, helpOpen }: { onHelp?: () => void; helpOpen?: boolean }) {
  const { data: info } = useGetInfo({ query: { retry: false } })
  const { data: releases } = useListReleases({ limit: 1 }, { query: { retry: false, staleTime: 300_000 } })
  const seen = useReleaseSeen((s) => s.seen)
  const unseen = (releases?.items?.[0]?.id ?? 0) > seen
  const { theme, toggle } = useTheme()
  const { t } = useI18n()
  const env = info?.instance.toUpperCase()

  return (
    <div className="flex items-center gap-2 ml-auto shrink-0">
      {env && (
        <span className={`text-sm font-semibold px-3 py-1 rounded-full ${ENV_BADGE_COLORS[env] ?? 'bg-gray-200 text-gray-800'}`}>
          {env}
        </span>
      )}
      {onHelp && (
        <button onClick={onHelp} className={`${round} ${helpOpen ? 'ring-2 ring-brand-500' : ''}`} aria-label={t('guide.toggle')} aria-pressed={helpOpen} title={t('guide.toggle')}>
          <CircleHelp className="w-4 h-4" />
        </button>
      )}
      <Link to="/nouveautes" className={`relative ${round}`} aria-label={unseen ? `${t('nav.releases')} · ${t('rel.unseen')}` : t('nav.releases')} title={t('nav.releases')}>
        <Megaphone className="w-4 h-4" />
        {unseen && <span className="absolute -top-0.5 -right-0.5 w-2.5 h-2.5 bg-red-500 rounded-full border-2 border-white dark:border-gray-700" />}
      </Link>
      <button onClick={toggle} className={round} aria-label={theme === 'dark' ? t('top.light') : t('top.dark')}>
        {theme === 'dark' ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
      </button>
      <LanguagePicker />
    </div>
  )
}

// The page a path shows, for its breadcrumb and its guide.
function section(pathname: string): string {
  const parts = pathname.split('/').filter(Boolean)
  if (parts.length === 0) return 'dashboard'
  if (parts[0] === 'modeles' && parts[1]) return 'template'
  return parts[0]
}

const LABELS: Record<string, Key> = {
  dashboard: 'nav.dashboard', modeles: 'nav.library', template: 'nav.library', liasses: 'nav.bundles', essai: 'nav.try',
  documents: 'nav.history', integrations: 'nav.integrations', aide: 'nav.help', docs: 'nav.help', nouveautes: 'nav.releases',
  monitoring: 'nav.monitoring', journal: 'nav.audit', utilisateurs: 'nav.users', 'cles-api': 'nav.keys', configuration: 'nav.config', profil: 'nav.profile',
}

// What to do on each page, shown until the user closes it.
const GUIDES: Record<string, { steps: Key[]; help?: string }> = {
  dashboard: { steps: ['guide.dashboard.1', 'guide.dashboard.2', 'guide.dashboard.3'], help: 'demarrer' },
  modeles: { steps: ['guide.library.1', 'guide.library.2', 'guide.library.3'], help: 'syntaxe' },
  template: { steps: ['guide.template.1', 'guide.template.2', 'guide.template.3'], help: 'syntaxe' },
  liasses: { steps: ['guide.bundles.1', 'guide.bundles.2', 'guide.bundles.3'], help: 'syntaxe' },
  essai: { steps: ['guide.try.1', 'guide.try.2', 'guide.try.3'], help: 'syntaxe' },
  documents: { steps: ['guide.history.1', 'guide.history.2', 'guide.history.3'], help: 'demarrer' },
  integrations: { steps: ['guide.integrations.1', 'guide.integrations.2'], help: 'mcp' },
  monitoring: { steps: ['guide.monitoring.1', 'guide.monitoring.2'] },
  journal: { steps: ['guide.audit.1', 'guide.audit.2'] },
  utilisateurs: { steps: ['guide.users.1', 'guide.users.2'], help: 'demarrer' },
  'cles-api': { steps: ['guide.keys.1', 'guide.keys.2', 'guide.keys.3'], help: 'api' },
  configuration: { steps: ['guide.config.1', 'guide.config.2'] },
  profil: { steps: ['guide.profile.1', 'guide.profile.2'], help: 'mcp' },
  nouveautes: { steps: ['guide.releases.1'] },
}

const CLOSED = 'docibis_guides_closed'

function closedGuides(): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(CLOSED) ?? '[]')
    return Array.isArray(value) ? value.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

function remember(closed: string[]) {
  try {
    localStorage.setItem(CLOSED, JSON.stringify(closed))
  } catch {
    // Remembered until the page is closed.
  }
}

/** The steps of a page, in a few words: what it is for and what to do first. */
function PageGuide({ steps, help, onClose }: { steps: Key[]; help?: string; onClose: () => void }) {
  const { t } = useI18n()
  return (
    <section className="mb-6 rounded-xl border border-sky-200 bg-sky-50/70 dark:border-sky-900/60 dark:bg-sky-900/15 px-4 py-3" aria-label={t('guide.title')}>
      <div className="flex items-start gap-3">
        {createElement(Lightbulb, { className: 'w-5 h-5 mt-0.5 shrink-0 text-sky-600 dark:text-sky-400' })}
        <div className="flex-1 min-w-0">
          <h2 className="text-sm font-semibold text-sky-900 dark:text-sky-200">{t('guide.title')}</h2>
          <ol className="mt-1.5 space-y-1 text-sm text-sky-900/90 dark:text-sky-100/90 list-decimal pl-5">
            {steps.map((s) => <li key={s}>{t(s)}</li>)}
          </ol>
          {help && <Link to={`/aide/${help}`} className="inline-block mt-2 text-sm font-medium text-sky-700 dark:text-sky-300 underline">{t('guide.more')}</Link>}
        </div>
        <button onClick={onClose} className="shrink-0 p-1 rounded text-sky-700/70 hover:text-sky-900 dark:text-sky-300/70 dark:hover:text-sky-100" aria-label={t('guide.close')} title={t('guide.close')}>
          <X className="w-4 h-4" />
        </button>
      </div>
    </section>
  )
}

/**
 * Page header: the title and the tools on one row, the
 * breadcrumb under it, then the guide of the page until it is closed.
 */
export function PageTitle({ children, crumbs }: { children: ReactNode; crumbs?: Crumb[] }) {
  const { t } = useI18n()
  const { pathname } = useLocation()
  const page = section(pathname)
  const guide = GUIDES[page]
  const [closed, setClosed] = useState(closedGuides)
  const open = !!guide && !closed.includes(page)
  const toggle = () => {
    const next = open ? [...closed, page] : closed.filter((x) => x !== page)
    remember(next)
    setClosed(next)
  }
  const items = crumbs ?? (LABELS[page] ? [{ label: t(LABELS[page]) }] : [])

  return (
    <>
      <div className="flex items-center gap-4 mb-2">
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100 min-w-0">{children}</h1>
        <PageTopTools onHelp={guide ? toggle : undefined} helpOpen={open} />
      </div>
      <div className="mb-6"><Breadcrumb items={items} /></div>
      {open && <PageGuide steps={guide.steps} help={guide.help} onClose={toggle} />}
    </>
  )
}
