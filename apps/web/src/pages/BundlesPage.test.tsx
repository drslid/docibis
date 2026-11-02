import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Bundle, LibraryTemplate, Session, User } from '@/api/generated/docibis'
import { BundlesPage } from './BundlesPage'

const editor: User = { id: 3, username: 'eve', firstName: 'Eve', lastName: 'Editrice', displayName: 'Eve Editrice', email: '', role: 'editor', active: true, language: 'fr', createdAt: '2026-10-01T08:00:00Z' }
const contrat: LibraryTemplate = {
  id: 7, path: 'souscription/contrat.docx', kind: 'template', description: '', published: 1, versions: 1, dataCheck: 'warn',
  createdAt: '2026-10-16T10:00:00Z', createdBy: 'admin', updatedAt: '2026-10-17T10:00:00Z',
}
const cg: LibraryTemplate = { ...contrat, id: 8, path: 'souscription/cg.pdf', kind: 'pdf' }
const bundle: Bundle = {
  id: 1, name: 'souscription-habitation', description: 'Contrat et CG', output: 'pdf',
  items: [{ template: 'souscription/contrat.docx', dataPath: 'contrat' }, { template: 'souscription/cg.pdf', condition: 'options.cg = true' }],
  createdAt: '2026-10-19T10:00:00Z', createdBy: 'eve', updatedAt: '2026-10-19T10:00:00Z', updatedBy: 'eve',
}

afterEach(() => vi.unstubAllGlobals())

describe('BundlesPage', () => {
  it('lists the bundles with how to call them, and creates one', async () => {
    const calls: { url: string; method: string; body?: string }[] = []
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      calls.push({ url, method, body: init?.body as string })
      if (url === '/api/auth/session') return Response.json({ authenticated: true, setupRequired: false, instance: 'INT', user: editor } satisfies Session)
      if (url === '/api/bundles' && method === 'GET') return Response.json([bundle])
      if (url === '/api/bundles' && method === 'POST') return Response.json({ ...bundle, id: 2, name: 'devis' }, { status: 201 })
      if (url.startsWith('/api/templates')) return Response.json([contrat, cg])
      return Response.json({ status: 404, title: 'Not Found' }, { status: 404 })
    }))
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter><BundlesPage /></MemoryRouter>
      </QueryClientProvider>,
    )
    expect(await screen.findByText('souscription-habitation')).toBeInTheDocument()
    expect(screen.getByText(/si options\.cg = true/)).toBeInTheDocument()
    expect(screen.getByText(/\/v1\/bundles\/souscription-habitation\/render/)).toBeInTheDocument()

    fireEvent.click(await screen.findByRole('button', { name: /Nouvelle liasse/ }))
    fireEvent.change(screen.getByLabelText('Nom appelé par les applications'), { target: { value: 'devis' } })
    await screen.findByRole('option', { name: 'souscription/contrat.docx' })
    fireEvent.change(screen.getByLabelText('Modèle du document 1'), { target: { value: 'souscription/contrat.docx' } })
    fireEvent.change(screen.getByLabelText('Condition du document 1'), { target: { value: ' remise = true ' } })
    fireEvent.click(screen.getByRole('button', { name: 'Enregistrer' }))
    await waitFor(() => expect(calls.some((c) => c.url === '/api/bundles' && c.method === 'POST')).toBe(true))
    const sent = JSON.parse(calls.find((c) => c.method === 'POST')?.body ?? '{}')
    expect(sent).toEqual({ name: 'devis', description: '', output: 'pdf', items: [{ template: 'souscription/contrat.docx', condition: 'remise = true' }] })
  })
})
