import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { FontsOverview, SettingsPage } from '@/api/generated/docibis'
import { ConfigPage } from './ConfigPage'

const page: SettingsPage = {
  groups: [
    {
      id: 'general',
      label: 'Général',
      settings: [
        { key: 'instance_name', label: "Nom de l'instance", kind: 'text', value: 'INT', default: '', source: 'environment', env: 'INSTANCE_NAME', locked: true, active: true, restart: false },
        { key: 'render_workers', label: 'Workers LibreOffice', kind: 'number', value: '1', default: '1', min: 1, max: 8, source: 'default', locked: false, active: false, restart: false },
      ],
    },
  ],
  startup: [{ key: 'listen', label: "Adresse d'écoute", value: ':8080', source: 'default' }],
}

describe('ConfigPage', () => {
  it('shows locked settings read-only and saves only the changes', async () => {
    const fetch = vi.fn(async (url: string, init?: RequestInit) => {
      const body = url.includes('/api/branding')
        ? { companyLogo: 'custom', version: 'abcdef0123456789' }
        : url.includes('/api/info')
        ? { name: 'DocIbis', version: 'dev', instance: 'INT', database: { engine: 'sqlite', replication: { enabled: false, locked: false } } }
        : page
      return new Response(init?.method === 'HEAD' ? null : JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetch)
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={['/configuration']}>
          <Routes>
            <Route path="/configuration/:section?" element={<ConfigPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(await screen.findByText('Fixé par la variable INSTANCE_NAME')).toBeInTheDocument()
    expect(screen.getByLabelText("Nom de l'instance")).toBeDisabled()
    expect(screen.getByText('Bientôt')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Workers LibreOffice'), { target: { value: '3' } })
    fireEvent.click(screen.getByRole('button', { name: /Enregistrer \(1\)/ }))
    await screen.findByText('Réglages enregistrés.')
    const put = fetch.mock.calls.find(([, init]) => init?.method === 'PUT')
    expect(JSON.parse(String(put?.[1]?.body))).toEqual({ values: { render_workers: '3' } })

    // The logo, in its own section of the left menu.
    fireEvent.click(screen.getByRole('link', { name: 'Apparence' }))
    fireEvent.click(await screen.findByRole('button', { name: /Aucun logo/ }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Enregistrer' })[0])
    await screen.findByText('Enregistré.')
    const logo = fetch.mock.calls.find(([url, init]) => url === '/api/branding' && init?.method === 'PUT')
    expect(JSON.parse(String(logo?.[1]?.body))).toEqual({ companyLogo: 'none' })
  })

  it('adds fonts and shows those the templates lack', async () => {
    const fonts: FontsOverview = {
      added: [],
      installed: ['DejaVu Sans', 'Liberation Sans'],
      missing: [
        { family: 'Arial', substitute: 'Liberation Sans', templates: ['contrats/a.docx', 'contrats/b.docx'] },
        { family: 'Helvetica Neue LT Std', templates: ['contrats/a.docx'] },
      ],
    }
    const fetch = vi.fn(async (url: string, init?: RequestInit) => {
      let body: unknown = page
      if (url === '/api/fonts' && init?.method === 'POST') {
        const file = (init.body as FormData).get('file') as File
        body = { id: 1, file: file.name, family: 'Helvetica Neue LT Std', style: 'Roman', size: 2048, createdAt: '2026-11-06T10:00:00Z', createdBy: 'admin' }
        fonts.added = [body as FontsOverview['added'][number]]
        fonts.missing = fonts.missing.slice(0, 1)
        return new Response(JSON.stringify(body), { status: 201, headers: { 'Content-Type': 'application/json' } })
      }
      if (url.includes('/api/fonts')) body = fonts
      if (url.includes('/api/info')) body = { name: 'DocIbis', version: 'dev', instance: 'INT', database: { engine: 'sqlite', replication: { enabled: false, locked: false } } }
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
    })
    vi.stubGlobal('fetch', fetch)
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={['/configuration/polices']}>
          <Routes>
            <Route path="/configuration/:section?" element={<ConfigPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(await screen.findByText('Helvetica Neue LT Std')).toBeInTheDocument()
    expect(screen.getByText('remplacée par Liberation Sans, de mêmes dimensions')).toBeInTheDocument()
    expect(screen.getByText('Polices installées (2 familles)')).toBeInTheDocument()

    const file = new File(['font'], 'HelveticaNeueLTStd-Roman.otf', { type: 'font/otf' })
    fireEvent.change(screen.getByLabelText('Ajouter des polices'), { target: { files: [file] } })
    expect(await screen.findByText(/Ajoutées : Helvetica Neue LT Std Roman/)).toBeInTheDocument()
    expect(await screen.findByText('HelveticaNeueLTStd-Roman.otf')).toBeInTheDocument()
    expect(fetch.mock.calls.some(([url, init]) => url === '/api/fonts' && init?.method === 'POST')).toBe(true)
  })
})
