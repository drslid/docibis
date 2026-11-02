import { Link, NavLink } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import {
  Activity,
  BookOpen,
  ChevronLeft,
  FileStack,
  FileText,
  FlaskConical,
  KeyRound,
  LayoutDashboard,
  Layers,
  LogOut,
  Megaphone,
  Plug,
  ScrollText,
  Settings,
  Users,
  type LucideIcon,
} from 'lucide-react'
import { getGetSessionQueryKey, useGetInfo, useGetSession, useListReleases, useLogout, type Role } from '@/api/generated/docibis'
import { atLeast } from '@/hooks/useMe'
import { useReleaseSeen } from '@/hooks/useReleaseSeen'
import { useI18n, type Key } from '@/i18n/useI18n'
import { BrandLogo } from './BrandLogo'

interface Item {
  to: string
  label: Key
  icon: LucideIcon
  colour: string
  role: Role
}

// Each entry keeps its colour and shows only to the roles allowed.
const groups: { title: Key; items: Item[] }[] = [
  {
    title: 'nav.documents',
    items: [
      { to: '/', label: 'nav.dashboard', icon: LayoutDashboard, colour: 'text-sky-500 dark:text-sky-400', role: 'viewer' },
      { to: '/modeles', label: 'nav.library', icon: FileStack, colour: 'text-blue-600 dark:text-blue-400', role: 'viewer' },
      { to: '/liasses', label: 'nav.bundles', icon: Layers, colour: 'text-fuchsia-500 dark:text-fuchsia-400', role: 'viewer' },
      { to: '/essai', label: 'nav.try', icon: FlaskConical, colour: 'text-amber-500 dark:text-amber-400', role: 'editor' },
      { to: '/documents', label: 'nav.history', icon: FileText, colour: 'text-emerald-500 dark:text-emerald-400', role: 'viewer' },
    ],
  },
  {
    title: 'nav.resources',
    items: [{ to: '/integrations', label: 'nav.integrations', icon: Plug, colour: 'text-violet-500 dark:text-violet-400', role: 'viewer' }],
  },
  {
    title: 'nav.supervision',
    items: [
      { to: '/monitoring', label: 'nav.monitoring', icon: Activity, colour: 'text-indigo-500 dark:text-indigo-400', role: 'admin' },
      { to: '/journal', label: 'nav.audit', icon: ScrollText, colour: 'text-rose-500 dark:text-rose-400', role: 'admin' },
    ],
  },
  {
    title: 'nav.admin',
    items: [
      { to: '/utilisateurs', label: 'nav.users', icon: Users, colour: 'text-cyan-500 dark:text-cyan-400', role: 'admin' },
      { to: '/cles-api', label: 'nav.keys', icon: KeyRound, colour: 'text-amber-600 dark:text-amber-400', role: 'admin' },
      { to: '/configuration', label: 'nav.config', icon: Settings, colour: 'text-slate-500 dark:text-slate-400', role: 'admin' },
    ],
  },
]

const linkClass = ({ isActive }: { isActive: boolean }) =>
  `flex items-center gap-3 px-3 py-2 rounded-lg transition-colors text-sm font-medium ${
    isActive
      ? 'bg-brand-50 text-brand-700 font-semibold dark:bg-brand-900/30 dark:text-brand-400'
      : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-gray-100 dark:hover:bg-gray-800'
  }`

interface SidebarProps {
  mobileOpen: boolean
  onMobileClose: () => void
  collapsed: boolean
  onToggleCollapse: () => void
}

