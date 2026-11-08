// The few parts of Office.js the Word add-in uses. Office.js itself comes from
// Microsoft (word/index.html): outside Word, window.Office stays undefined.

interface AsyncResult<T> {
  status: 'succeeded' | 'failed'
  value: T
  error?: { message: string }
}

interface OfficeFile {
  sliceCount: number
  getSliceAsync(index: number, callback: (result: AsyncResult<{ data: number[] }>) => void): void
  closeAsync(callback?: () => void): void
}

interface WordRange {
  insertText(text: string, location: 'Replace'): WordRange
  select(): void
}

interface WordContext {
  document: {
    getSelection(): WordRange
    body: { search(text: string, options: { matchCase: boolean }): { items: WordRange[]; load(properties: string): void } }
  }
  sync(): Promise<void>
}

declare global {
  interface Window {
    Office?: {
      onReady(): Promise<{ host: string | null }>
      context: {
        displayLanguage: string
        document: {
          getFileAsync(type: 'compressed', options: { sliceSize: number }, callback: (result: AsyncResult<OfficeFile>) => void): void
        }
      }
    }
    Word?: { run<T>(batch: (context: WordContext) => Promise<T>): Promise<T> }
  }
}

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'

/** Whether the page runs in Word, once Office.js is ready. */
export async function inWord(): Promise<boolean> {
  if (!window.Office) return false
  const { host } = await window.Office.onReady()
  return host === 'Word'
}

/** The language Word shows its menus in: "fr-FR". */
export function wordLanguage(): string {
  return window.Office?.context?.displayLanguage ?? ''
}

/** The document open in Word, as a .docx file, read in slices of 4 MB. */
export function currentDocument(name = 'modele.docx'): Promise<File> {
  return new Promise((resolve, reject) => {
    window.Office!.context.document.getFileAsync('compressed', { sliceSize: 4 << 20 }, (opened) => {
      if (opened.status !== 'succeeded') {
        reject(new Error(opened.error?.message ?? 'getFileAsync'))
        return
      }
      const file = opened.value
      const parts: Uint8Array<ArrayBuffer>[] = []
      const read = (i: number) => {
        if (i === file.sliceCount) {
          file.closeAsync()
          resolve(new File(parts, name, { type: DOCX }))
          return
        }
        file.getSliceAsync(i, (slice) => {
          if (slice.status !== 'succeeded') {
            file.closeAsync()
            reject(new Error(slice.error?.message ?? 'getSliceAsync'))
            return
          }
          parts.push(new Uint8Array(slice.value.data))
          read(i + 1)
        })
      }
      read(0)
    })
  })
}

/** Writes text in place of the selection. */
export function insertAtSelection(text: string): Promise<void> {
  return window.Word!.run(async (context) => {
    context.document.getSelection().insertText(text, 'Replace')
    await context.sync()
  })
}

/** Selects the first place of the document that holds text; false when there is none. */
export function findInDocument(text: string): Promise<boolean> {
  return window.Word!.run(async (context) => {
    // Word searches 255 characters at most.
    const found = context.document.body.search(text.slice(0, 255), { matchCase: true })
    found.load('items')
    await context.sync()
    if (found.items.length === 0) return false
    found.items[0].select()
    await context.sync()
    return true
  })
}
