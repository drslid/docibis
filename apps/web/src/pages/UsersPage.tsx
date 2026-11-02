import { useState, type FormEvent } from 'react'
import { keepPreviousData, useQueryClient } from '@tanstack/react-query'
import { Info, KeyRound, Loader2, LogOut, Pencil, Power, ShieldCheck, Trash2, UserPlus, Users, X } from 'lucide-react'
import {
  getGetSessionQueryKey,
  getListUsersQueryKey,
  useCreateUser,
  useDeleteUser,
  useListUsers,
  useResetUserMfa,
  useRevokeUserSessions,
  useUpdateUser,
  type Role,
  type User,
  type UserUpdate,
} from '@/api/generated/docibis'
import { Avatar } from '@/components/common/Avatar'
import { EmptyState } from '@/components/common/EmptyState'
import { useDebounced } from '@/components/common/ListTools'
import { Modal } from '@/components/common/Modal'
import { PageTitle } from '@/components/layout/PageTitle'
import { useMe } from '@/hooks/useMe'
import { useI18n } from '@/i18n/useI18n'

const ROLES: Role[] = ['admin', 'editor', 'viewer']

const roleTone: Record<Role, string> = {
  admin: 'bg-brand-50 text-brand-700 dark:bg-brand-900/40 dark:text-brand-300',
  editor: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300',
  viewer: 'bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300',
}

export function RoleBadge({ role }: { role: Role }) {
  const { t } = useI18n()
  return <span className={`inline-block whitespace-nowrap text-xs font-medium px-2.5 py-0.5 rounded-full ${roleTone[role]}`}>{t(`role.${role}`)}</span>
}

/** "jean-pierre DUPONT" becomes "Jean-Pierre Dupont". */
export function capitalizeName(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/(^|[\s'’-])(\p{L})/gu, (_, sep: string, letter: string) => sep + letter.toUpperCase())
}

/** Create or edit an account: the e-mail is the sign-in name. */
function UserDialog({ user, onClose, onDone }: { user: User | null; onClose: () => void; onDone: () => void }) {
  const { t } = useI18n()
  const [firstName, setFirstName] = useState(user?.firstName ?? '')
  const [lastName, setLastName] = useState(user?.lastName ?? '')
  const [email, setEmail] = useState(user?.email ?? '')
  const [role, setRole] = useState<Role>(user?.role ?? 'editor')
  const [password, setPassword] = useState('')
  const create = useCreateUser({ mutation: { onSuccess: onDone } })
  const update = useUpdateUser({ mutation: { onSuccess: onDone } })
  const failure = create.error ?? update.error
  const pending = create.isPending || update.isPending

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const first = capitalizeName(firstName)
    const last = capitalizeName(lastName)
    const mail = email.trim().toLowerCase()
    if (!user) {
      create.mutate({ data: { username: mail, firstName: first, lastName: last, email: mail, role, password } })
      return
    }
    const changes: UserUpdate = {}
    if (first !== user.firstName) changes.firstName = first
    if (last !== user.lastName) changes.lastName = last
    if (mail !== user.email) changes.email = mail
    if (role !== user.role) changes.role = role
    if (password) changes.password = password
    if (Object.keys(changes).length === 0) onClose()
    else update.mutate({ id: user.id, data: changes })
  }

  const footer = (
    <div className="flex justify-end gap-2">
      <button type="button" className="btn-secondary" onClick={onClose}>
        {t('common.cancel')}
      </button>
      <button type="submit" form="user-form" className="btn-primary inline-flex items-center gap-2" disabled={pending}>
        {pending && <Loader2 className="w-4 h-4 animate-spin" />}
        {user ? t('common.save') : t('common.create')}
      </button>
    </div>
  )

  return (
    <Modal onClose={onClose} title={user ? t('users.edit', { name: user.displayName }) : t('users.new')} footer={footer}>
      <form id="user-form" className="space-y-4" onSubmit={submit}>
        <div className="grid grid-cols-2 gap-3">
          <label className="block">
            <span className="label">{t('users.firstName')}</span>
            <input className="input" value={firstName} onChange={(e) => setFirstName(e.target.value)} onBlur={() => setFirstName(capitalizeName)} autoComplete="off" required maxLength={60} />
          </label>
          <label className="block">
            <span className="label">{t('users.lastName')}</span>
            <input className="input" value={lastName} onChange={(e) => setLastName(e.target.value)} onBlur={() => setLastName(capitalizeName)} autoComplete="off" required maxLength={60} />
          </label>
        </div>
        <div>
          <label className="label" htmlFor="user-email">{t('users.col.email')}</label>
          <input
            id="user-email"
            className="input"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            onBlur={() => setEmail((v) => v.trim().toLowerCase())}
            required={!user}
            maxLength={100}
            autoComplete="off"
          />
          {!user && <span className="block text-xs text-gray-500 mt-1">{t('users.emailHint')}</span>}
          {user && user.username !== user.email && <span className="block text-xs text-gray-500 mt-1">{t('users.signInWith', { name: user.username })}</span>}
        </div>
        <div>
          <label className="label" htmlFor="user-role">{t('users.col.role')}</label>
          <select id="user-role" className="input" value={role} onChange={(e) => setRole(e.target.value as Role)}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {t(`role.${r}`)}
              </option>
            ))}
          </select>
        </div>
        <div className="flex gap-2 rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-100 dark:border-blue-900/40 px-3 py-2 text-xs text-blue-800 dark:text-blue-200">
          <Info className="w-4 h-4 shrink-0 mt-0.5" />
          <span>{t(`role.${role}.help`)}</span>
        </div>
        <label className="block">
          <span className="label">{user ? t('users.newPassword') : t('users.password')}</span>
          <input className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required={!user} minLength={user && !password ? undefined : 12} maxLength={72} />
          <span className="block text-xs text-gray-500 mt-1">{t('users.passwordHint')}</span>
        </label>
        {failure && (
          <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2 dark:bg-red-900/20 dark:border-red-900/40 dark:text-red-300" role="alert">
            {failure.message}
          </p>
        )}
      </form>
    </Modal>
  )
}

