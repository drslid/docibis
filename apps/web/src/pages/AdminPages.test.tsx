import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AuditPage as Journal, Dashboard, Info, Monitoring, RenderPage, Session, User } from '@/api/generated/docibis'
import { Sidebar } from '@/components/layout/Sidebar'
import { dictionaries } from '@/i18n/translations'
import { translate, useI18nStore } from '@/i18n/useI18n'
import { AuditPage } from './AuditPage'
import DashboardPage from './DashboardPage'
import { DocumentsPage } from './DocumentsPage'
import { MonitoringPage } from './MonitoringPage'
import { UsersPage } from './UsersPage'

const info: Info = { name: 'DocIbis', version: '0.1.0', instance: 'INT', database: { engine: 'sqlite', replication: { enabled: false, locked: false } } }
const admin: User = { id: 1, username: 'admin', firstName: 'Alice', lastName: 'Admin', displayName: 'Alice Admin', email: 'alice@example.fr', role: 'admin', active: true, language: 'fr', createdAt: '2026-10-01T08:00:00Z' }
const viewer: User = { ...admin, id: 2, username: 'victor', firstName: 'Victor', lastName: '', displayName: 'Victor', email: '', role: 'viewer' }

function json(body: unknown, status = 200) {
  return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

type Call = { url: string; method: string; body?: unknown }

/** Answers the console calls; route answers the page ones. */
function serve(route: (url: string, method: string) => unknown, user: User = admin): Call[] {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined })
      if (url === '/api/auth/session') return json({ authenticated: true, setupRequired: false, instance: 'INT', user } satisfies Session)
      if (url === '/api/info') return json(info)
      const body = route(url, method)
      return body === undefined ? json({ status: 404, title: 'Not Found' }, 404) : json(body)
    }),
  )
  return calls
}

function show(page: React.ReactNode) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>{page}</MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => useI18nStore.setState({ locale: 'fr', chosen: false }))

describe('translations', () => {
  it('cover every text in the four languages', () => {
    for (const [locale, texts] of Object.entries(dictionaries)) {
      for (const key of Object.keys(dictionaries.fr)) expect(texts[key as keyof typeof texts], `${locale}: ${key}`).toBeTruthy()
    }
    expect(translate('en', 'common.range', { from: 1, to: 50, total: 120 })).toBe('1–50 of 120')
    expect(translate('de', 'nav.users')).toBe('Benutzer')
  })
})

describe('Sidebar', () => {
  const sidebar = <Sidebar mobileOpen={false} onMobileClose={() => {}} collapsed={false} onToggleCollapse={() => {}} />

  it('shows a viewer only the pages of a viewer', async () => {
    serve(() => undefined, viewer)
    show(sidebar)
    expect(await screen.findByText('Victor')).toBeInTheDocument()
    expect(screen.getByText('Lecteur')).toBeInTheDocument()
    expect(screen.getByText('Documents produits')).toBeInTheDocument()
    expect(screen.queryByText('Essai')).not.toBeInTheDocument()
    expect(screen.queryByText('Utilisateurs')).not.toBeInTheDocument()
    expect(screen.queryByText("Journal d'audit")).not.toBeInTheDocument()
  })

  it('shows an administrator everything', async () => {
    serve(() => undefined)
    show(sidebar)
    expect(await screen.findByText('Utilisateurs')).toBeInTheDocument()
    for (const label of ['Essai', 'Intégrations', 'Monitoring', "Journal d'audit", 'Clés API', 'Configuration', 'Aide']) expect(screen.getByText(label)).toBeInTheDocument()
  })
})

