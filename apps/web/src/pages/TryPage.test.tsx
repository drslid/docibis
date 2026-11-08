import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { Info, TryResult, TryTemplate } from '@/api/generated/docibis'
import { TryPage } from './TryPage'

const info: Info = { name: 'DocIbis', version: '0.1.0', instance: 'INT', database: { engine: 'sqlite', replication: { enabled: false, locked: false } } }

const template: TryTemplate = {
  name: 'mandat.docx',
  size: 58_000,
  prepareMs: 120,
  analysis: {
    fields: 2, flowFields: 0, checkboxes: 1, sections: 0, blockSections: 0, repeats: 0, images: 0, shapes: 0, tables: 1,
    engine: 'overlay', reason: 'mise en page fixe : 2 champs en bout de ligne, 1 case, 0 image',
  },
  sample: { client: { nom: 'Exemple nom' }, estClient: true },
}

const result: TryResult = { pdf: btoa('%PDF-1.7 test'), format: 'pdf', size: 13, engine: 'standard', prepareMs: 0, mergeMs: 3, renderMs: 151, totalMs: 160 }

function stubApi() {
  const answers: Record<string, unknown> = { '/api/info': info, '/api/try/analyze': template, '/api/try/render': result }
  const fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url) =>
    new Response(JSON.stringify(answers[url]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
  vi.stubGlobal('fetch', fetch)
  return fetch
}

async function openTemplate(name = 'mandat.docx') {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter>
        <TryPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  fireEvent.change(screen.getByLabelText('Modèle Word, Excel, PowerPoint ou formulaire PDF'), { target: { files: [new File(['PK'], name)] } })
  expect(await screen.findByText('Calque possible')).toBeInTheDocument()
}

describe('TryPage', () => {
  it('reads a template, fills the example data and shows the PDF', async () => {
    const fetch = stubApi()
    URL.createObjectURL = vi.fn(() => 'blob:pdf')
    URL.revokeObjectURL = vi.fn()
    await openTemplate()
    expect(screen.getByLabelText('nom')).toHaveValue('Exemple nom')
    expect(screen.getByLabelText('estClient')).toBeChecked()
    fireEvent.change(screen.getByLabelText('nom'), { target: { value: 'Durand' } })
    fireEvent.click(screen.getByRole('tab', { name: 'JSON' }))
    expect((screen.getByLabelText('Données JSON') as HTMLTextAreaElement).value).toContain('"nom": "Durand"')

    fireEvent.click(screen.getByRole('button', { name: 'Produire le PDF' }))
    expect(await screen.findByText('PDF produit en 160 ms')).toBeInTheDocument()
    expect(screen.getByTitle('Aperçu du PDF')).toHaveAttribute('src', 'blob:pdf')
    const sent = fetch.mock.calls.find(([url]) => url === '/api/try/render')?.[1]
    expect((sent?.body as FormData).get('data')).toContain('Durand')
  })

  it('refuses data that is not a JSON object', async () => {
    stubApi()
    await openTemplate()
    fireEvent.click(screen.getByRole('tab', { name: 'JSON' }))
    fireEvent.change(screen.getByLabelText('Données JSON'), { target: { value: '[1, 2]' } })
    expect(screen.getByText('Les données doivent être un objet JSON { … }.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Produire le PDF' })).toBeDisabled()
  })

  it('produces a template of the library with a watermark', async () => {
    const fiche = { id: 7, path: 'acme/fiche.docx', kind: 'template', published: 2, versions: 2 }
    const answers: Record<string, unknown> = {
      '/api/info': info, '/api/try/render': result, '/api/templates': [fiche, { ...fiche, id: 8, path: 'acme/cg.pdf', kind: 'pdf' }],
      '/api/templates/7': { template: fiche, fields: [], versions: [], sample: { client: { nom: 'Bibliothèque' } } },
    }
    const fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url) => Response.json(answers[url]))
    vi.stubGlobal('fetch', fetch)
    URL.createObjectURL = vi.fn(() => 'blob:pdf')
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <TryPage />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    const pick = await screen.findByLabelText('Ou un modèle de la bibliothèque')
    expect(screen.queryByRole('option', { name: 'acme/cg.pdf' })).not.toBeInTheDocument()
    fireEvent.change(pick, { target: { value: 'acme/fiche.docx' } })
    fireEvent.click(screen.getByRole('tab', { name: 'JSON' }))
    await waitFor(() => expect((screen.getByLabelText('Données JSON') as HTMLTextAreaElement).value).toContain('Bibliothèque'))
    fireEvent.change(screen.getByLabelText('Filigrane'), { target: { value: 'SPÉCIMEN' } })
    fireEvent.click(screen.getByRole('button', { name: 'Produire le PDF' }))
    expect(await screen.findByText('PDF produit en 160 ms')).toBeInTheDocument()
    const form = fetch.mock.calls.find(([url]) => url === '/api/try/render')?.[1]?.body as FormData
    expect([form.get('templateName'), form.get('watermark'), form.get('template'), form.get('pdfa')]).toEqual(['acme/fiche.docx', 'SPÉCIMEN', null, null])
  })

  it('sends the PDF options, and another format without them', async () => {
    const fetch = stubApi()
    URL.createObjectURL = vi.fn(() => 'blob:doc')
    await openTemplate()
    fireEvent.change(screen.getByLabelText("Mot de passe d'ouverture"), { target: { value: 'secret-1' } })
    fireEvent.change(screen.getByLabelText('Mot de passe du propriétaire (restrictions)'), { target: { value: 'owner-1' } })
    fireEvent.change(screen.getByLabelText('Impression'), { target: { value: '0' } })
    fireEvent.change(screen.getByLabelText('Titre'), { target: { value: 'Devis 42' } })
    fireEvent.click(screen.getByRole('button', { name: 'Produire le PDF' }))
    await screen.findByText('PDF produit en 160 ms')
    const pdfForm = fetch.mock.calls.filter(([url]) => url === '/api/try/render').at(-1)?.[1]?.body as FormData
    expect([pdfForm.get('passwordProtect'), pdfForm.get('pdfRestrictPassword'), pdfForm.get('pdfRestrictPrinting'), pdfForm.get('title'), pdfForm.get('format')])
      .toEqual(['secret-1', 'owner-1', '0', 'Devis 42', null])

    fetch.mockImplementation(async (url) => Response.json(url === '/api/try/render' ? { ...result, format: 'odt' } : url === '/api/info' ? info : template))
    fireEvent.change(screen.getByLabelText('Format du document'), { target: { value: 'odt' } })
    expect(screen.getByLabelText("Mot de passe d'ouverture")).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: 'Produire le document' }))
    expect(await screen.findByText('Document produit en 160 ms')).toBeInTheDocument()
    expect(screen.getByText("Pas d'aperçu pour ce format : téléchargez le document.")).toBeInTheDocument()
    const odtForm = fetch.mock.calls.filter(([url]) => url === '/api/try/render').at(-1)?.[1]?.body as FormData
    expect([odtForm.get('format'), odtForm.get('passwordProtect')]).toEqual(['odt', null])
  })

  it('offers the formats of the kind of template, and PDF again when the kind changes', async () => {
    stubApi()
    await openTemplate()
    fireEvent.change(screen.getByLabelText('Format du document'), { target: { value: 'odt' } })
    fireEvent.change(screen.getByLabelText('Modèle Word, Excel, PowerPoint ou formulaire PDF'), { target: { files: [new File(['PK'], 'facture.xlsx')] } })
    const select = screen.getByLabelText('Format du document') as HTMLSelectElement
    expect([...select.options].map((o) => o.value)).toEqual(['pdf', 'xlsx', 'ods'])
    expect(select.value).toBe('pdf')
  })

  it('fills a PDF form: its fields, PDF only, the passwords as only options', async () => {
    const form: TryTemplate = {
      name: 'cerfa_13753.pdf', size: 520_000, prepareMs: 0, sample: { txtNoms: 'Exemple txtNoms', numCP: '12345' },
      analysis: { fields: 2, flowFields: 0, checkboxes: 0, sections: 0, blockSections: 0, repeats: 0, images: 0, shapes: 0, tables: 0, engine: 'form', reason: 'formulaire PDF : DocIbis remplit ses champs lui-même, sans LibreOffice' },
    }
    const cerfa = { id: 9, path: 'cerfa/13753.pdf', kind: 'pdf', published: 1, versions: 1, engine: 'form' }
    const answers: Record<string, unknown> = {
      '/api/info': info, '/api/try/analyze': form, '/api/try/render': { ...result, engine: 'form' },
      '/api/templates': [cerfa, { ...cerfa, id: 8, path: 'acme/cg.pdf', engine: '' }],
    }
    vi.stubGlobal('fetch', vi.fn(async (url: string) => Response.json(answers[url])))
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter>
          <TryPage />
        </MemoryRouter>
      </QueryClientProvider>,
    )
    expect(await screen.findByRole('option', { name: 'cerfa/13753.pdf' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'acme/cg.pdf' })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Modèle Word, Excel, PowerPoint ou formulaire PDF'), { target: { files: [new File(['%PDF-'], 'cerfa_13753.pdf')] } })
    expect(await screen.findByText('Formulaire PDF')).toBeInTheDocument()
    expect(screen.getByText('2 champ(s) · 0 case(s)')).toBeInTheDocument()
    expect(screen.getByLabelText('txtNoms')).toHaveValue('Exemple txtNoms')
    expect([...(screen.getByLabelText('Format du document') as HTMLSelectElement).options].map((o) => o.value)).toEqual(['pdf'])
    expect(screen.getByText("Un formulaire PDF est rempli tel quel : seuls les mots de passe et les restrictions s'appliquent.")).toBeInTheDocument()
    expect(screen.queryByLabelText('Filigrane')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Titre')).not.toBeInTheDocument()
    expect(screen.getByLabelText("Mot de passe d'ouverture")).toBeEnabled()
  })
})
