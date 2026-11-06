import type { TryRenderForm, TryRenderFormPdfRestrictEditing, TryRenderFormPdfRestrictPrinting } from '@/api/generated/docibis'
import { useI18n } from '@/i18n/useI18n'

/** The formats of a document, with their content type for the browser. */
export const FORMATS = [
  { value: 'pdf', label: 'PDF', mime: 'application/pdf' },
  { value: 'docx', label: 'Word (.docx)', mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' },
  { value: 'doc', label: 'Word 97 (.doc)', mime: 'application/msword' },
  { value: 'odt', label: 'OpenDocument (.odt)', mime: 'application/vnd.oasis.opendocument.text' },
  { value: 'rtf', label: 'RTF', mime: 'application/rtf' },
  { value: 'html', label: 'HTML', mime: 'text/html' },
  { value: 'txt', label: '', mime: 'text/plain' },
] as const

export interface PdfSettings {
  password: string
  ownerPassword: string
  printing: '' | '0' | '1'
  editing: '' | '0' | '1' | '2' | '3'
  noCopy: boolean
  skipEmpty: boolean
  title: string
  author: string
  subject: string
  keywords: string
}

export const NO_PDF_SETTINGS: PdfSettings = {
  password: '', ownerPassword: '', printing: '', editing: '', noCopy: false, skipEmpty: false, title: '', author: '', subject: '', keywords: '',
}

/** The fields of the form for these settings; restrictions need the owner password. */
export function pdfSettingsForm(s: PdfSettings): Partial<TryRenderForm> {
  return {
    ...(s.password && { passwordProtect: s.password }),
    ...(s.ownerPassword && {
      pdfRestrictPassword: s.ownerPassword,
      ...(s.printing && { pdfRestrictPrinting: Number(s.printing) as TryRenderFormPdfRestrictPrinting }),
      ...(s.editing && { pdfRestrictEditing: Number(s.editing) as TryRenderFormPdfRestrictEditing }),
      ...(s.noCopy && { pdfRestrictCopy: true }),
    }),
    ...(s.skipEmpty && { pdfSkipEmptyPages: true }),
    ...(s.title.trim() && { title: s.title.trim() }),
    ...(s.author.trim() && { author: s.author.trim() }),
    ...(s.subject.trim() && { subject: s.subject.trim() }),
    ...(s.keywords.trim() && { keywords: s.keywords.trim() }),
  }
}

export function PdfOptions({ value, onChange }: { value: PdfSettings; onChange: (v: PdfSettings) => void }) {
  const { t } = useI18n()
  const set = <K extends keyof PdfSettings>(key: K, v: PdfSettings[K]) => onChange({ ...value, [key]: v })
  const text = (key: 'title' | 'author' | 'subject' | 'keywords') => (
    <div key={key}>
      <label className="label" htmlFor={`try-${key}`}>{t(`try.meta.${key}`)}</label>
      <input id={`try-${key}`} className="input" value={value[key]} maxLength={500} onChange={(e) => set(key, e.target.value)} />
    </div>
  )
  return (
    <>
      <div>
        <label className="label" htmlFor="try-password">{t('try.password')}</label>
        <input id="try-password" type="password" autoComplete="new-password" className="input" value={value.password} maxLength={128} onChange={(e) => set('password', e.target.value)} />
      </div>
      <div>
        <label className="label" htmlFor="try-owner">{t('try.ownerPassword')}</label>
        <input id="try-owner" type="password" autoComplete="new-password" className="input" value={value.ownerPassword} maxLength={128} onChange={(e) => set('ownerPassword', e.target.value)} />
      </div>
      <fieldset className="sm:col-span-2 grid gap-3 sm:grid-cols-3 items-end" disabled={!value.ownerPassword}>
        <div>
          <label className="label" htmlFor="try-printing">{t('try.printing')}</label>
          <select id="try-printing" className="input" value={value.printing} onChange={(e) => set('printing', e.target.value as PdfSettings['printing'])}>
            <option value="">{t('try.allowed')}</option>
            <option value="1">{t('try.printLow')}</option>
            <option value="0">{t('try.forbidden')}</option>
          </select>
        </div>
        <div>
          <label className="label" htmlFor="try-editing">{t('try.editing')}</label>
          <select id="try-editing" className="input" value={value.editing} onChange={(e) => set('editing', e.target.value as PdfSettings['editing'])}>
            <option value="">{t('try.allowed')}</option>
            <option value="3">{t('try.editComments')}</option>
            <option value="2">{t('try.editForms')}</option>
            <option value="1">{t('try.editPages')}</option>
            <option value="0">{t('try.forbidden')}</option>
          </select>
        </div>
        <label className="flex items-center gap-2 text-sm pb-2"><input type="checkbox" checked={value.noCopy} onChange={(e) => set('noCopy', e.target.checked)} /> {t('try.noCopy')}</label>
      </fieldset>
      <p className="sm:col-span-2 text-xs text-gray-400">{t('try.restrictHint')}</p>
      <label className="flex items-center gap-2 text-sm sm:col-span-2"><input type="checkbox" checked={value.skipEmpty} onChange={(e) => set('skipEmpty', e.target.checked)} /> {t('try.skipEmpty')}</label>
      {(['title', 'author', 'subject', 'keywords'] as const).map(text)}
    </>
  )
}
