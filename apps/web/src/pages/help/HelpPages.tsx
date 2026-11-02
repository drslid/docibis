import { Children, isValidElement, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import ReactMarkdown, { type Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { ArrowLeft, BookOpen, FileQuestion, ScrollText } from 'lucide-react'
import { CopyButton } from '@/components/common/CopyButton'
import { EmptyState } from '@/components/common/EmptyState'
import { SearchBox } from '@/components/common/ListTools'
import { READ_SYNTAX } from '@/components/help/FirstSteps'
import { PageTitle } from '@/components/layout/PageTitle'
import { useI18n } from '@/i18n/useI18n'
import { HELP_PAGES } from './pages'

function slugify(text: string): string {
  return text
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')
}

function textOf(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(textOf).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return textOf(node.props.children)
  return ''
}

// Internal links stay in the console; files and the contract open as files.
function MarkdownLink({ href = '', children }: { href?: string; children?: ReactNode }) {
  if (href.startsWith('/') && !/\.(docx|json)$/.test(href)) return <Link to={href} className="text-brand-600 underline">{children}</Link>
  return <a href={href} className="text-brand-600 underline" {...(href.startsWith('http') ? { target: '_blank', rel: 'noreferrer' } : { download: true })}>{children}</a>
}

const components: Components = {
  h1: ({ children }) => <h1 className="text-2xl font-bold mb-4 text-gray-900 dark:text-gray-100">{children}</h1>,
  h2: ({ children }) => <h2 id={slugify(textOf(children))} className="scroll-mt-6 text-xl font-semibold mt-8 mb-3 text-gray-900 dark:text-gray-100">{children}</h2>,
  h3: ({ children }) => <h3 id={slugify(textOf(children))} className="scroll-mt-6 text-base font-semibold mt-6 mb-2 text-gray-900 dark:text-gray-100">{children}</h3>,
  p: ({ children }) => <p className="my-3 leading-relaxed">{children}</p>,
  ul: ({ children }) => <ul className="my-3 list-disc pl-6 space-y-1">{children}</ul>,
  ol: ({ children }) => <ol className="my-3 list-decimal pl-6 space-y-1">{children}</ol>,
  a: ({ href, children }) => <MarkdownLink href={href}>{children}</MarkdownLink>,
  table: ({ children }) => <div className="my-4 overflow-x-auto"><table className="w-full text-sm border border-gray-200 dark:border-gray-700">{children}</table></div>,
  th: ({ children }) => <th className="text-left px-3 py-2 bg-gray-50 dark:bg-gray-900/40 font-semibold border-b border-gray-200 dark:border-gray-700">{children}</th>,
  td: ({ children }) => <td className="px-3 py-2 border-b border-gray-100 dark:border-gray-700 align-top">{children}</td>,
  code: ({ children, className }) =>
    className ? <code className={className}>{children}</code> : <code className="px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-700 text-[0.85em]">{children}</code>,
  pre: ({ children }) => {
    const code = textOf(Children.toArray(children)).replace(/\n$/, '')
    return (
      <div className="relative my-4 group">
        <pre className="bg-gray-900 text-gray-100 rounded-lg p-4 pr-24 text-xs leading-relaxed overflow-x-auto">{children}</pre>
        <CopyButton text={code} className="absolute top-2 right-2 py-1 px-2 text-xs" />
      </div>
    )
  },
}

/** The list of guides, searched by title and content. */
export function HelpHome() {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const origin = window.location.origin
  const q = query.trim().toLowerCase()
  const pages = HELP_PAGES.filter((p) => !q || `${p.title} ${p.summary} ${p.content(origin)}`.toLowerCase().includes(q))

  return (
    <div>
      <PageTitle>{t('nav.help')}</PageTitle>
      <p className="text-sm text-gray-500 dark:text-gray-400 mb-5">{t('help.intro')}</p>
      <div className="mb-6 max-w-xl">
        <SearchBox value={query} onChange={setQuery} placeholder={t('help.search')} />
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {pages.map((p) => (
          <Link key={p.slug} to={`/aide/${p.slug}`} className="card p-5 hover:border-brand-300 dark:hover:border-brand-700 transition-colors">
            <div className="flex items-center gap-2 font-semibold text-gray-900 dark:text-gray-100">
              <p.icon className={`w-5 h-5 ${p.colour}`} /> {p.title}
            </div>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">{p.summary}</p>
          </Link>
        ))}
        {(!q || `${t('help.reference')} ${t('help.referenceText')}`.toLowerCase().includes(q)) && (
          <Link to="/docs" className="card p-5 hover:border-brand-300 dark:hover:border-brand-700 transition-colors">
            <div className="flex items-center gap-2 font-semibold text-gray-900 dark:text-gray-100">
              <ScrollText className="w-5 h-5 text-sky-500" /> {t('help.reference')}
            </div>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">{t('help.referenceText')}</p>
          </Link>
        )}
      </div>
      {pages.length === 0 && q && <p className="text-sm text-gray-500 mt-4">{t('help.none')}</p>}
      <p className="text-xs text-gray-400 mt-6">{t('help.frenchOnly')}</p>
    </div>
  )
}

/** One guide, with its table of contents built from its headings. */
export function HelpPage() {
  const { slug = '' } = useParams()
  const { t } = useI18n()
  const page = HELP_PAGES.find((p) => p.slug === slug)
  const content = useMemo(() => page?.content(window.location.origin) ?? '', [page])
  const body = useRef<HTMLDivElement>(null)
  const [toc, setToc] = useState<{ id: string; text: string; level: number }[]>([])

  useEffect(() => {
    if (slug !== 'syntaxe') return
    try {
      localStorage.setItem(READ_SYNTAX, '1')
    } catch {
      // The first steps keep this step open.
    }
  }, [slug])

  useLayoutEffect(() => {
    const root = body.current
    if (!root) return
    setToc([...root.querySelectorAll('h2[id], h3[id]')].map((el) => ({ id: el.id, text: el.textContent ?? '', level: el.tagName === 'H3' ? 3 : 2 })))
  }, [content])

  if (!page) {
    return <EmptyState icon={<FileQuestion className="w-12 h-12" />} title={t('notFound.title')} action={<Link to="/aide" className="btn-primary">{t('help.back')}</Link>} />
  }

  return (
    <div>
      <PageTitle crumbs={[{ label: t('nav.help'), to: '/aide' }, { label: page.title }]}>{t('nav.help')}</PageTitle>
      <Link to="/aide" className="inline-flex items-center gap-1 text-sm text-gray-500 hover:text-gray-800 dark:hover:text-gray-200 mb-4">
        <ArrowLeft className="w-4 h-4" /> {t('help.back')}
      </Link>
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_14rem] items-start">
        <article ref={body} className="card text-sm text-gray-700 dark:text-gray-300 min-w-0">
          <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
            {content}
          </ReactMarkdown>
        </article>
        {toc.length > 0 && (
          <nav className="hidden lg:block sticky top-8 card p-4 text-sm">
            <div className="flex items-center gap-2 font-semibold mb-2 text-gray-900 dark:text-gray-100">
              <BookOpen className="w-4 h-4 text-teal-500" /> {t('help.toc')}
            </div>
            <ul className="space-y-1">
              {toc.map((e) => (
                <li key={e.id} className={e.level === 3 ? 'pl-3' : ''}>
                  <a href={`#${e.id}`} className="text-gray-600 hover:text-brand-600 dark:text-gray-400">{e.text}</a>
                </li>
              ))}
            </ul>
          </nav>
        )}
      </div>
    </div>
  )
}
