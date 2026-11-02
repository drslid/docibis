import { useMemo } from 'react'
import { ApiReferenceReact } from '@scalar/api-reference-react'
import '@scalar/api-reference-react/style.css'
import { useTheme } from '@/hooks/useTheme'

/** Interactive reference of the contract, with Scalar. Calls
 * tried from here stay on this origin; the key is typed by the reader. */
export default function ApiReferencePage() {
  const { theme } = useTheme()
  const configuration = useMemo(
    () => ({
      url: '/v1/openapi.json',
      servers: [{ url: window.location.origin, description: 'Cette instance' }],
      darkMode: theme === 'dark',
      hideDarkModeToggle: true,
      withDefaultFonts: false,
      // The bundled assistant would send the contract to a third party.
      agent: { disabled: true },
      showDeveloperTools: 'never' as const,
      defaultHttpClient: { targetKey: 'shell' as const, clientKey: 'curl' as const },
    }),
    [theme],
  )
  return (
    <div className="-m-4 md:-m-8 min-h-screen">
      <ApiReferenceReact configuration={configuration} />
    </div>
  )
}