describe('UsersPage', () => {
  it('lists, searches, creates and disables accounts', async () => {
    const calls = serve((url, method) => {
      if (url.startsWith('/api/users') && method === 'GET') return [admin, viewer]
      if (url === '/api/users' && method === 'POST') return { ...viewer, id: 3, username: 'eve', role: 'editor' }
      if (url === '/api/users/2' && method === 'PATCH') return { ...viewer, active: false }
    })
    show(<UsersPage />)
    expect(await screen.findByText(/alice@example\.fr/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Supprimer admin' })).toBeDisabled()

    fireEvent.change(screen.getByLabelText(/Rechercher un nom/), { target: { value: 'vic' } })
    await waitFor(() => expect(calls.some((c) => c.url === '/api/users?q=vic')).toBe(true))

    fireEvent.click(screen.getByRole('button', { name: 'Désactiver victor' }))
    await waitFor(() => expect(calls).toContainEqual({ url: '/api/users/2', method: 'PATCH', body: { active: false } }))

    fireEvent.click(screen.getByRole('button', { name: /Nouveau compte/ }))
    const dialog = within(screen.getByRole('dialog'))
    fireEvent.change(dialog.getByLabelText('Prénom'), { target: { value: 'ève-marie' } })
    fireEvent.change(dialog.getByLabelText('Nom'), { target: { value: 'DUPONT' } })
    fireEvent.change(dialog.getByLabelText('E-mail'), { target: { value: 'Eve@Example.fr' } })
    fireEvent.change(dialog.getByLabelText('Rôle'), { target: { value: 'editor' } })
    fireEvent.change(dialog.getByLabelText(/Mot de passe provisoire/), { target: { value: 'a long enough password' } })
    fireEvent.click(screen.getByRole('button', { name: 'Créer' }))
    await waitFor(() =>
      expect(calls).toContainEqual({
        url: '/api/users',
        method: 'POST',
        body: { username: 'eve@example.fr', firstName: 'Ève-Marie', lastName: 'Dupont', email: 'eve@example.fr', role: 'editor', password: 'a long enough password' },
      }),
    )
  })

  it('switches the interface language and keeps it on the account', async () => {
    const calls = serve((url, method) => {
      if (url.startsWith('/api/users')) return [admin]
      if (url === '/api/me' && method === 'PATCH') return { ...admin, language: 'en' }
    })
    show(<UsersPage />)
    fireEvent.click(await screen.findByRole('button', { name: /Langue/ }))
    fireEvent.click(screen.getByRole('menuitemradio', { name: /English/ }))
    expect(await screen.findByRole('heading', { name: 'Users' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /New account/ })).toBeInTheDocument()
    await waitFor(() => expect(calls).toContainEqual({ url: '/api/me', method: 'PATCH', body: { language: 'en' } }))
  })
})

describe('DocumentsPage', () => {
  it('shows the history and filters it', async () => {
    const page: RenderPage = {
      total: 2,
      items: [
        { id: 2, createdAt: '2026-10-16T18:01:00Z', source: 'api', actor: 'billing-app', template: 'contrat.docx', format: 'pdf', status: 'ok', size: 52_000, durationMs: 180, engine: 'standard' },
        { id: 1, createdAt: '2026-10-16T18:00:00Z', source: 'console', actor: 'admin', template: 'tarif.docx', format: 'pdf', status: 'refused', error: 'Données JSON invalides', size: 0, durationMs: 3, engine: 'standard' },
      ],
    }
    const calls = serve((url) => (url.startsWith('/api/renders') ? page : undefined))
    show(<DocumentsPage />)
    expect(await screen.findByText('contrat.docx')).toBeInTheDocument()
    expect(screen.getByText('billing-app')).toBeInTheDocument()
    expect(screen.getByText('Données JSON invalides')).toBeInTheDocument()
    expect(screen.getAllByText('Refusé').length).toBeGreaterThan(0)
    fireEvent.change(screen.getByLabelText('Résultat'), { target: { value: 'error' } })
    await waitFor(() => expect(calls.some((c) => c.url === '/api/renders?limit=50&offset=0&status=error')).toBe(true))
  })
})

describe('AuditPage', () => {
  it('names the actions in the interface language', async () => {
    const journal: Journal = {
      total: 1,
      actions: ['login', 'login.failed'],
      items: [{ id: 1, createdAt: '2026-10-16T18:00:00Z', actor: '', action: 'login.failed', target: 'user:eve', details: 'mot de passe faux', ip: '10.0.0.1' }],
    }
    serve((url) => (url.startsWith('/api/audit') ? journal : undefined))
    show(<AuditPage />)
    expect(await screen.findByText('user:eve')).toBeInTheDocument()
    expect(screen.getAllByText('Connexion refusée').length).toBeGreaterThan(0)
    expect(screen.getByRole('option', { name: 'Connexion' })).toBeInTheDocument()
  })
})

describe('MonitoringPage', () => {
  it('shows the health, the workers and pauses the live view', async () => {
    const at = '2026-10-16T18:00:00Z'
    const state: Monitoring = {
      at,
      health: 'degraded',
      problems: ['1 échec(s) dans la dernière heure'],
      version: '0.2.0',
      instance: 'INT',
      uptimeSeconds: 3_900,
      database: { engine: 'sqlite', ok: true, replication: { enabled: true, locked: true } },
      workers: { ready: true, total: 2, busy: 1, queued: 0 },
      memory: { heapMb: 12, systemMb: 40, goroutines: 30 },
      traffic: { requestsPerMinute: 7, errorsPerMinute: 0, series: [{ t: at, requests: 7, errors: 0 }] },
      renders: { lastHour: 5, errorsLastHour: 1, refusedLastHour: 2, p50Ms: 150, p95Ms: 420, last24h: 40, series: [{ t: at, count: 5, errors: 1, p95Ms: 420 }] },
    }
    serve((url) => (url === '/api/monitoring' ? state : undefined))
    show(<MonitoringPage />)
    expect(await screen.findByText('Dégradé')).toBeInTheDocument()
    expect(screen.getByText('1 échec(s) dans la dernière heure')).toBeInTheDocument()
    expect(screen.getByText('1 / 2')).toBeInTheDocument()
    expect(screen.getByText('420 ms')).toBeInTheDocument()
    expect(screen.getByText('1 h 5 min')).toBeInTheDocument()
    expect(screen.getByText('Refusés (modèle ou données) : 2')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /Pause/ }))
    expect(screen.getByText('En pause')).toBeInTheDocument()
  })
})