const filterInput = 'w-full min-w-[7rem] px-2 py-1 text-xs font-normal rounded-md border border-gray-200 dark:border-gray-600 bg-white dark:bg-gray-900 text-gray-700 dark:text-gray-200 focus:outline-none focus:ring-1 focus:ring-brand-500'

/** Console accounts and their role (administrators only), laid out. */
export function UsersPage() {
  const { t, date } = useI18n()
  const me = useMe()
  const queryClient = useQueryClient()
  const [text, setText] = useState('')
  const [role, setRole] = useState('')
  const [state, setState] = useState('')
  const [mfa, setMfa] = useState('')
  const [editing, setEditing] = useState<User | null | undefined>(undefined)
  const [notice, setNotice] = useState('')
  const q = useDebounced(text.trim(), 250)

  const { data, error, isPending } = useListUsers(
    { ...(q && { q }), ...(role && { role: role as Role }), ...(state && { active: state === 'active' }) },
    { query: { placeholderData: keepPreviousData } },
  )
  const users = (data ?? []).filter((u) => !mfa || (mfa === 'on') === !!u.mfaEnabled)
  const refresh = (message = '') => {
    setNotice(message)
    void queryClient.invalidateQueries({ queryKey: getListUsersQueryKey() })
    void queryClient.invalidateQueries({ queryKey: getGetSessionQueryKey() })
  }
  const update = useUpdateUser({ mutation: { onSuccess: () => refresh() } })
  const remove = useDeleteUser({ mutation: { onSuccess: () => refresh() } })
  const revoke = useRevokeUserSessions()
  const resetMfa = useResetUserMfa()
  const failure = update.error ?? remove.error ?? revoke.error ?? resetMfa.error
  const filtered = text !== '' || role !== '' || state !== '' || mfa !== ''

  const icon = 'p-1.5 rounded-lg text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-gray-700 dark:hover:text-gray-100 disabled:opacity-30 disabled:pointer-events-none'

  return (
    <div>
      <PageTitle>{t('nav.users')}</PageTitle>
      <div className="flex flex-wrap items-center justify-between gap-3 mb-6">
        <p className="text-sm text-gray-600 dark:text-gray-400 max-w-3xl">{t('users.intro2')}</p>
        <button className="btn-primary inline-flex items-center gap-2" onClick={() => setEditing(null)}>
          <UserPlus className="w-4 h-4" /> {t('users.new')}
        </button>
      </div>

      {notice && <p className="text-sm text-emerald-700 dark:text-emerald-400 mb-3" role="status">{notice}</p>}
      {failure && <p className="text-sm text-red-700 dark:text-red-400 mb-3" role="alert">{failure.message}</p>}

      <div className="card p-0 overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-sm text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">
                <th className="px-4 py-3 font-medium">{t('users.col.name')}</th>
                <th className="px-4 py-3 font-medium">{t('users.col.role')}</th>
                <th className="px-4 py-3 font-medium">{t('users.col.mfa')}</th>
                <th className="px-4 py-3 font-medium">{t('users.col.status')}</th>
                <th className="px-4 py-3 font-medium">{t('users.col.lastLogin')}</th>
                <th className="px-4 py-3 font-medium text-right">{t('users.col.actions')}</th>
              </tr>
              <tr className="border-b border-gray-100 dark:border-gray-700 bg-gray-50/50 dark:bg-gray-800/50">
                <th className="px-4 py-1.5">
                  <input value={text} onChange={(e) => setText(e.target.value)} placeholder={t('users.search')} aria-label={t('users.search')} className={filterInput} />
                </th>
                <th className="px-4 py-1.5">
                  <select value={role} onChange={(e) => setRole(e.target.value)} className={filterInput} aria-label={t('users.col.role')}>
                    <option value="">{t('users.allRoles')}</option>
                    {ROLES.map((r) => (
                      <option key={r} value={r}>
                        {t(`role.${r}`)}
                      </option>
                    ))}
                  </select>
                </th>
                <th className="px-4 py-1.5">
                  <select value={mfa} onChange={(e) => setMfa(e.target.value)} className={filterInput} aria-label={t('users.col.mfa')}>
                    <option value="">{t('users.all')}</option>
                    <option value="on">{t('mfa.on')}</option>
                    <option value="off">{t('mfa.off')}</option>
                  </select>
                </th>
                <th className="px-4 py-1.5">
                  <select value={state} onChange={(e) => setState(e.target.value)} className={filterInput} aria-label={t('users.col.status')}>
                    <option value="">{t('users.allStates')}</option>
                    <option value="active">{t('users.active')}</option>
                    <option value="inactive">{t('users.inactive')}</option>
                  </select>
                </th>
                <th className="px-4 py-1.5" />
                <th className="px-4 py-1.5">
                  {filtered && (
                    <button
                      onClick={() => {
                        setText('')
                        setRole('')
                        setState('')
                        setMfa('')
                      }}
                      className="text-xs text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 inline-flex items-center gap-1 ml-auto"
                    >
                      <X className="w-3 h-3" /> {t('common.resetFilters')}
                    </button>
                  )}
                </th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const self = u.id === me?.id
                return (
                  <tr key={u.id} className={`border-b border-gray-100 dark:border-gray-700/60 hover:bg-gray-50 dark:hover:bg-gray-700/30 transition-colors ${u.active ? '' : 'opacity-60'}`}>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-3">
                        <Avatar id={u.id} name={u.displayName} email={u.email} avatar={{ icon: u.avatarIcon, color: u.avatarColor }} role={u.role} />
                        <div className="min-w-0">
                          <div className="font-medium text-gray-900 dark:text-gray-100 truncate">
                            {u.displayName}
                            {self && <span className="ml-1 text-xs font-normal text-gray-400">({t('common.you')})</span>}
                          </div>
                          <div className="text-xs text-gray-500 dark:text-gray-400 truncate">
                            {u.email || <span className="font-mono">{u.username}</span>}
                            {u.email && u.username !== u.email && <span className="font-mono"> · {u.username}</span>}
                          </div>
                        </div>
                      </div>
                    </td>
                    <td className="px-4 py-3">
                      <RoleBadge role={u.role} />
                    </td>
                    <td className="px-4 py-3">
                      {u.mfaEnabled ? (
                        <span className="inline-flex items-center gap-1 text-xs text-emerald-700 dark:text-emerald-400">
                          <ShieldCheck className="w-3.5 h-3.5" /> {t('mfa.on')}
                        </span>
                      ) : (
                        <span className={`text-xs ${u.mfaRequired ? 'text-amber-700 dark:text-amber-400' : 'text-gray-400'}`}>{u.mfaRequired ? t('mfa.atNextLogin') : t('mfa.off')}</span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <span className={`inline-flex items-center gap-1.5 text-xs font-medium ${u.active ? 'text-emerald-700 dark:text-emerald-400' : 'text-gray-500'}`}>
                        <span className={`w-2 h-2 rounded-full ${u.active ? 'bg-emerald-500' : 'bg-gray-400'}`} />
                        {u.active ? t('users.active') : t('users.inactive')}
                      </span>
                    </td>
                    <td className="px-4 py-3 whitespace-nowrap text-gray-500 dark:text-gray-400 text-xs">{u.lastLoginAt ? date(u.lastLoginAt) : t('users.never')}</td>
                    <td className="px-4 py-3 text-right whitespace-nowrap">
                      <button className={icon} title={t('common.edit')} aria-label={`${t('common.edit')} ${u.username}`} onClick={() => setEditing(u)}>
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        className={icon}
                        disabled={!u.mfaEnabled}
                        title={t('users.resetMfa')}
                        aria-label={`${t('users.resetMfa')} ${u.username}`}
                        onClick={() =>
                          window.confirm(t('users.resetMfaConfirm', { name: u.displayName })) &&
                          resetMfa.mutate({ id: u.id }, { onSuccess: () => refresh(t('users.mfaReset', { name: u.displayName })) })
                        }
                      >
                        <KeyRound className="w-4 h-4" />
                      </button>
                      <button
                        className={icon}
                        disabled={self}
                        title={u.active ? t('users.disable') : t('users.enable')}
                        aria-label={`${u.active ? t('users.disable') : t('users.enable')} ${u.username}`}
                        onClick={() => update.mutate({ id: u.id, data: { active: !u.active } })}
                      >
                        <Power className={`w-4 h-4 ${u.active ? '' : 'text-emerald-600'}`} />
                      </button>
                      <button
                        className={icon}
                        disabled={self}
                        title={t('users.signOut')}
                        aria-label={`${t('users.signOut')} ${u.username}`}
                        onClick={() => revoke.mutate({ id: u.id }, { onSuccess: () => setNotice(t('users.signedOut', { name: u.displayName })) })}
                      >
                        <LogOut className="w-4 h-4" />
                      </button>
                      <button
                        className={`${icon} hover:text-red-600`}
                        disabled={self}
                        title={t('common.delete')}
                        aria-label={`${t('common.delete')} ${u.username}`}
                        onClick={() => window.confirm(t('users.deleteConfirm', { name: u.displayName })) && remove.mutate({ id: u.id })}
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
        {error ? (
          <p className="text-sm text-red-700 p-6" role="alert">{t('common.unavailable', { message: error.message })}</p>
        ) : isPending ? (
          <p className="text-sm text-gray-500 p-6">{t('common.loading')}</p>
        ) : (
          users.length === 0 && <EmptyState icon={<Users className="w-10 h-10" />} title={t('users.none')} />
        )}
      </div>

      {editing !== undefined && (
        <UserDialog
          key={editing?.id ?? 'new'}
          user={editing}
          onClose={() => setEditing(undefined)}
          onDone={() => {
            setEditing(undefined)
            refresh(t('common.saved'))
          }}
        />
      )}
    </div>
  )
}
