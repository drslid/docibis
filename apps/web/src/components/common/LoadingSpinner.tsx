import { Loader2 } from 'lucide-react'

export function LoadingSpinner({ size = 'md' }: { size?: 'sm' | 'md' | 'lg' }) {
  const sizeClass = { sm: 'w-4 h-4', md: 'w-8 h-8', lg: 'w-12 h-12' }[size]

  return (
    <div className="flex items-center justify-center p-8">
      <Loader2 className={`${sizeClass} animate-spin text-brand-500`} />
    </div>
  )
}