describe('DashboardPage', () => {
  it('shows the figures of the last 30 days', async () => {
    const days = Array.from({ length: 30 }, (_, i) => ({ date: `2026-09-${String(i + 1).padStart(2, '0')}`, ok: i, refused: 0, errors: 0 }))
    const dash: Dashboard = {
      today: 12, last7d: 80, last30d: 435, successRate: 98.6, refused30d: 5, errors30d: 1, p50Ms: 140, p95Ms: 610, activeKeys: 3, users: 7,
      days, sources: [{ name: 'api', count: 400 }, { name: 'mcp', count: 41 }], topTemplates: [{ name: 'contrat.docx', count: 300 }],
      topActors: [{ name: 'billing-app', count: 390 }], recent: [],
    }
    serve((url) => (url === '/api/dashboard' ? dash : undefined))
    show(<DashboardPage />)
    expect(await screen.findByText('435')).toBeInTheDocument()
    expect(screen.getByText('98,6 %')).toBeInTheDocument()
    expect(screen.getByText('contrat.docx')).toBeInTheDocument()
    expect(screen.getByText('billing-app')).toBeInTheDocument()
    expect(screen.getByText('MCP')).toBeInTheDocument()
  })
})

describe('Prometheus', () => {
  it('creates the token, shows it once with the scrape configuration', async () => {
    const at = '2026-10-16T18:00:00Z'
    const state: Monitoring = {
      at, health: 'ok', version: '0.2.0', instance: 'INT', uptimeSeconds: 60,
      database: { engine: 'sqlite', ok: true, replication: { enabled: false, locked: false } },
      workers: { ready: true, total: 2, busy: 0, queued: 0 }, memory: { heapMb: 12, systemMb: 40, goroutines: 30 },
      traffic: { requestsPerMinute: 0, errorsPerMinute: 0, series: [] },
      renders: { lastHour: 0, errorsLastHour: 0, refusedLastHour: 0, p50Ms: 0, p95Ms: 0, last24h: 0, series: [] },
    }
    let configured = false
    const calls = serve((url, method) => {
      if (url === '/api/monitoring') return state
      if (url === '/api/metrics/token' && method === 'POST') {
        configured = true
        return { configured: true, fromEnv: false, createdAt: at, createdBy: 'admin', token: 'dim_xxxxxxxx' }
      }
      if (url === '/api/metrics/token') return { configured, fromEnv: false }
    })
    show(<MonitoringPage />)
    fireEvent.click(await screen.findByRole('button', { name: /Créer un jeton/ }))
    expect(await screen.findByTestId('metrics-token')).toHaveTextContent('dim_xxxxxxxx')
    expect(screen.getByText(/credentials: dim_xxxxxxxx/)).toBeInTheDocument()
    expect(calls.some((c) => c.url === '/api/metrics/token' && c.method === 'POST')).toBe(true)
  })
})
