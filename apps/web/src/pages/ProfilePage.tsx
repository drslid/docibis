import { useState, type FormEvent, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { KeyRound, Loader2, LogOut, Monitor, Moon, Palette, Pencil, Plug, Plus, ShieldCheck, ShieldOff, Sun, Trash2, UserRound } from 'lucide-react'
import {
  getGetSessionQueryKey,
  getListMySessionsQueryKey,
  getListMyTokensQueryKey,
  useChangePassword,
  useCloseMySession,
  useCloseOtherSessions,
  useConfirmMfa,
  useCreateMyToken,
  useDisableMfa,
  useGetMcpInfo,
  useListMySessions,
  useListMyTokens,
  useRevokeMyToken,
  useStartMfa,
  useUpdateProfile,
  type MfaStep,
  type UserTokenCreated,
} from '@/api/generated/docibis'
import { Avatar } from '@/components/common/Avatar'
import { CopyButton } from '@/components/common/CopyButton'
import { Flag } from '@/components/common/Flag'
import { PageTitle } from '@/components/layout/PageTitle'
import { AvatarPicker } from '@/components/profile/AvatarPicker'
import { CodeField, TotpEnrollment } from '@/components/profile/TotpEnrollment'
import { useMe } from '@/hooks/useMe'
import { useTheme } from '@/hooks/useTheme'
import { LOCALE_LABELS } from '@/i18n/translations'
import { LOCALES, tag, useI18n } from '@/i18n/useI18n'
import { RoleBadge, capitalizeName } from './UsersPage'

type Section = 'profile' | 'preferences' | 'security' | 'tokens'
const SECTIONS: Section[] = ['profile', 'preferences', 'security', 'tokens']

function Card({ icon, title, hint, action, children }: { icon: ReactNode; title: string; hint?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      <div className="flex flex-wrap items-center justify-between gap-2 mb-1">
        <h2 className="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-gray-100">
          {icon}
          {title}
        </h2>
        {action}
      </div>
      {hint && <p className="text-sm text-gray-500 dark:text-gray-400 mb-4">{hint}</p>}
      {!hint && <div className="mb-4" />}
      {children}
    </section>
  )
}

function Message({ error, ok }: { error?: string; ok?: string }) {
  if (error) return <p className="text-sm text-red-700 dark:text-red-400" role="alert">{error}</p>
  if (ok) return <p className="text-sm text-emerald-700 dark:text-emerald-400" role="status">{ok}</p>
  return null
}

/** A readable device from the browser's user agent. */
export function deviceOf(agent?: string): string {
  if (!agent) return ''
  const browser = /Edg\//.test(agent) ? 'Edge' : /Firefox\//.test(agent) ? 'Firefox' : /Chrome\//.test(agent) ? 'Chrome' : /Safari\//.test(agent) ? 'Safari' : ''
  const os = /Windows/.test(agent) ? 'Windows' : /Mac OS X/.test(agent) ? 'macOS' : /Android/.test(agent) ? 'Android' : /iPhone|iPad/.test(agent) ? 'iOS' : /Linux/.test(agent) ? 'Linux' : ''
  return [browser, os].filter(Boolean).join(' · ') || agent.slice(0, 60)
}

/** The signed-in account. */
export function ProfilePage() {
  const me = useMe()
  const { t, locale, setLocale, date } = useI18n()
  const { theme, toggle } = useTheme()
  const queryClient = useQueryClient()
  const reload = () => queryClient.invalidateQueries({ queryKey: getGetSessionQueryKey() })
  const [params, setParams] = useSearchParams()
  const asked = params.get('section') as Section | null
  const section: Section = asked && SECTIONS.includes(asked) ? asked : 'profile'
  const [avatarOpen, setAvatarOpen] = useState(false)
  const [notice, setNotice] = useState('')

  const [first, setFirst] = useState(me?.firstName ?? '')
  const [last, setLast] = useState(me?.lastName ?? '')
  const profile = useUpdateProfile({ mutation: { onSuccess: () => void reload() } })

  const sessions = useListMySessions()
  const tokens = useListMyTokens()

  if (!me) return null

  const go = (next: Section) => {
    setNotice('')
    setParams(next === 'profile' ? {} : { section: next }, { replace: true })
  }
  const activeTokens = (tokens.data ?? []).filter((x) => x.status === 'active').length
  const tabs: { key: Section; label: string; badge?: number }[] = [
    { key: 'profile', label: t('profile.identity') },
    { key: 'preferences', label: t('profile.preferences') },
    { key: 'security', label: t('profile.security'), badge: (sessions.data?.length ?? 0) > 1 ? sessions.data?.length : undefined },
    { key: 'tokens', label: t('tokens.title'), badge: activeTokens || undefined },
  ]
  const chip = (active: boolean) =>
    `inline-flex items-center gap-2 px-3 py-2 rounded-lg border text-sm font-medium transition-colors ${
      active ? 'border-brand-500 bg-brand-50 text-brand-700 dark:bg-brand-900/30 dark:text-brand-300' : 'border-gray-200 dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-700/40'
    }`

  return (
    <div>
      <PageTitle>{t('nav.profile')}</PageTitle>

      <section className="card mb-6">
        <div className="flex flex-col sm:flex-row sm:items-center gap-4">
          <button
            type="button"
            onClick={() => setAvatarOpen(true)}
            aria-label={t('avatar.change')}
            title={t('avatar.change')}
            className="group relative self-start sm:self-center rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-500 focus-visible:ring-offset-[6px]"
          >
            <span className="block rounded-full transition group-hover:brightness-90">
              <Avatar id={me.id} name={me.displayName} email={me.email} avatar={{ icon: me.avatarIcon, color: me.avatarColor }} size="lg" role={me.role} decorative />
            </span>
            <span
              aria-hidden="true"
              className="absolute -right-1.5 -bottom-1.5 w-6 h-6 rounded-full bg-white dark:bg-gray-700 text-gray-700 dark:text-gray-100 shadow ring-2 ring-white dark:ring-gray-800 flex items-center justify-center transition group-hover:scale-110 group-hover:bg-brand-600 group-hover:text-white"
            >
              <Pencil className="w-3 h-3" />
            </span>
          </button>
          <div className="flex-1 min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100 truncate">{me.displayName}</h2>
              <RoleBadge role={me.role} />
              {me.mfaEnabled ? (
                <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
                  <ShieldCheck className="w-3.5 h-3.5" /> {t('mfa.on')}
                </span>
              ) : (
                me.mfaRequired && (
                  <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300">
                    <ShieldOff className="w-3.5 h-3.5" /> {t('mfa.required')}
                  </span>
                )
              )}
            </div>
            <div className="text-sm text-gray-500 dark:text-gray-400 truncate">{me.email || me.username}</div>
            <div className="text-xs text-gray-400 font-mono mt-0.5">{me.username}</div>
          </div>
          <div className="text-xs text-gray-500 dark:text-gray-400 sm:text-right space-y-0.5">
            <div>{t('profile.since', { date: date(me.createdAt, false) })}</div>
            {me.lastLoginAt && <div>{t('profile.lastLogin', { date: date(me.lastLoginAt) })}</div>}
          </div>
        </div>
      </section>

      <div className="flex flex-wrap gap-1 bg-gray-100 dark:bg-gray-800 rounded-lg p-1 max-w-full mb-6 w-fit">
        {tabs.map(({ key, label, badge }) => (
          <button
            key={key}
            onClick={() => go(key)}
            aria-current={section === key ? 'page' : undefined}
            className={`px-3 sm:px-4 py-2 rounded-md text-sm font-medium whitespace-nowrap transition-colors inline-flex items-center gap-1.5 ${
              section === key ? 'bg-brand-600 text-white' : 'text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200'
            }`}
          >
            {label}
            {!!badge && <span className={`text-[11px] px-1.5 rounded-full ${section === key ? 'bg-white/20' : 'bg-gray-200 dark:bg-gray-700'}`}>{badge}</span>}
          </button>
        ))}
      </div>

      <div className="min-w-0 space-y-6">
        {notice && <p className="p-3 rounded-lg text-sm bg-emerald-50 text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300" role="status">{notice}</p>}

        {section === 'profile' && (
          <Card icon={<UserRound className="w-5 h-5" />} title={t('profile.identity')}>
            <form
              className="space-y-4"
              onSubmit={(e: FormEvent) => {
                e.preventDefault()
                profile.mutate({ data: { firstName: capitalizeName(first), lastName: capitalizeName(last) } }, { onSuccess: () => setNotice(t('common.saved')) })
              }}
            >
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
                <label className="block">
                  <span className="label">{t('users.firstName')}</span>
                  <input className="input" value={first} onChange={(e) => setFirst(e.target.value)} onBlur={() => setFirst(capitalizeName)} maxLength={60} autoComplete="given-name" />
                </label>
                <label className="block">
                  <span className="label">{t('users.lastName')}</span>
                  <input className="input" value={last} onChange={(e) => setLast(e.target.value)} onBlur={() => setLast(capitalizeName)} maxLength={60} autoComplete="family-name" />
                </label>
              </div>
              <dl className="grid grid-cols-1 sm:grid-cols-3 gap-4 rounded-lg bg-gray-50 dark:bg-gray-900/40 px-4 py-3">
                <div className="min-w-0">
                  <dt className="text-xs font-medium text-gray-500 dark:text-gray-400">{t('users.col.email')}</dt>
                  <dd className="text-sm text-gray-800 dark:text-gray-200 mt-0.5 break-all">{me.email || '—'}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-xs font-medium text-gray-500 dark:text-gray-400">{t('users.username')}</dt>
                  <dd className="text-sm text-gray-800 dark:text-gray-200 mt-0.5 font-mono break-all">{me.username}</dd>
                </div>
                <div>
                  <dt className="text-xs font-medium text-gray-500 dark:text-gray-400">{t('users.col.role')}</dt>
                  <dd className="mt-1">
                    <RoleBadge role={me.role} />
                  </dd>
                </div>
              </dl>
              <p className="text-xs text-gray-400">{t('profile.identityHint')}</p>
              <Message error={profile.error?.message} />
              <div className="flex justify-end">
                <button type="submit" className="btn-primary inline-flex items-center gap-2" disabled={profile.isPending || (first.trim() === me.firstName && last.trim() === me.lastName)}>
                  {profile.isPending && <Loader2 className="w-4 h-4 animate-spin" />} {t('common.save')}
                </button>
              </div>
            </form>
          </Card>
        )}

        {section === 'preferences' && (
          <Card icon={<Palette className="w-5 h-5" />} title={t('profile.preferences')} hint={t('profile.preferencesHint')}>
            <div className="space-y-4">
              <div>
                <div className="label">{t('profile.language')}</div>
                <div className="flex flex-wrap gap-2">
                  {LOCALES.map((l) => (
                    <button
                      key={l}
                      type="button"
                      lang={l}
                      className={chip(l === locale)}
                      aria-pressed={l === locale}
                      onClick={() => {
                        setLocale(l)
                        if (me.language !== l) profile.mutate({ data: { language: l } })
                      }}
                    >
                      <Flag locale={l} /> {LOCALE_LABELS[l]}
                    </button>
                  ))}
                </div>
              </div>
              <div>
                <div className="label">{t('profile.theme')}</div>
                <div className="flex gap-2">
                  <button type="button" className={chip(theme === 'light')} aria-pressed={theme === 'light'} onClick={() => theme !== 'light' && toggle()}>
                    <Sun className="w-4 h-4" /> {t('profile.light')}
                  </button>
                  <button type="button" className={chip(theme === 'dark')} aria-pressed={theme === 'dark'} onClick={() => theme !== 'dark' && toggle()}>
                    <Moon className="w-4 h-4" /> {t('profile.dark')}
                  </button>
                </div>
              </div>
            </div>
          </Card>
        )}

        {section === 'security' && (
          <>
            <PasswordCard onDone={(text) => setNotice(text)} />
            <MfaCard onDone={(text) => { setNotice(text); void reload() }} />
            <SessionsCard />
          </>
        )}

        {section === 'tokens' && <TokensCard />}
      </div>

      {avatarOpen && (
        <AvatarPicker
          user={me}
          onClose={() => setAvatarOpen(false)}
          onSaved={() => {
            void reload()
            setAvatarOpen(false)
            setNotice(t('avatar.saved'))
          }}
        />
      )}
    </div>
  )
}

function PasswordCard({ onDone }: { onDone: (text: string) => void }) {
  const { t } = useI18n()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const password = useChangePassword({
    mutation: {
      onSuccess: () => {
        setCurrent('')
        setNext('')
        setConfirm('')
        onDone(t('profile.changed'))
      },
    },
  })
  const mismatch = confirm !== '' && confirm !== next
  return (
    <Card icon={<KeyRound className="w-5 h-5" />} title={t('profile.change')}>
      <form
        className="grid grid-cols-1 sm:grid-cols-3 gap-4 items-end"
        onSubmit={(e) => {
          e.preventDefault()
          if (!mismatch) password.mutate({ data: { currentPassword: current, newPassword: next } })
        }}
      >
        <label className="block">
          <span className="label">{t('profile.current')}</span>
          <input className="input" type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" required maxLength={72} />
        </label>
        <label className="block">
          <span className="label">{t('profile.new')}</span>
          <input className="input" type="password" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" required minLength={12} maxLength={72} />
        </label>
        <label className="block">
          <span className="label">{t('profile.confirm')}</span>
          <input className="input" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required maxLength={72} />
        </label>
        <div className="sm:col-span-3 flex flex-wrap items-center justify-between gap-3">
          <Message error={mismatch ? t('setup.mismatch') : password.error?.message} />
          <button type="submit" className="btn-primary ml-auto" disabled={password.isPending || mismatch}>
            {t('common.save')}
          </button>
        </div>
      </form>
    </Card>
  )
}

function MfaCard({ onDone }: { onDone: (text: string) => void }) {
  const me = useMe()
  const { t } = useI18n()
  const [step, setStep] = useState<MfaStep | null>(null)
  const [code, setCode] = useState('')
  const [disabling, setDisabling] = useState(false)
  const start = useStartMfa({ mutation: { onSuccess: (s) => { setCode(''); setStep(s) } } })
  const confirm = useConfirmMfa({ mutation: { onSuccess: () => { setStep(null); onDone(t('mfa.enabled')) } } })
  const disable = useDisableMfa({ mutation: { onSuccess: () => { setDisabling(false); setCode(''); onDone(t('mfa.disabled')) } } })
  if (!me) return null

  return (
    <Card
      icon={<ShieldCheck className="w-5 h-5" />}
      title={t('mfa.cardTitle')}
      hint={me.mfaRequired ? t('mfa.requiredHint') : t('mfa.optionalHint')}
      action={
        me.mfaEnabled ? (
          <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
            <ShieldCheck className="w-3.5 h-3.5" /> {t('mfa.on')}
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded-full bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300">
            <ShieldOff className="w-3.5 h-3.5" /> {t('mfa.off')}
          </span>
        )
      }
    >
      {step ? (
        <form
          className="max-w-sm space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            confirm.mutate({ data: { ticket: step.ticket, code } })
          }}
        >
          <TotpEnrollment step={step} />
          <CodeField code={code} onCode={setCode} />
          <Message error={confirm.error?.message} />
          <div className="flex gap-2">
            <button type="submit" className="btn-primary" disabled={confirm.isPending || code.length !== 6}>
              {t('mfa.enrollSubmit')}
            </button>
            <button type="button" className="btn-ghost" onClick={() => setStep(null)}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      ) : disabling ? (
        <form
          className="max-w-sm space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            disable.mutate({ data: { code } })
          }}
        >
          <CodeField code={code} onCode={setCode} />
          <Message error={disable.error?.message} />
          <div className="flex gap-2">
            <button type="submit" className="btn-danger" disabled={disable.isPending || code.length !== 6}>
              {t('mfa.disable')}
            </button>
            <button type="button" className="btn-ghost" onClick={() => setDisabling(false)}>
              {t('common.cancel')}
            </button>
          </div>
        </form>
      ) : (
        <div className="flex flex-wrap gap-2">
          <button type="button" className="btn-primary inline-flex items-center gap-2" disabled={start.isPending} onClick={() => start.mutate()}>
            <ShieldCheck className="w-4 h-4" /> {me.mfaEnabled ? t('mfa.renew') : t('mfa.enable')}
          </button>
          {me.mfaEnabled && !me.mfaRequired && (
            <button type="button" className="btn-secondary" onClick={() => { setCode(''); setDisabling(true) }}>
              {t('mfa.disable')}
            </button>
          )}
          <Message error={start.error?.message} />
        </div>
      )}
    </Card>
  )
}

