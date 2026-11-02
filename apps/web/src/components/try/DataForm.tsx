import { ImagePlus, Plus, Trash2 } from 'lucide-react'
import { useI18n } from '@/i18n/useI18n'

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json }

/** An empty value of the same shape, for a new row of a list. */
function blank(v: Json): Json {
  if (Array.isArray(v)) return []
  if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, blank(x)]))
  if (typeof v === 'boolean') return false
  if (typeof v === 'number') return 0
  if (typeof v === 'string') return ''
  return null
}

const ISO_DATE = /^\d{4}-\d{2}-\d{2}$/
const input = 'input py-1.5 text-sm'

function ImageField({ id, value, onChange }: { id: string; value: string; onChange: (v: string) => void }) {
  const { t } = useI18n()
  const read = (file?: File) => {
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => onChange(String(reader.result))
    reader.readAsDataURL(file)
  }
  return (
    <div className="flex items-center gap-3">
      {value.startsWith('data:image/') && <img src={value} alt="" className="h-10 max-w-24 object-contain border border-gray-200 dark:border-gray-700 rounded bg-white" />}
      <label className="btn-secondary py-1 text-sm inline-flex items-center gap-2 cursor-pointer">
        <ImagePlus className="w-4 h-4" /> {t('form.image')}
        <input id={id} type="file" accept="image/png,image/jpeg,image/gif,image/svg+xml" className="hidden" onChange={(e) => read(e.target.files?.[0])} />
      </label>
      {value && <button type="button" className="text-xs text-gray-500 hover:text-red-600" onClick={() => onChange('')}>{t('form.clear')}</button>}
    </div>
  )
}

function Field({ id, name, value, onChange, depth }: { id: string; name: string; value: Json; onChange: (v: Json) => void; depth: number }) {
  const { t } = useI18n()
  if (depth > 8) return <p className="text-xs text-gray-500">{t('form.tooDeep')}</p>
  if (Array.isArray(value)) {
    const sample = value[0] ?? ''
    return (
      <fieldset className="rounded-lg border border-gray-200 dark:border-gray-700 p-3">
        <legend className="px-1 text-sm font-medium font-mono">{name} <span className="font-sans text-xs text-gray-400">({t('form.rows', { n: value.length })})</span></legend>
        <div className="space-y-3">
          {value.map((item, i) => (
            <div key={i} className="flex gap-2 items-start">
              <span className="text-xs text-gray-400 w-6 pt-2 text-right tabular-nums">{i + 1}</span>
              <div className="flex-1 min-w-0">
                <Field id={`${id}-${i}`} name="" value={item} depth={depth + 1} onChange={(v) => onChange(value.map((x, j) => (j === i ? v : x)))} />
              </div>
              <button type="button" className="p-1.5 text-gray-400 hover:text-red-600" aria-label={t('form.removeRow', { n: i + 1 })} onClick={() => onChange(value.filter((_, j) => j !== i))}>
                <Trash2 className="w-4 h-4" />
              </button>
            </div>
          ))}
        </div>
        <button type="button" className="btn-ghost py-1 text-sm inline-flex items-center gap-1 mt-2" onClick={() => onChange([...value, blank(sample)])}>
          <Plus className="w-4 h-4" /> {t('form.addRow')}
        </button>
      </fieldset>
    )
  }
  if (value && typeof value === 'object') {
    const entries = Object.entries(value)
    const body = (
      <div className="grid gap-3 sm:grid-cols-2">
        {entries.map(([k, v]) => (
          <div key={k} className={v !== null && typeof v === 'object' ? 'sm:col-span-2' : ''}>
            <Field id={`${id}-${k}`} name={k} value={v} depth={depth + 1} onChange={(x) => onChange({ ...value, [k]: x })} />
          </div>
        ))}
      </div>
    )
    if (!name) return body
    return (
      <fieldset className="rounded-lg border border-gray-200 dark:border-gray-700 p-3">
        <legend className="px-1 text-sm font-medium font-mono">{name}</legend>
        {body}
      </fieldset>
    )
  }
  const control = (() => {
    if (typeof value === 'boolean') {
      return <input id={id} type="checkbox" className="h-4 w-4" checked={value} onChange={(e) => onChange(e.target.checked)} />
    }
    if (typeof value === 'number') {
      return <input id={id} type="number" step="any" className={input} value={value} onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))} />
    }
    const text = value ?? ''
    if (text.startsWith('data:image/') || /(^|[._])(logo|image|photo|signature|img)/i.test(name)) {
      return <ImageField id={id} value={text} onChange={onChange} />
    }
    if (ISO_DATE.test(text)) {
      return <input id={id} type="date" className={input} value={text} onChange={(e) => onChange(e.target.value)} />
    }
    if (text.length > 60 || text.includes('\n')) {
      return <textarea id={id} className={`${input} min-h-16`} value={text} onChange={(e) => onChange(e.target.value)} />
    }
    return <input id={id} className={input} value={text} onChange={(e) => onChange(e.target.value)} />
  })()
  if (!name) return control
  const inline = typeof value === 'boolean'
  return (
    <div className={inline ? 'flex items-center gap-2' : ''}>
      {inline && control}
      <label htmlFor={id} className={inline ? 'text-sm font-mono' : 'label font-mono'}>{name}</label>
      {!inline && control}
    </div>
  )
}

/** A form built from the data a template reads: texts, dates, checkboxes, list rows to add or remove, images. */
export function DataForm({ value, onChange }: { value: Record<string, Json>; onChange: (v: Record<string, Json>) => void }) {
  const { t } = useI18n()
  if (Object.keys(value).length === 0) return <p className="text-sm text-gray-500">{t('form.empty')}</p>
  return <Field id="form" name="" value={value} depth={0} onChange={(v) => onChange(v as Record<string, Json>)} />
}
