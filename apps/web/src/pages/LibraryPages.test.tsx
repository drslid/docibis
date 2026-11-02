import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { LibraryTemplate, Release, ReleasePage, Session, TemplateDetail, User } from '@/api/generated/docibis'
import { Sidebar } from '@/components/layout/Sidebar'
import { useReleaseSeen } from '@/hooks/useReleaseSeen'
import { LibraryPage, TemplatePage } from './LibraryPage'
import { ReleasesPage } from './ReleasesPage'

const admin: User = { id: 1, username: 'admin', firstName: 'Alice', lastName: 'Admin', displayName: 'Alice Admin', email: '', role: 'admin', active: true, language: 'fr', createdAt: '2026-10-01T08:00:00Z' }
const viewer: User = { ...admin, id: 2, username: 'victor', displayName: 'Victor', role: 'viewer' }

const fiche: LibraryTemplate = {
  id: 7, path: 'acme/contracts/fiche_conseil.docx', kind: 'template', description: 'Fiche conseil', published: 2, versions: 2, dataCheck: 'warn',
  engine: 'overlay', hasReference: true, check: { at: '2026-10-17T10:00:00Z', version: '1.4.0', ok: false, pages: 3, referencePages: 3, permille: [0.1, 8.4, 0], threshold: 3 },
  fast: { state: 'qualified', at: '2026-10-17T10:00:00Z', permille: 0.04, tests: 3, overlayMs: 0.4, standardMs: 420 },
  createdAt: '2026-10-16T10:00:00Z', createdBy: 'admin', updatedAt: '2026-10-17T10:00:00Z',
}
const cg: LibraryTemplate = { ...fiche, id: 8, path: 'acme/cg.pdf', kind: 'pdf', description: '', published: 1, versions: 1, engine: '', hasReference: false, check: undefined }
const detail: TemplateDetail = {
  template: fiche,
  fields: [{ path: 'client.nom', kind: 'text', optional: false }, { path: 'lignes[].montant', kind: 'text', optional: false }, { path: 'signature', kind: 'image', optional: true }],
  sample: { client: { nom: 'Exemple nom' } },
  versions: [
    { number: 2, size: 120_000, sha256: 'b', engine: 'overlay', note: 'texte revu', hasReference: true, check: fiche.check, createdAt: '2026-10-17T10:00:00Z', createdBy: 'eve' },
    { number: 1, size: 118_000, sha256: 'a', engine: 'overlay', note: 'import', hasReference: false, createdAt: '2026-10-16T10:00:00Z', createdBy: 'admin' },
  ],
}
const note = (id: number, version: string): Release => ({ id, version, title: `Version ${version}`, content: '- **Bibliothèque** de modèles', createdBy: 'admin', createdAt: '2026-10-17T10:00:00Z', updatedAt: '2026-10-17T10:00:00Z' })

type Call = { url: string; method: string; body?: BodyInit | null }

function serve(route: (url: string, method: string) => unknown, user: User = admin): Call[] {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body })
      if (url === '/api/auth/session') return Response.json({ authenticated: true, setupRequired: false, instance: 'INT', user } satisfies Session)
      if (url === '/api/info') return Response.json({ name: 'DocIbis', version: '1.4.0', instance: 'INT', database: { engine: 'sqlite', replication: { enabled: false, locked: false } } })
      const body = route(url, method)
      return body === undefined ? Response.json({ status: 404, title: 'Not Found' }, { status: 404 }) : Response.json(body)
    }),
  )
  return calls
}

function show(page: React.ReactNode, at = '/') {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[at]}>
        <Routes>
          <Route path="/" element={page} />
          <Route path="/modeles/:id" element={page} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
  useReleaseSeen.setState({ seen: 0 })
})

describe('LibraryPage', () => {
  it('lists the templates with their version, engine and reference', async () => {
    serve((url) => {
      if (url.startsWith('/api/templates/stats')) return [{ id: 7, count: 1234, errors: 2, refused: 0 }]
      return url.startsWith('/api/templates') ? [{ ...fiche, lint: { errors: 1, warnings: 0, infos: 0 } }, cg] : undefined
    })
    show(<LibraryPage />)
    expect(await screen.findByText('acme/contracts/fiche_conseil.docx')).toBeInTheDocument()
    expect(screen.getByText('PDF fixe')).toBeInTheDocument()
    expect(screen.getByText('v2 publiée')).toBeInTheDocument()
    expect(screen.getByText('Calque actif')).toBeInTheDocument()
    expect(screen.getByText('Écart')).toBeInTheDocument()
    expect(screen.getByText('1 erreur(s)')).toBeInTheDocument()
    expect(await screen.findByText((1234).toLocaleString())).toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('Non utilisés (30 j)'))
    expect(screen.getByText('Tous les modèles publiés ont servi ces 30 derniers jours.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Exporter/ })).toHaveAttribute('href', '/api/templates/export')
    expect(screen.getByRole('button', { name: /Tout comparer/ })).toBeInTheDocument()
  })

  it('lets a viewer read only', async () => {
    serve((url) => (url.startsWith('/api/templates/stats') ? [] : url.startsWith('/api/templates') ? [fiche] : undefined), viewer)
    show(<LibraryPage />)
    expect(await screen.findByText('acme/contracts/fiche_conseil.docx')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Ajouter un modèle/ })).not.toBeInTheDocument()
  })
})

describe('TemplatePage', () => {
  it('shows the data expected, the versions and goes back to an older one', async () => {
    const calls = serve((url, method) => {
      if (url === '/api/templates/7' && method === 'GET') return detail
      if (url === '/api/templates/7/publish') return { ...detail, template: { ...fiche, published: 1 } }
      return undefined
    })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    show(<TemplatePage />, '/modeles/7')
    expect(await screen.findByText('lignes[].montant')).toBeInTheDocument()
    expect(screen.getByText('texte revu')).toBeInTheDocument()
    expect(screen.getByText('3 pages, écart maximal 8.4 ‰ (toléré : 3 ‰)')).toBeInTheDocument()
    expect(screen.getByText(/-F templateName=acme\/contracts\/fiche_conseil.docx/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Revenir à celle-ci' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/templates/7/publish' && c.method === 'POST' && c.body === '{"version":1}')).toBe(true))
  })
})

describe('ReleasesPage', () => {
  it('shows the notes, the newest first, and marks them read', async () => {
    const page: ReleasePage = { items: [note(2, '1.4.0'), note(1, '1.3.0')], total: 2 }
    serve((url) => (url.startsWith('/api/releases') ? page : undefined))
    show(<ReleasesPage />)
    expect(await screen.findByText('Version 1.4.0')).toBeInTheDocument()
    expect(screen.getByText('Dernière')).toBeInTheDocument()
    expect(screen.getAllByText('Bibliothèque')[0].tagName).toBe('STRONG')
    await waitFor(() => expect(localStorage.getItem('docibis_last_seen_release')).toBe('2'))
    expect(screen.getByRole('button', { name: /Nouvelle note/ })).toBeInTheDocument()
  })

  it('shows a dot in the menu until the newest note is read', async () => {
    serve((url) => (url.startsWith('/api/releases') ? { items: [note(3, '1.5.0')], total: 1 } : undefined))
    show(<Sidebar mobileOpen={false} onMobileClose={() => {}} collapsed={false} onToggleCollapse={() => {}} />)
    expect(await screen.findByLabelText('Nouveautés non lues')).toBeInTheDocument()
    useReleaseSeen.getState().markSeen(3)
    await waitFor(() => expect(screen.queryByLabelText('Nouveautés non lues')).not.toBeInTheDocument())
  })
})
