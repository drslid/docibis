import { useState } from 'react'
import { Link } from 'react-router'
import { keepPreviousData } from '@tanstack/react-query'
import { Activity, Bot, BookOpen, CheckCircle2, Code2, Download, KeyRound, LifeBuoy, Plug, ScrollText, XCircle, type LucideIcon } from 'lucide-react'
import { useGetMcpInfo, useListMcpCalls } from '@/api/generated/docibis'
import { CopyButton } from '@/components/common/CopyButton'
import { Pager } from '@/components/common/ListTools'
import { PageTitle } from '@/components/layout/PageTitle'
import { atLeast, useMe } from '@/hooks/useMe'
import { useI18n } from '@/i18n/useI18n'

interface McpInfo {
  available: boolean
  serverName: string
  url: string
  authHeader: string
}

function vscodeConfiguration(info: McpInfo): string {
  return JSON.stringify(
    {
      inputs: [{ type: 'promptString', id: 'docibis-api-key', description: 'Jeton personnel DocIbis (dit_…, page Profil)', password: true }],
      servers: { [info.serverName]: { type: 'http', url: info.url, headers: { Authorization: 'Bearer ${input:docibis-api-key}' } } },
    },
    null,
    2,
  )
}

function vscodeInstallLink(info: McpInfo): string {
  const server = {
    name: info.serverName,
    type: 'http',
    url: info.url,
    headers: { Authorization: 'Bearer ${input:docibis-api-key}' },
    inputs: [{ type: 'promptString', id: 'docibis-api-key', description: 'Jeton personnel DocIbis (dit_…, page Profil)', password: true }],
  }
  return `vscode:mcp/install?${encodeURIComponent(JSON.stringify(server))}`
}

function Code({ text, label }: { text: string; label: string }) {
  return (
    <div className="relative">
      <pre className="bg-gray-900 text-gray-100 rounded-lg p-4 pr-24 text-xs leading-relaxed overflow-x-auto" aria-label={label}>
        <code>{text}</code>
      </pre>
      <CopyButton text={text} className="absolute top-2 right-2 py-1 px-2 text-xs" />
    </div>
  )
}

/** The MCP calls of the account (of everyone for an administrator). */
function McpCalls() {
  const { t, date } = useI18n()
  const me = useMe()
  const admin = atLeast(me, 'admin')
  const [all, setAll] = useState(false)
  const [offset, setOffset] = useState(0)
  const LIMIT = 25
  const { data, isPending, error } = useListMcpCalls(
    { limit: LIMIT, offset, ...(all && { all: true }) },
    { query: { placeholderData: keepPreviousData, refetchInterval: 15_000 } },
  )
  const items = data?.items ?? []
  return (
    <section id="calls" className="card mb-6 scroll-mt-6">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-2">
        <h2 className="flex items-center gap-2 text-lg font-semibold">
          <Activity className="w-5 h-5 text-emerald-500" /> {t('calls.title')}
        </h2>
        {admin && (
          <div className="flex gap-1 bg-gray-100 dark:bg-gray-800 rounded-lg p-1">
            {[false, true].map((value) => (
              <button
                key={String(value)}
                type="button"
                onClick={() => {
                  setAll(value)
                  setOffset(0)
                }}
                className={`px-3 py-1 rounded-md text-xs font-medium ${all === value ? 'bg-brand-600 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200'}`}
              >
                {value ? t('calls.all') : t('calls.mine')}
              </button>
            ))}
          </div>
        )}
      </div>
      <p className="text-sm text-gray-600 dark:text-gray-300 mb-4">{t('calls.intro')}</p>
      {error ? (
        <p className="text-sm text-red-700" role="alert">{t('common.unavailable', { message: error.message })}</p>
      ) : isPending ? (
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      ) : items.length === 0 ? (
        <div className="text-center py-8 text-gray-500 dark:text-gray-400">
          <Activity className="w-10 h-10 mx-auto mb-3 opacity-40" />
          <p>{t('calls.none')}</p>
          <p className="text-sm mt-1">{t('calls.noneHint')}</p>
        </div>
      ) : (
        <>
          <ul className="divide-y divide-gray-100 dark:divide-gray-700">
            {items.map((call) => (
              <li key={call.id} className="py-3 flex items-center gap-3 flex-wrap">
                {call.status === 'ok' ? (
                  <CheckCircle2 className="w-5 h-5 shrink-0 text-emerald-600 dark:text-emerald-400" />
                ) : (
                  <XCircle className="w-5 h-5 shrink-0 text-red-600 dark:text-red-400" />
                )}
                <div className="flex-1 min-w-[12rem]">
                  <div className="text-sm font-medium text-gray-900 dark:text-gray-100">{call.tool || call.method}</div>
                  <div className="text-xs text-gray-500 dark:text-gray-400 font-mono">{call.method}</div>
                  {call.error && <div className="text-xs text-red-600 dark:text-red-400 mt-0.5 break-words">{call.error}</div>}
                </div>
                <span
                  className={`inline-flex items-center gap-1.5 text-xs font-medium px-2 py-1 rounded ${
                    call.via === 'token' ? 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300' : 'bg-violet-100 text-violet-700 dark:bg-violet-900/40 dark:text-violet-300'
                  }`}
                >
                  {call.via === 'token' ? <Plug className="w-3.5 h-3.5" /> : <KeyRound className="w-3.5 h-3.5" />}
                  {call.via === 'token' ? t('calls.token') : t('calls.key')}
                </span>
                <span className="text-xs text-gray-500 dark:text-gray-400 w-20 text-right tabular-nums">{call.durationMs} ms</span>
                <span className="text-xs text-gray-500 dark:text-gray-400 w-32 text-right">{date(call.createdAt)}</span>
                <span className="text-xs text-gray-400 dark:text-gray-500 w-28 text-right truncate" title={call.actor}>
                  {call.actor}
                </span>
              </li>
            ))}
          </ul>
          <Pager offset={offset} limit={LIMIT} total={data?.total ?? 0} onChange={setOffset} />
        </>
      )}
    </section>
  )
}