function SessionsCard() {
  const { t, locale, date } = useI18n()
  const queryClient = useQueryClient()
  const sessions = useListMySessions()
  const refresh = () => queryClient.invalidateQueries({ queryKey: getListMySessionsQueryKey() })
  const close = useCloseMySession({ mutation: { onSuccess: (_, v) => { if ((sessions.data ?? []).find((s) => s.id === v.id)?.current) window.location.reload(); else void refresh() } } })
  const others = useCloseOtherSessions({ mutation: { onSuccess: () => void refresh() } })
  const [now] = useState(() => Date.now())
  const ago = (iso?: string) => {
    if (!iso) return '—'
    const rtf = new Intl.RelativeTimeFormat(tag(locale), { numeric: 'auto' })
    const minutes = Math.round((new Date(iso).getTime() - now) / 60000)
    if (minutes > -1) return rtf.format(0, 'second')
    if (minutes > -60) return rtf.format(minutes, 'minute')
    if (minutes > -24 * 60) return rtf.format(Math.round(minutes / 60), 'hour')
    return date(iso)
  }
  const list = sessions.data ?? []
  return (
    <Card
      icon={<Monitor className="w-5 h-5" />}
      title={t('profile.sessions')}
      hint={t('sessions.hint')}
      action={
        list.length > 1 && (
          <button className="btn-secondary text-sm inline-flex items-center gap-1.5" disabled={others.isPending} onClick={() => others.mutate()}>
            <LogOut className="w-4 h-4" /> {t('profile.closeOthers')}
          </button>
        )
      }
    >
      <ul className="divide-y divide-gray-100 dark:divide-gray-700/60">
        {list.map((s) => (
          <li key={s.id} className="py-2.5 flex items-center gap-3">
            <Monitor className="w-4 h-4 text-gray-400 shrink-0" />
            <div className="flex-1 min-w-0">
              <div className="flex flex-wrap items-center gap-2 text-sm text-gray-900 dark:text-gray-100">
                {deviceOf(s.userAgent) || t('sessions.unknownDevice')}
                {s.current && <span className="text-[11px] px-1.5 py-0.5 rounded-full bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">{t('sessions.current')}</span>}
              </div>
              <div className="text-xs text-gray-400">
                {t('sessions.opened', { date: date(s.createdAt) })} · {t('sessions.lastUsed', { date: ago(s.lastUsedAt) })}
              </div>
            </div>
            <button className="btn-secondary text-xs px-2 py-1" disabled={close.isPending} onClick={() => close.mutate({ id: s.id })}>
              {t('sessions.close')}
            </button>
          </li>
        ))}
      </ul>
      <Message error={close.error?.message ?? others.error?.message} />
    </Card>
  )
}

