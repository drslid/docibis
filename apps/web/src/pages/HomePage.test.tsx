import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { Info, Problem } from '@/api/generated/docibis'
import { HomePage } from './HomePage'

function respond(status: number, body: Info | Problem) {
  const type = status < 400 ? 'application/json' : 'application/problem+json'
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': type } })),
  )
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <HomePage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('HomePage', () => {
  it('shows the instance and its database', async () => {
    respond(200, {
      name: 'DocIbis',
      version: '0.1.0',
      instance: 'INT',
      database: { engine: 'sqlite', replication: { enabled: true, locked: true, target: 's3://docibis-backup/db' } },
    })
    renderPage()
    expect(await screen.findByText('SQLite')).toBeInTheDocument()
    expect(screen.getByText('s3://docibis-backup/db')).toBeInTheDocument()
    expect(screen.getAllByText('INT').length).toBeGreaterThan(0)
  })

  it('warns when the database has no copy', async () => {
    respond(200, {
      name: 'DocIbis',
      version: 'dev',
      instance: '',
      database: { engine: 'sqlite', replication: { enabled: false, locked: false } },
    })
    renderPage()
    expect(await screen.findByText(/les données sont perdues/)).toBeInTheDocument()
  })

  it('waits while the API starts', async () => {
    respond(503, { status: 503, title: 'Service Unavailable', detail: 'Le service démarre' })
    renderPage()
    expect(await screen.findByText(/Le service démarre/)).toBeInTheDocument()
  })
})