/** Connecting applications and AI assistants. */
export function IntegrationsPage() {
  const { t } = useI18n()
  const me = useMe()
  const origin = window.location.origin
  const { data: info } = useGetMcpInfo({ query: { staleTime: 300_000, retry: false } })
  const mcp: McpInfo | null = info?.available ? { ...info, url: origin + new URL(info.url, origin).pathname } : null

  const links: [string, LucideIcon, string, string][] = [
    ['#mcp', Bot, t('integrations.mcpTitle'), 'text-indigo-500'],
    ['#calls', Activity, t('calls.title'), 'text-emerald-500'],
    ['/profil?section=tokens', Plug, t('tokens.title'), 'text-indigo-500'],
    ['#api', Code2, t('integrations.apiTitle'), 'text-violet-500'],
    ['/docs', ScrollText, t('help.reference'), 'text-sky-500'],
    ['/aide/mcp', LifeBuoy, t('integrations.helpMcp'), 'text-teal-500'],
    ['/aide/api', LifeBuoy, t('integrations.helpApi'), 'text-teal-500'],
  ]
  if (atLeast(me, 'admin')) links.splice(2, 0, ['/cles-api', KeyRound, t('nav.keys'), 'text-amber-500'])

  return (
    <div>
      <PageTitle>{t('nav.integrations')}</PageTitle>
      <p className="text-sm text-gray-500 dark:text-gray-400 mb-5">{t('integrations.intro')}</p>

      <div className="flex flex-wrap gap-2 mb-6">
        {links.map(([to, Icon, label, colour]) =>
          to.startsWith('#') ? (
            <a key={to} href={to} className="btn-secondary inline-flex items-center gap-2 text-sm py-1.5">
              <Icon className={`w-4 h-4 ${colour}`} /> {label}
            </a>
          ) : (
            <Link key={to} to={to} className="btn-secondary inline-flex items-center gap-2 text-sm py-1.5">
              <Icon className={`w-4 h-4 ${colour}`} /> {label}
            </Link>
          ),
        )}
      </div>

      <section id="mcp" className="card mb-6 scroll-mt-6">
        <h2 className="flex items-center gap-2 text-lg font-semibold mb-2">
          <Bot className="w-5 h-5 text-indigo-500" /> {t('integrations.mcpTitle')}
        </h2>
        <p className="text-sm text-gray-600 dark:text-gray-300 mb-4">{t('integrations.mcpIntro')}</p>
        {!mcp ? (
          <p className="text-sm text-gray-500">{t('integrations.mcpUnavailable')}</p>
        ) : (
          <div className="space-y-5 text-sm">
            <div>
              <div className="font-semibold mb-1">{t('integrations.step1')}</div>
              <p className="text-gray-600 dark:text-gray-300">{t('integrations.step1Token', { header: 'Authorization: Bearer dit_…' })}</p>
              <Link to="/profil?section=tokens" className="btn-secondary inline-flex items-center gap-2 text-sm py-1.5 mt-2">
                <Plug className="w-4 h-4 text-indigo-500" /> {t('tokens.manage')}
              </Link>
            </div>
            <div>
              <div className="font-semibold mb-1">{t('integrations.step2')}</div>
              <Code text={mcp.url} label={t('integrations.step2')} />
            </div>
            <div>
              <div className="font-semibold mb-1">{t('integrations.step3')}</div>
              <p className="text-gray-600 dark:text-gray-300 mb-2">{t('integrations.vscode')}</p>
              <Code text={vscodeConfiguration(mcp)} label="mcp.json" />
              <div className="flex flex-wrap gap-2 mt-3">
                <a href={vscodeInstallLink(mcp)} className="btn-primary inline-flex items-center gap-2 text-sm py-1.5">
                  <Download className="w-4 h-4" /> {t('integrations.install')}
                </a>
                <Link to="/aide/mcp" className="btn-ghost inline-flex items-center gap-2 text-sm py-1.5">
                  <LifeBuoy className="w-4 h-4" /> {t('integrations.helpMcp')}
                </Link>
              </div>
            </div>
          </div>
        )}
      </section>

      <McpCalls />

      <section id="api" className="card scroll-mt-6">
        <h2 className="flex items-center gap-2 text-lg font-semibold mb-2">
          <Code2 className="w-5 h-5 text-violet-500" /> {t('integrations.apiTitle')}
        </h2>
        <p className="text-sm text-gray-600 dark:text-gray-300 mb-3">{t('integrations.apiIntro')}</p>
        <Code text={`POST ${origin}/v1/render`} label="POST /v1/render" />
        <div className="flex flex-wrap gap-2 mt-3">
          <Link to="/docs" className="btn-primary inline-flex items-center gap-2 text-sm py-1.5">
            <ScrollText className="w-4 h-4" /> {t('help.reference')}
          </Link>
          <Link to="/aide/api" className="btn-ghost inline-flex items-center gap-2 text-sm py-1.5">
            <BookOpen className="w-4 h-4" /> {t('integrations.helpApi')}
          </Link>
        </div>
      </section>
    </div>
  )
}