function TokensCard() {
  const { t, date } = useI18n()
  const queryClient = useQueryClient()
  const tokens = useListMyTokens()
  const info = useGetMcpInfo()
  const [name, setName] = useState('')
  const [created, setCreated] = useState<UserTokenCreated | null>(null)
  const refresh = () => queryClient.invalidateQueries({ queryKey: getListMyTokensQueryKey() })
  const create = useCreateMyToken({ mutation: { onSuccess: (data) => { setCreated(data); setName(''); void refresh() } } })
  const revoke = useRevokeMyToken({ mutation: { onSuccess: () => void refresh() } })
  const url = `${window.location.origin}${info.data?.url ?? '/v1/mcp'}`
  const config = created
    ? JSON.stringify(
        { mcpServers: { docibis: { command: 'npx', args: ['-y', 'mcp-remote', url, '--header', 'Authorization:Bearer ${DOCIBIS_API_KEY}'], env: { DOCIBIS_API_KEY: created.secret } } } },
        null,
        2,
      )
    : ''
  const tone: Record<string, string> = {
    active: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
    expired: 'bg-amber-50 text-amber-800 dark:bg-amber-900/30 dark:text-amber-300',
    revoked: 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300',
  }

  return (
    <Card icon={<Plug className="w-5 h-5" />} title={t('tokens.title')} hint={t('tokens.hint')}>
      <form
        className="flex flex-wrap gap-2 mb-4"
        onSubmit={(e) => {
          e.preventDefault()
          setCreated(null)
          create.mutate({ data: { name: name.trim() } })
        }}
      >
        <input className="input flex-1 min-w-[14rem]" value={name} onChange={(e) => setName(e.target.value)} placeholder={t('tokens.namePlaceholder')} maxLength={100} required aria-label={t('tokens.name')} />
        <button type="submit" className="btn-primary inline-flex items-center gap-2" disabled={create.isPending || !name.trim()}>
          <Plus className="w-4 h-4" /> {t('tokens.create')}
        </button>
      </form>
      <Message error={create.error?.message ?? revoke.error?.message} />

      {created && (
        <div className="rounded-lg border border-amber-300 bg-amber-50 dark:bg-amber-900/20 dark:border-amber-700 p-4 mb-4 space-y-3">
          <p className="text-sm font-medium text-amber-900 dark:text-amber-200">{t('tokens.once')}</p>
          <div className="flex items-center gap-2">
            <code className="flex-1 rounded bg-white dark:bg-gray-900 px-3 py-2 text-xs break-all">{created.secret}</code>
            <CopyButton text={created.secret} />
          </div>
          <div>
            <div className="text-xs text-amber-900 dark:text-amber-200 mb-1">{t('tokens.config')}</div>
            <pre className="rounded bg-gray-900 text-gray-100 text-xs p-3 overflow-x-auto">{config}</pre>
            <CopyButton text={config} className="mt-2" />
          </div>
        </div>
      )}

      {(tokens.data ?? []).length === 0 ? (
        <p className="text-sm text-gray-400 text-center py-3">{t('tokens.none')}</p>
      ) : (
        <ul className="divide-y divide-gray-100 dark:divide-gray-700/60">
          {(tokens.data ?? []).map((tk) => (
            <li key={tk.id} className="py-2.5 flex items-center gap-3">
              <Plug className="w-4 h-4 text-gray-400 shrink-0" />
              <div className="flex-1 min-w-0">
                <div className="flex flex-wrap items-center gap-2 text-sm text-gray-900 dark:text-gray-100">
                  <span className="font-medium">{tk.name}</span>
                  <code className="text-xs text-gray-500">{tk.prefix}…</code>
                  <span className={`text-[11px] px-1.5 py-0.5 rounded-full ${tone[tk.status]}`}>{t(`tokens.status.${tk.status}`)}</span>
                </div>
                <div className="text-xs text-gray-400">
                  {t('tokens.created', { date: date(tk.createdAt) })}
                  {tk.expiresAt && <> · {t('tokens.expires', { date: date(tk.expiresAt, false) })}</>}
                  {' · '}
                  {tk.lastUsedAt ? t('tokens.used', { date: date(tk.lastUsedAt) }) : t('tokens.never')}
                </div>
              </div>
              {tk.status === 'active' && (
                <button
                  className="p-1.5 rounded-lg text-gray-500 hover:bg-gray-100 hover:text-red-600 dark:hover:bg-gray-700"
                  title={t('tokens.revoke')}
                  aria-label={`${t('tokens.revoke')} ${tk.name}`}
                  onClick={() => window.confirm(t('tokens.revokeConfirm', { name: tk.name })) && revoke.mutate({ id: tk.id })}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