export function Sidebar({ mobileOpen, onMobileClose, collapsed, onToggleCollapse }: SidebarProps) {
  const { data: info } = useGetInfo({ query: { retry: false } })
  const { data: session } = useGetSession()
  const queryClient = useQueryClient()
  const logout = useLogout({ mutation: { onSettled: () => queryClient.invalidateQueries({ queryKey: getGetSessionQueryKey() }) } })
  const user = session?.user
  const { t } = useI18n()
  const { data: releases } = useListReleases({ limit: 1 }, { query: { retry: false, staleTime: 300_000 } })
  const seen = useReleaseSeen((s) => s.seen)
  const unseen = (releases?.items[0]?.id ?? 0) > seen
  const visible = groups
    .map((group) => ({ ...group, items: group.items.filter((item) => atLeast(user, item.role)) }))
    .filter((group) => group.items.length > 0)

  return (
    <aside
      className={`fixed left-0 top-0 h-screen bg-white dark:bg-gray-900 border-r border-gray-200 dark:border-gray-700 flex flex-col transition-all z-50 ${
        collapsed ? 'w-16' : 'w-60'
      } ${mobileOpen ? 'translate-x-0' : '-translate-x-full'} md:translate-x-0`}
    >
      <div className="flex items-start px-3 py-4 border-b border-gray-200 dark:border-gray-700">
        <div className="flex-1 min-w-0">
          <BrandLogo collapsed={collapsed} />
        </div>
        {!collapsed && (
          <button onClick={onToggleCollapse} className="shrink-0 text-gray-400 hover:text-gray-600" aria-label={t('nav.collapse')}>
            <ChevronLeft className="w-4 h-4" />
          </button>
        )}
      </div>
      {collapsed && (
        <button onClick={onToggleCollapse} className="mx-auto mt-2 text-gray-400 hover:text-gray-600" aria-label={t('nav.expand')}>
          <ChevronLeft className="w-4 h-4 rotate-180" />
        </button>
      )}

      <nav className="flex-1 px-2 py-4 overflow-y-auto dark:text-gray-300">
        {visible.map((group, index) => (
          <div key={group.title} className="space-y-1">
            {collapsed ? (
              index > 0 && <div className="border-t border-gray-200 dark:border-gray-700 my-3 mx-2" />
            ) : (
              <div className={`px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-gray-400 dark:text-gray-500 ${index > 0 ? 'pt-3' : ''}`}>
                {t(group.title)}
              </div>
            )}
            {group.items.map((item) => (
              <NavLink key={item.to} to={item.to} end={item.to === '/'} className={linkClass} onClick={onMobileClose} title={collapsed ? t(item.label) : undefined}>
                <item.icon className={`w-5 h-5 shrink-0 ${item.colour}`} />
                {!collapsed && <span className="flex-1">{t(item.label)}</span>}
              </NavLink>
            ))}
          </div>
        ))}
      </nav>

      <div className="border-t border-gray-200 dark:border-gray-700 px-2 py-3 space-y-1">
        {user && (
          <Link
            to="/profil"
            onClick={onMobileClose}
            className="flex items-center gap-3 px-3 py-2 rounded-lg hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
            title={collapsed ? `${user.displayName || user.username} · ${t('nav.profile')}` : t('nav.profile')}
          >
            <div className="w-5 h-5 rounded-full shrink-0 flex items-center justify-center text-[10px] font-bold text-white bg-brand-600">
              {(user.displayName || user.username)[0]?.toUpperCase()}
            </div>
            {!collapsed && (
              <div className="truncate">
                <div className="text-sm text-gray-700 dark:text-gray-300 truncate">{user.displayName || user.username}</div>
                <div className="text-xs text-gray-400">{t(`role.${user.role}`)}</div>
              </div>
            )}
          </Link>
        )}
        <NavLink to="/aide" className={linkClass} onClick={onMobileClose} title={collapsed ? t('nav.help') : undefined}>
          <BookOpen className="w-5 h-5 shrink-0 text-teal-500 dark:text-teal-400" />
          {!collapsed && <span className="flex-1">{t('nav.help')}</span>}
        </NavLink>
        <NavLink to="/nouveautes" className={linkClass} onClick={onMobileClose} title={collapsed ? t('nav.releases') : undefined}>
          <span className="relative shrink-0">
            <Megaphone className="w-5 h-5 text-orange-500 dark:text-orange-400" />
            {unseen && <span className="absolute -top-0.5 -right-0.5 w-2 h-2 rounded-full bg-orange-500 ring-2 ring-white dark:ring-gray-900" aria-label={t('rel.unseen')} />}
          </span>
          {!collapsed && <span className="flex-1">{t('nav.releases')}</span>}
        </NavLink>
        <button
          onClick={() => logout.mutate()}
          className="flex items-center gap-3 px-3 py-2 rounded-lg text-sm font-medium text-gray-400 hover:text-red-600 hover:bg-gray-100 dark:hover:bg-gray-800 w-full transition-colors"
          title={collapsed ? t('nav.logout') : undefined}
        >
          <LogOut className="w-5 h-5 shrink-0" />
          {!collapsed && <span>{t('nav.logout')}</span>}
        </button>
        {info && !collapsed && (
          <div className="px-3 pt-1 text-[10px] text-gray-400 dark:text-gray-600 text-center">v{info.version.replace(/^v/i, '')}</div>
        )}
      </div>
    </aside>
  )
}
