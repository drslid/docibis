import { lazy, useEffect } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createBrowserRouter, Navigate } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { Loader2 } from 'lucide-react'
import { useGetSession } from '@/api/generated/docibis'
import { LoadingSpinner } from '@/components/common/LoadingSpinner'
import { MainLayout } from '@/components/layout/MainLayout'
import { RequireRole } from '@/hooks/useMe'
import { useI18n, useI18nStore } from '@/i18n/useI18n'
import { LoginPage, SetupPage } from '@/pages/LoginPage'
import { NotFoundPage } from '@/pages/NotFoundPage'

// Each page is loaded when first opened: the sign-in page stays light.
const page = <K extends string>(load: () => Promise<Record<K, React.ComponentType>>, name: K) =>
  lazy(() => load().then((m) => ({ default: m[name] })))
const ApiKeysPage = page(() => import('@/pages/ApiKeysPage'), 'ApiKeysPage')
const AuditPage = page(() => import('@/pages/AuditPage'), 'AuditPage')
const ConfigPage = page(() => import('@/pages/ConfigPage'), 'ConfigPage')
const DocumentsPage = page(() => import('@/pages/DocumentsPage'), 'DocumentsPage')
const IntegrationsPage = page(() => import('@/pages/IntegrationsPage'), 'IntegrationsPage')
const ProfilePage = page(() => import('@/pages/ProfilePage'), 'ProfilePage')
const TryPage = page(() => import('@/pages/TryPage'), 'TryPage')
const UsersPage = page(() => import('@/pages/UsersPage'), 'UsersPage')
const LibraryPage = page(() => import('@/pages/LibraryPage'), 'LibraryPage')
const TemplatePage = page(() => import('@/pages/LibraryPage'), 'TemplatePage')
const BundlesPage = page(() => import('@/pages/BundlesPage'), 'BundlesPage')
const ReleasesPage = page(() => import('@/pages/ReleasesPage'), 'ReleasesPage')
const MonitoringPage = page(() => import('@/pages/MonitoringPage'), 'MonitoringPage')
const HelpHome = page(() => import('@/pages/help/HelpPages'), 'HelpHome')
const HelpPage = page(() => import('@/pages/help/HelpPages'), 'HelpPage')
const ApiReferencePage = lazy(() => import('@/pages/help/ApiReferencePage'))
const DashboardPage = lazy(() => import('@/pages/DashboardPage'))

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 30_000, refetchOnWindowFocus: false } },
})

const router = createBrowserRouter([
  {
    element: <MainLayout />,
    children: [
      { index: true, element: <DashboardPage /> },
      { path: 'modeles', element: <LibraryPage /> },
      { path: 'modeles/:id', element: <TemplatePage /> },
      { path: 'liasses', element: <BundlesPage /> },
      { path: 'nouveautes', element: <ReleasesPage /> },
      { path: 'essai', element: <RequireRole role="editor"><TryPage /></RequireRole> },
      { path: 'documents', element: <DocumentsPage /> },
      { path: 'integrations', element: <IntegrationsPage /> },
      { path: 'aide', element: <HelpHome /> },
      { path: 'aide/:slug', element: <HelpPage /> },
      { path: 'docs', element: <ApiReferencePage /> },
      { path: 'developpeurs', element: <Navigate to="/aide/api" replace /> },
      { path: 'monitoring', element: <RequireRole role="admin"><MonitoringPage /></RequireRole> },
      { path: 'journal', element: <RequireRole role="admin"><AuditPage /></RequireRole> },
      { path: 'utilisateurs', element: <RequireRole role="admin"><UsersPage /></RequireRole> },
      { path: 'cles-api', element: <RequireRole role="admin"><ApiKeysPage /></RequireRole> },
      { path: 'configuration/:section?', element: <RequireRole role="admin"><ConfigPage /></RequireRole> },
      { path: 'profil', element: <ProfilePage /> },
      { path: '*', element: <NotFoundPage /> },
    ],
  },
])

// Shows the first administrator form, the sign-in page or the console.
function Gate() {
  const { data, error, isPending } = useGetSession({
    query: { retry: false, refetchInterval: (q) => (q.state.error?.status === 503 ? 3_000 : false) },
  })
  const { t } = useI18n()
  const language = data?.user?.language

  // The account language applies unless this browser chose another one.
  useEffect(() => {
    const { chosen, locale, setLocale } = useI18nStore.getState()
    if (language && !chosen && language !== locale) setLocale(language, false)
  }, [language])

  if (isPending) return <LoadingSpinner />
  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center gap-3 text-gray-600">
        {error.status === 503 ? <><Loader2 className="w-5 h-5 animate-spin text-brand-500" /> {t('home.starting')}</> : <>{t('common.unavailable', { message: error.message })}</>}
      </div>
    )
  }
  if (data.setupRequired) return <SetupPage />
  if (!data.authenticated) return <LoginPage />
  return <RouterProvider router={router} />
}

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Gate />
    </QueryClientProvider>
  )
}
