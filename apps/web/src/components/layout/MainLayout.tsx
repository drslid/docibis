import { Suspense, useState } from 'react'
import { Outlet } from 'react-router'
import { Menu } from 'lucide-react'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { Sidebar } from './Sidebar'

export function MainLayout() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem('sidebarCollapsed') === '1'
    } catch {
      return false
    }
  })

  const toggleCollapsed = () => {
    setCollapsed((prev) => {
      const next = !prev
      try {
        localStorage.setItem('sidebarCollapsed', next ? '1' : '0')
      } catch {
        // The choice lasts until the page is closed.
      }
      return next
    })
  }

  return (
    <div className="flex min-h-screen">
      <button
        onClick={() => setMobileOpen(true)}
        className="fixed top-3 left-3 z-40 md:hidden p-2 rounded-lg bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 shadow-xs"
        aria-label="Menu"
      >
        <Menu className="w-5 h-5 text-gray-700 dark:text-gray-300" />
      </button>

      {mobileOpen && <div className="fixed inset-0 bg-black/30 z-40 md:hidden" onClick={() => setMobileOpen(false)} />}

      <Sidebar
        mobileOpen={mobileOpen}
        onMobileClose={() => setMobileOpen(false)}
        collapsed={collapsed}
        onToggleCollapse={toggleCollapsed}
      />
      <main className={`flex-1 min-w-0 ml-0 ${collapsed ? 'md:ml-16' : 'md:ml-60'} p-4 pt-14 md:pt-8 md:p-8 transition-[margin] duration-200`}>
        <Suspense fallback={<LoadingSpinner />}>
          <Outlet />
        </Suspense>
      </main>
    </div>
  )
}
