import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TryTemplate } from '@/api/generated/docibis'
import { addinId, wordManifest } from './manifest'
import { issueText, WordPane } from './WordPane'

const template: TryTemplate = {
  name: 'modele.docx',
  size: 1200,
  prepareMs: 90,
  analysis: { fields: 1, flowFields: 0, checkboxes: 0, sections: 1, blockSections: 0, repeats: 0, images: 0, shapes: 0, tables: 0, engine: 'standard', reason: 'le texte coule' },
  sample: { client: { nom: 'Exemple nom' } },
  lint: [{ level: 'error', code: 'unclosed', message: '« <<cs_vip>> » n’a pas de <<es_>> qui la ferme.' }],
  fields: [{ path: 'client.nom', kind: 'text', optional: false }],
}

/** Word as Office.js shows it: one document of two slices, a selection, a search. */
function stubWord() {
  const inserted: string[] = []
  const searched: string[] = []
  const select = vi.fn()
  vi.stubGlobal('Office', {
    onReady: () => Promise.resolve({ host: 'Word' }),
    context: {
      displayLanguage: 'fr-FR',
      document: {
        getFileAsync: (_type: string, _options: unknown, done: (r: unknown) => void) =>
          done({
            status: 'succeeded',
            value: {
              sliceCount: 2,
              getSliceAsync: (i: number, cb: (r: unknown) => void) => cb({ status: 'succeeded', value: { data: i === 0 ? [80, 75] : [3, 4] } }),
              closeAsync: vi.fn(),
            },
          }),
      },
    },
  })
  vi.stubGlobal('Word', {
    run: async (batch: (context: unknown) => Promise<unknown>) =>
      batch({
        document: {
          getSelection: () => ({ insertText: (text: string) => inserted.push(text) }),
          body: {
            search: (text: string) => {
              searched.push(text)
              return { items: [{ select }], load: () => {} }
            },
          },
        },
        sync: () => Promise.resolve(),
      }),
  })
  return { inserted, searched, select }
}

describe('WordPane', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => localStorage.clear())

  it('tells to open it from Word', async () => {
    render(<WordPane />)
    expect(await screen.findByText(/Ce volet s’ouvre dans Word/)).toBeInTheDocument()
  })

  it('keeps the personal token, inserts tags, checks the document and shows the PDF', async () => {
    const word = stubWord()
    const fetch = vi.fn<(url: string, init?: RequestInit) => Promise<Response>>(async (url) =>
      url === '/v1/templates/analyze' ? Response.json(template) : new Response(new Blob(['%PDF-1.7']), { status: 200 }))
    vi.stubGlobal('fetch', fetch)
    URL.createObjectURL = vi.fn(() => 'blob:pdf')
    URL.revokeObjectURL = vi.fn()
    render(<WordPane />)

    fireEvent.change(await screen.findByLabelText('Jeton personnel'), { target: { value: 'dit_secret' } })
    fireEvent.click(screen.getByRole('button', { name: 'Enregistrer' }))
    expect(localStorage.getItem('docibis_word_token')).toBe('dit_secret')

    fireEvent.change(screen.getByLabelText('Champ des données'), { target: { value: 'client.nom' } })
    fireEvent.submit(screen.getByLabelText('Champ des données').closest('form')!)
    await waitFor(() => expect(word.inserted).toEqual(['<<client.nom>>']))

    fireEvent.click(screen.getByRole('tab', { name: 'Vérifier' }))
    fireEvent.click(screen.getByRole('button', { name: 'Vérifier le modèle' }))
    expect(await screen.findByText('le texte coule.')).toBeInTheDocument()
    const [url, init] = fetch.mock.calls[0]
    const sent = (init?.body as FormData).get('template') as File
    expect([url, (init?.headers as Record<string, string>).Authorization, sent.name, sent.size]).toEqual(['/v1/templates/analyze', 'Bearer dit_secret', 'modele.docx', 4])
    fireEvent.click(screen.getByRole('button', { name: 'Trouver' }))
    await waitFor(() => expect(word.select).toHaveBeenCalled())
    expect(word.searched).toEqual(['<<cs_vip>>'])

    fireEvent.click(screen.getByRole('tab', { name: 'Aperçu' }))
    expect((screen.getByLabelText('Données (JSON)') as HTMLTextAreaElement).value).toContain('"nom": "Exemple nom"')
    fireEvent.click(screen.getByRole('button', { name: 'Produire le PDF' }))
    expect(await screen.findByTitle('Aperçu')).toHaveAttribute('src', 'blob:pdf')
    const render_ = fetch.mock.calls[1]
    expect([render_[0], (render_[1]?.body as FormData).get('devMode')]).toEqual(['/v1/render', 'true'])
  })

  it('asks for another token when the API refuses it', async () => {
    stubWord()
    localStorage.setItem('docibis_word_token', 'dit_old')
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ detail: 'inconnu' }, { status: 401 })))
    render(<WordPane />)
    fireEvent.click(await screen.findByRole('tab', { name: 'Vérifier' }))
    fireEvent.click(screen.getByRole('button', { name: 'Vérifier le modèle' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Jeton refusé')
  })
})

describe('issueText', () => {
  it('finds the tag an issue is about', () => {
    expect(issueText('« <<cs_vip>> » sans fin')).toBe('<<cs_vip>>')
    expect(issueText('La police « Calibri » manque.')).toBe('Calibri')
    expect(issueText('Révisions Word oubliées.')).toBe('')
  })
})

describe('wordManifest', () => {
  it('points Word at the pane and icons of the instance', async () => {
    const id = await addinId('https://docibis.example')
    expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-5[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    expect(await addinId('https://docibis.example')).toBe(id)
    expect(await addinId('https://rec.example')).not.toBe(id)
    const xml = new DOMParser().parseFromString(wordManifest('https://docibis.example', 'DocIbis <INT> & co', id), 'application/xml')
    expect(xml.querySelector('parsererror')).toBeNull()
    expect(xml.querySelector('SourceLocation')?.getAttribute('DefaultValue')).toBe('https://docibis.example/word/')
    expect(xml.querySelector('DisplayName')?.getAttribute('DefaultValue')).toBe('DocIbis <INT> & co')
    expect(xml.querySelector('Id')?.textContent).toBe(id)
  })
})
