import { useMemo, useState } from 'react'
import { Navigate, NavLink, useParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import {
  Cpu,
  DatabaseBackup,
  FileText,
  History,
  ImagePlus,
  Loader2,
  Lock,
  Palette,
  Rocket,
  Plus,
  Save,
  ScrollText,
  Send,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  Type,
  Undo2,
  Webhook,
  Zap,
  type LucideIcon,
} from 'lucide-react'
import {
  getGetBrandingQueryKey,
  getGetSettingsQueryKey,
  getListEnvironmentsQueryKey,
  useGetBranding,
  useGetSettings,
  useGetSigningKey,
  useListEnvironments,
  useSaveEnvironments,
  useUpdateBranding,
  useUpdateSettings,
  type BrandingCompanyLogo,
  type Environment,
  type Setting,
} from '@/api/generated/docibis'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { CopyButton } from '@/components/common/CopyButton'
import { SettingsCard } from '@/components/common/SettingsCard'
import { FontsSection } from '@/components/config/FontsSection'
import { BrandLogo, companyLogoURL } from '@/components/layout/BrandLogo'
import { PageTitle } from '@/components/layout/PageTitle'
import { useI18n, type Key } from '@/i18n/useI18n'

const SOURCES: Record<string, Key> = {
  default: 'config.source.default',
  console: 'config.source.console',
  environment: 'config.source.environment',
  file: 'config.source.file',
}

// Icon and colour of each group of settings, in the left menu.
const GROUPS: Record<string, { icon: LucideIcon; colour: string }> = {
  general: { icon: SlidersHorizontal, colour: 'text-sky-500' },
  rendering: { icon: Cpu, colour: 'text-indigo-500' },
  documents: { icon: FileText, colour: 'text-emerald-500' },
  fast: { icon: Zap, colour: 'text-amber-500' },
  native: { icon: Rocket, colour: 'text-orange-500' },
  async: { icon: Webhook, colour: 'text-fuchsia-500' },
  storage: { icon: DatabaseBackup, colour: 'text-teal-500' },
  history: { icon: History, colour: 'text-violet-500' },
  security: { icon: ShieldCheck, colour: 'text-rose-500' },
  logs: { icon: ScrollText, colour: 'text-slate-500' },
}

function Input({ setting, value, onChange }: { setting: Setting; value: string; onChange: (v: string) => void }) {
  const disabled = setting.locked
  switch (setting.kind) {
    case 'toggle':
      return (
        <button
          type="button"
          role="switch"
          aria-checked={value === 'true'}
          aria-label={setting.label}
          disabled={disabled}
          onClick={() => onChange(value === 'true' ? 'false' : 'true')}
          className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 ${value === 'true' ? 'bg-brand-600' : 'bg-gray-300 dark:bg-gray-600'}`}
        >
          <span className={`inline-block h-5 w-5 rounded-full bg-white shadow transition-transform ${value === 'true' ? 'translate-x-5' : 'translate-x-0.5'}`} />
        </button>
      )
    case 'choice':
      return (
        <select className="input max-w-xs" value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} aria-label={setting.label}>
          {setting.options?.map((o) => <option key={o} value={o}>{o}</option>)}
        </select>
      )
    case 'list':
      return <textarea className="input min-h-24 font-mono text-sm" value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} aria-label={setting.label} />
    case 'number':
      return <input className="input max-w-40" type="number" min={setting.min} max={setting.max} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} aria-label={setting.label} />
    default:
      return <input className="input" value={value} disabled={disabled} placeholder={setting.default} onChange={(e) => onChange(e.target.value)} aria-label={setting.label} />
  }
}

const LOGOS: BrandingCompanyLogo[] = ['none', 'custom']
const MAX_LOGO = 512 * 1024

/** The company logo under DocIbis. */
function BrandingSection() {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const { data: saved } = useGetBranding()
  const [mode, setMode] = useState<BrandingCompanyLogo | null>(null)
  const [image, setImage] = useState('')
  const [notice, setNotice] = useState('')
  const update = useUpdateBranding({
    mutation: {
      onSuccess: (b) => {
        queryClient.setQueryData(getGetBrandingQueryKey(), b)
        setMode(null)
        setImage('')
        setNotice(t('common.saved'))
      },
    },
  })

  if (!saved) return <Loader2 className="w-5 h-5 animate-spin text-gray-400" />
  const current = mode ?? saved.companyLogo
  const preview = current === 'none' ? null : image || (saved.companyLogo === 'custom' ? companyLogoURL(saved) : null)

  const pick = (file?: File) => {
    setNotice('')
    if (!file) return
    if (file.size > MAX_LOGO) {
      setNotice(t('branding.tooLarge'))
      return
    }
    const reader = new FileReader()
    reader.onload = () => setImage(String(reader.result))
    reader.readAsDataURL(file)
  }

  return (
    <SettingsCard title={t('config.branding')}>
      <p className="mb-4">{t('branding.help')}</p>
      <div className="grid gap-3 sm:grid-cols-2 mb-6">
        {LOGOS.map((o) => (
          <button
            key={o}
            type="button"
            aria-pressed={current === o}
            onClick={() => { setMode(o); setNotice('') }}
            className={`text-left rounded-xl border p-3.5 transition-colors ${
              current === o
                ? 'border-brand-500 bg-brand-50 ring-1 ring-brand-500 dark:bg-brand-900/20'
                : 'border-gray-200 hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600'
            }`}
          >
            <div className="font-semibold text-gray-900 dark:text-gray-100">{t(`branding.${o}`)}</div>
            <div className="mt-1 text-xs text-gray-500 dark:text-gray-400">{t(`branding.${o}Help`)}</div>
          </button>
        ))}
      </div>

      {current === 'custom' && (
        <div className="mb-6">
          <label className="btn-secondary inline-flex items-center gap-2 text-sm cursor-pointer">
            <ImagePlus className="w-4 h-4" /> {t('branding.choose')}
            <input type="file" accept="image/png,image/jpeg,image/webp,image/svg+xml" className="hidden" onChange={(e) => pick(e.target.files?.[0])} />
          </label>
          <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">{t('branding.fileHelp')}</p>
        </div>
      )}

      <div className="label">{t('branding.preview')}</div>
      <div className="grid gap-4 sm:grid-cols-2 max-w-xl mb-6">
        <div className="rounded-xl border border-gray-200 bg-white p-4">
          <BrandLogo light company={preview} className="w-52 mx-auto" />
          <div className="mt-3 text-center text-xs text-gray-400">{t('branding.previewLight')}</div>
        </div>
        <div className="dark">
          <div className="rounded-xl border border-gray-700 bg-gray-900 p-4">
            <BrandLogo company={preview} className="w-52 mx-auto" />
            <div className="mt-3 text-center text-xs text-gray-400">{t('branding.previewDark')}</div>
          </div>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <button
          type="button"
          className="btn-primary inline-flex items-center gap-2 text-sm"
          disabled={update.isPending || (current === 'custom' && !preview) || (mode === null && !image)}
          onClick={() => update.mutate({ data: { companyLogo: current, ...(current === 'custom' && image ? { image } : {}) } })}
        >
          {update.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />} {t('common.save')}
        </button>
        {update.error && <span className="text-sm text-red-700" role="alert">{update.error.message}</span>}
        {notice && !update.error && <span className="text-sm text-gray-600 dark:text-gray-300" role="status">{notice}</span>}
      </div>
    </SettingsCard>
  )
}

/** The other DocIbis that templates are sent to (INT, then REC, then PROD), with their keys. */
function EnvironmentsSection() {
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const { data: saved } = useListEnvironments()
  const { data: signing } = useGetSigningKey()
  const [draft, setDraft] = useState<Environment[] | null>(null)
  const [notice, setNotice] = useState('')
  const save = useSaveEnvironments({
    mutation: {
      onSuccess: (envs) => {
        queryClient.setQueryData(getListEnvironmentsQueryKey(), envs)
        setDraft(null)
        setNotice(t('common.saved'))
      },
    },
  })
  if (!saved) return <Loader2 className="w-5 h-5 animate-spin text-gray-400" />
  const rows = draft ?? saved
  const set = (i: number, e: Environment) => { setNotice(''); setDraft(rows.map((x, j) => (j === i ? e : x))) }
  const line = signing ? `${signing.instance} ${signing.publicKey}` : ''

  return (
    <SettingsCard title={t('envs.title')}>
      <p className="mb-4">{t('envs.help')}</p>
      <div className="space-y-2">
        {rows.map((e, i) => (
          <div key={i} className="grid gap-2 md:grid-cols-[8rem_minmax(0,1fr)_minmax(0,1fr)_auto] md:items-center">
            <input className="input py-1.5" aria-label={t('envs.name')} placeholder="REC" maxLength={20} value={e.name} onChange={(x) => set(i, { ...e, name: x.target.value })} />
            <input className="input py-1.5 font-mono text-sm" aria-label={t('envs.url')} placeholder="https://docibis-rec.exemple.fr" value={e.url} onChange={(x) => set(i, { ...e, url: x.target.value })} />
            <input className="input py-1.5 font-mono text-sm" aria-label={t('envs.key')} placeholder="dib_…" autoComplete="off" value={e.key ?? ''} onChange={(x) => set(i, { ...e, key: x.target.value })} />
            <button type="button" className="p-1.5 text-gray-400 hover:text-red-600" aria-label={t('common.delete')} onClick={() => setDraft(rows.filter((_, j) => j !== i))}><Trash2 className="w-4 h-4" /></button>
          </div>
        ))}
      </div>
      {rows.length === 0 && <p className="text-gray-500">{t('envs.none')}</p>}
      <div className="flex flex-wrap items-center gap-3 mt-4">
        <button type="button" className="btn-ghost inline-flex items-center gap-1 text-sm" disabled={rows.length >= 10} onClick={() => setDraft([...rows, { name: '', url: '', key: '' }])}>
          <Plus className="w-4 h-4" /> {t('envs.add')}
        </button>
        <button type="button" className="btn-primary inline-flex items-center gap-2 text-sm ml-auto" disabled={!draft || save.isPending} onClick={() => draft && save.mutate({ data: draft })}>
          {save.isPending ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />} {t('common.save')}
        </button>
      </div>
      {save.error && <p className="text-sm text-red-700 mt-2" role="alert">{save.error.problem?.detail ?? save.error.message}</p>}
      {notice && !save.error && <p className="text-sm text-gray-600 dark:text-gray-300 mt-2" role="status">{notice}</p>}
      {line && (
        <div className="mt-6 pt-4 border-t border-gray-100 dark:border-gray-700">
          <div className="label">{t('envs.signingKey')}</div>
          <div className="flex items-center gap-2">
            <code className="flex-1 min-w-0 text-xs break-all rounded-md bg-gray-50 dark:bg-gray-900 px-2 py-1.5">{line}</code>
            <CopyButton text={line} className="shrink-0 py-1 px-2 text-xs" />
          </div>
          <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">{t('envs.signingKeyHelp')}</p>
        </div>
      )}
    </SettingsCard>
  )
}

/** Settings in sections, with the menu on the left. */
export function ConfigPage() {
  const { section } = useParams()
  const { t } = useI18n()
  const queryClient = useQueryClient()
  const { data, isPending, error } = useGetSettings()
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [saved, setSaved] = useState(false)
  const update = useUpdateSettings({
    mutation: {
      onSuccess: (page) => {
        queryClient.setQueryData(getGetSettingsQueryKey(), page)
        setDraft({})
        setSaved(true)
      },
    },
  })

  const current = useMemo(() => {
    const out: Record<string, string> = {}
    data?.groups.forEach((g) => g.settings.forEach((s) => { out[s.key] = s.value }))
    return out
  }, [data])
  const changes = Object.fromEntries(Object.entries(draft).filter(([k, v]) => v !== current[k]))
  const dirty = Object.keys(changes).length > 0

  if (isPending) return <LoadingSpinner />
  if (error || !data) return <div className="card text-red-700">{t('common.unavailable', { message: error?.message ?? '' })}</div>

  const sections = [
    ...data.groups.map((g) => ({ id: g.id, label: g.label, ...(GROUPS[g.id] ?? { icon: SlidersHorizontal, colour: 'text-gray-500' }) })),
    { id: 'apparence', label: t('config.branding'), icon: Palette, colour: 'text-pink-500' },
    { id: 'polices', label: t('fonts.title'), icon: Type, colour: 'text-lime-600' },
    { id: 'environnements', label: t('envs.title'), icon: Send, colour: 'text-fuchsia-500' },
    { id: 'demarrage', label: t('config.startup'), icon: Server, colour: 'text-slate-500' },
  ]
  const active = sections.find((s) => s.id === section)
  if (!active) return <Navigate to={`/configuration/${sections[0].id}`} replace />
  const group = data.groups.find((g) => g.id === active.id)
  const pending = (id: string) => data.groups.find((g) => g.id === id)?.settings.some((s) => s.key in changes)

  return (
    <div>
      <PageTitle crumbs={[{ label: t('nav.config'), to: '/configuration' }, { label: active.label }]}>{t('nav.config')}</PageTitle>
      <p className="text-sm text-gray-500 dark:text-gray-400 mb-6 max-w-3xl">{t('config.intro')}</p>

      <div className="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)] items-start">
        <nav className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl shadow-sm p-2 space-y-1 lg:sticky lg:top-8" aria-label={t('nav.config')}>
          {sections.map((s) => (
            <NavLink
              key={s.id}
              to={`/configuration/${s.id}`}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-brand-50 text-brand-700 font-semibold dark:bg-brand-900/30 dark:text-brand-400'
                    : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-gray-100 dark:hover:bg-gray-700'
                }`
              }
            >
              <s.icon className={`w-4 h-4 shrink-0 ${s.colour}`} />
              <span className="flex-1">{s.label}</span>
              {pending(s.id) && <span className="w-2 h-2 rounded-full bg-amber-500" />}
            </NavLink>
          ))}
        </nav>

        <div className="space-y-6 min-w-0">
          {group && (
            <SettingsCard title={group.label}>
              <div className="divide-y divide-gray-100 dark:divide-gray-700 -my-4">
                {group.settings.map((s) => (
                  <div key={s.key} className="py-4 grid gap-3 md:grid-cols-[1fr_minmax(0,22rem)] md:items-start">
                    <div>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium text-sm text-gray-900 dark:text-gray-100">{s.label}</span>
                        {!s.active && <span className="text-[11px] px-2 py-0.5 rounded-full bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400">{t('config.soon')}</span>}
                        {s.restart && <span className="text-[11px] px-2 py-0.5 rounded-full bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">{t('config.restart')}</span>}
                      </div>
                      {s.help && <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">{s.help}</p>}
                      <p className="text-xs text-gray-400 mt-1">
                        {s.locked ? (
                          <span className="inline-flex items-center gap-1"><Lock className="w-3 h-3" /> {t('config.lockedBy', { env: s.env ?? '' })}</span>
                        ) : (
                          <>{t(SOURCES[s.source])}{s.env ? ` · ${s.env}` : ''}{s.default ? ` · ${t('config.default', { value: s.default })}` : ''}</>
                        )}
                      </p>
                    </div>
                    <Input setting={s} value={draft[s.key] ?? s.value} onChange={(v) => { setSaved(false); setDraft((d) => ({ ...d, [s.key]: v })) }} />
                  </div>
                ))}
              </div>
            </SettingsCard>
          )}

          {active.id === 'apparence' && <BrandingSection />}

          {active.id === 'polices' && <FontsSection />}

          {active.id === 'environnements' && <EnvironmentsSection />}

          {active.id === 'demarrage' && (
            <SettingsCard title={t('config.startup')}>
              <p className="text-sm text-gray-500 dark:text-gray-400 mb-4">{t('config.startupHelp')}</p>
              <div className="divide-y divide-gray-100 dark:divide-gray-700">
                {data.startup.map((s) => (
                  <div key={s.key} className="py-3 flex flex-wrap justify-between gap-2 text-sm">
                    <span>
                      <span className="font-medium">{s.label}</span>
                      <span className="text-xs text-gray-400 ml-2">{s.help} · {t(SOURCES[s.source] ?? 'config.source.default')}</span>
                    </span>
                    <code className="text-gray-700 dark:text-gray-300 break-all">{s.value || '—'}</code>
                  </div>
                ))}
              </div>
            </SettingsCard>
          )}
        </div>
      </div>

      {(dirty || saved) && (
        <div className="sticky bottom-0 -mx-4 md:-mx-8 mt-6 border-t border-gray-200 dark:border-gray-700 bg-white/95 dark:bg-gray-900/95 backdrop-blur px-4 md:px-8 py-3 flex items-center justify-end gap-3 z-30">
          {update.error && <span className="text-sm text-red-700 mr-auto" role="alert">{update.error.message}</span>}
          {saved && !dirty && <span className="text-sm text-emerald-700 mr-auto">{t('config.saved')}</span>}
          <button className="btn-ghost inline-flex items-center gap-2" disabled={!dirty || update.isPending} onClick={() => setDraft({})}>
            <Undo2 className="w-4 h-4" /> {t('common.cancel')}
          </button>
          <button className="btn-primary inline-flex items-center gap-2" disabled={!dirty || update.isPending} onClick={() => update.mutate({ data: { values: changes } })}>
            <Save className="w-4 h-4" /> {t('common.save')}{dirty ? ` (${Object.keys(changes).length})` : ''}
          </button>
        </div>
      )}
    </div>
  )
}
