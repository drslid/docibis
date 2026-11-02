import { useGetBranding, type Branding } from '@/api/generated/docibis'

export function companyLogoURL(b?: Branding): string | null {
  if (!b || b.companyLogo !== 'custom') return null
  return `/api/branding/logo?v=${b.version ?? ''}`
}

/** "DocIbis" in the brand colour, with the company logo under it when one is set.
 * `light` keeps light colours on a background that stays white in dark mode;
 * `company` replaces the saved logo (an image URL, or null for none). */
export function BrandLogo({ collapsed, light, company, className = '' }: { collapsed?: boolean; light?: boolean; company?: string | null; className?: string }) {
  const { data } = useGetBranding({ query: { staleTime: 300_000, retry: false } })
  const src = company !== undefined ? company : companyLogoURL(data)
  const custom = !!src
  if (collapsed) return <img src="/favicon.svg" alt="DocIbis" className="w-8 h-8 mx-auto" />
  return (
    <div className={className}>
      <div className="flex items-center justify-center gap-2">
        <img src="/favicon.svg" alt="" className="w-9 h-9 shrink-0" />
        <span className={`text-[1.7rem] leading-none tracking-tight text-gray-900 ${light ? '' : 'dark:text-gray-100'}`}>
          Doc<span className="font-bold text-brand-600">Ibis</span>
        </span>
      </div>
      {src && (
        <img
          src={src}
          alt=""
          className={`block w-[86%] max-h-12 mx-auto mt-2.5 object-contain ${custom && !light ? 'dark:bg-white/90 dark:rounded-md dark:p-1' : ''}`}
        />
      )}
    </div>
  )
}
