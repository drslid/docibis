import { useState } from 'react'
import { Check, Loader2, Shuffle } from 'lucide-react'
import { useSaveAvatar, type User } from '@/api/generated/docibis'
import { Avatar, AVATAR_COLOURS, AVATAR_ICONS, avatarFor, type AvatarChoice } from '@/components/common/Avatar'
import { Modal } from '@/components/common/Modal'
import { RoleBadge } from '@/pages/UsersPage'
import { useI18n } from '@/i18n/useI18n'

/** The window where the account changes its avatar, previewed at every size. */
export function AvatarPicker({ user, onClose, onSaved }: { user: User; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n()
  const current = avatarFor(user.id, { icon: user.avatarIcon, color: user.avatarColor })
  const [draft, setDraft] = useState<AvatarChoice>(current)
  const save = useSaveAvatar({ mutation: { onSuccess: onSaved } })
  const dirty = draft.icon !== current.icon || draft.color !== current.color || !user.avatarIcon
  const person = { id: user.id, name: user.displayName, email: user.email, avatar: draft, role: user.role }

  const shuffle = () => {
    const pick = <T,>(list: T[]) => list[Math.floor(Math.random() * list.length)]
    setDraft({ color: pick(AVATAR_COLOURS), icon: pick(AVATAR_ICONS)[0] })
  }

  const footer = (
    <div className="flex flex-wrap items-center justify-end gap-2">
      {save.error && (
        <p role="alert" className="mr-auto text-sm text-red-600 dark:text-red-400">
          {save.error.message}
        </p>
      )}
      <button type="button" onClick={onClose} disabled={save.isPending} className="btn-secondary">
        {t('common.cancel')}
      </button>
      <button type="button" onClick={() => save.mutate({ data: draft })} disabled={!dirty || save.isPending} className="btn-primary inline-flex items-center gap-2">
        {save.isPending && <Loader2 className="w-4 h-4 animate-spin" />} {t('common.save')}
      </button>
    </div>
  )

  return (
    <Modal onClose={save.isPending ? () => {} : onClose} title={t('avatar.title')} size="lg" footer={footer}>
      <div className="space-y-6">
        <div className="grid grid-cols-[auto_minmax(0,1fr)] sm:grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-4 sm:gap-x-6 gap-y-3 rounded-xl border border-gray-100 dark:border-gray-700/70 bg-gray-50 dark:bg-gray-900/40 p-4 sm:p-5">
          <div className="row-span-2 sm:row-span-1">
            <Avatar {...person} size="xl" />
          </div>
          <div className="min-w-0">
            <div className="font-semibold text-gray-900 dark:text-gray-100 truncate">{user.displayName}</div>
            <div className="mt-1">
              <RoleBadge role={user.role} />
            </div>
            <div className="mt-3 sm:mt-4 flex items-center gap-3 sm:gap-4">
              <span className="hidden sm:inline-flex">
                <Avatar {...person} size="lg" decorative />
              </span>
              <Avatar {...person} size="md" decorative />
              <Avatar {...person} size="sm" decorative />
              <Avatar {...person} size="xs" decorative />
            </div>
          </div>
          <button type="button" onClick={shuffle} className="btn-secondary text-sm inline-flex items-center gap-2 justify-self-start sm:justify-self-end">
            <Shuffle className="w-4 h-4" /> {t('avatar.random')}
          </button>
        </div>

        <p className="text-sm text-gray-500 dark:text-gray-400 -mt-2">{t('avatar.hint')}</p>

        <div>
          <div className="text-xs font-medium text-gray-500 dark:text-gray-400 mb-2" id="avatar-colours">
            {t('avatar.colour')}
          </div>
          <div role="radiogroup" aria-labelledby="avatar-colours" className="flex flex-wrap gap-2.5">
            {AVATAR_COLOURS.map((colour, i) => {
              const chosen = draft.color === colour
              return (
                <button
                  key={colour}
                  type="button"
                  role="radio"
                  aria-checked={chosen}
                  aria-label={`${t('avatar.colour')} ${i + 1}`}
                  onClick={() => setDraft((d) => ({ ...d, color: colour }))}
                  className={`w-9 h-9 rounded-full flex items-center justify-center ring-offset-2 ring-offset-white dark:ring-offset-gray-800 transition ${chosen ? 'ring-2 ring-gray-900 dark:ring-gray-100' : 'hover:scale-110'}`}
                  style={{ backgroundColor: colour }}
                >
                  {chosen && <Check className="w-4 h-4 text-white" strokeWidth={3} />}
                </button>
              )
            })}
          </div>
        </div>

        <div>
          <div className="text-xs font-medium text-gray-500 dark:text-gray-400 mb-2" id="avatar-icons">
            {t('avatar.icon')}
          </div>
          <div role="radiogroup" aria-labelledby="avatar-icons" className="grid grid-cols-8 sm:grid-cols-12 gap-1.5">
            {AVATAR_ICONS.map(([name, Icon], i) => {
              const chosen = draft.icon === name
              return (
                <button
                  key={name}
                  type="button"
                  role="radio"
                  aria-checked={chosen}
                  aria-label={`${t('avatar.icon')} ${i + 1}`}
                  onClick={() => setDraft((d) => ({ ...d, icon: name }))}
                  className={`aspect-square rounded-lg flex items-center justify-center transition-colors ${
                    chosen ? 'text-white shadow-sm' : 'border border-gray-200 dark:border-gray-700 text-gray-600 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700'
                  }`}
                  style={chosen ? { backgroundColor: draft.color } : undefined}
                >
                  <Icon className="w-5 h-5" />
                </button>
              )
            })}
          </div>
        </div>
      </div>
    </Modal>
  )
}
