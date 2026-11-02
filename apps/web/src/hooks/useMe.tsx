import type { ReactNode } from 'react'
import { Lock } from 'lucide-react'
import { useGetSession, type Role, type User } from '@/api/generated/docibis'
import { EmptyState } from '@/components/common/EmptyState'
import { useI18n } from '@/i18n/useI18n'

// Same order as the server: each role includes the ones below.
const rank: Record<Role, number> = { viewer: 1, editor: 2, admin: 3 }

export function atLeast(user: User | undefined, role: Role): boolean {
  return !!user && rank[user.role] >= rank[role]
}

/** The signed-in account. */
export function useMe(): User | undefined {
  return useGetSession({ query: { retry: false } }).data?.user
}

/** Shows the page only to the roles allowed; the server checks again. */
export function RequireRole({ role, children }: { role: Role; children: ReactNode }) {
  const me = useMe()
  const { t } = useI18n()
  if (!atLeast(me, role)) {
    return <EmptyState icon={<Lock className="w-12 h-12" />} title={t('forbidden.title')} description={t('forbidden.text')} />
  }
  return children
}
