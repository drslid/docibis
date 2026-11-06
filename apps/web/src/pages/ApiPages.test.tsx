import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { ApiKey, Info } from '@/api/generated/docibis'
import { ApiKeysPage } from './ApiKeysPage'
import { HelpPage } from './help/HelpPages'

const info: Info = { name: 'DocIbis', version: '0.1.0', instance: 'INT', database: { engine: 'sqlite', replication: { enabled: false, locked: false } } }

const key: ApiKey = {
  id: 1, name: 'billing-app INT', prefix: 'dib_abcdefgh', scopes: ['render'], createdBy: 'admin',
  createdAt: '2026-10-16T18:00:00Z', expiresAt: '2027-10-16T18:00:00Z', status: 'active',
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function show(page: React.ReactNode) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>{page}</MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ApiKeysPage', () => {
  it('creates a key and shows its secret once', async () => {
    const keys: ApiKey[] = []
    vi.stubGlobal('fetch', vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url, init) => {
      if (url === '/api/keys' && init?.method === 'POST') {
        keys.push(key)
        return json({ key, secret: 'dib_abcdefgh_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' }, 201)
      }
      return json(url === '/api/keys' ? keys : url === '/api/spaces' ? [] : info)
    }))
    show(<ApiKeysPage />)
    expect(await screen.findByText('Aucune clé')).toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('Application'), { target: { value: 'billing-app INT' } })
    fireEvent.click(screen.getByRole('button', { name: /Créer la clé/ }))
    expect(await screen.findByTestId('secret')).toHaveTextContent('dib_abcdefgh_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx')
    expect(await screen.findByText('dib_abcdefgh_…')).toBeInTheDocument()
    expect(screen.getByText('Révoquer')).toBeInTheDocument()
  })
})

describe('Help, API guide', () => {
  it('shows ready-to-run calls of this instance', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(info)))
    const { container } = render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={['/aide/api']}>
          <Routes>
            <Route path="/aide/:slug" element={<HelpPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(screen.getByRole('heading', { name: "Utiliser l'API" })).toBeInTheDocument()
    const blocks = [...container.querySelectorAll('pre code')].map((c) => c.textContent ?? '')
    expect(blocks.some((b) => b.includes(`${window.location.origin}/v1/render`) && b.includes('Authorization: Bearer $DOCIBIS_API_KEY'))).toBe(true)
    expect(blocks.some((b) => b.includes('requests.post('))).toBe(true)
    expect(screen.getByRole('link', { name: "référence de l'API" })).toHaveAttribute('href', '/docs')
  })
})
