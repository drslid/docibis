import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { useI18n } from '@/i18n/useI18n'

interface ModalProps {
  onClose: () => void
  title: string
  children: ReactNode
  size?: 'sm' | 'md' | 'lg' | 'xl'
  /** Actions kept visible under the scrolling content. */
  footer?: ReactNode
}

const WIDTH = { sm: 'max-w-md', md: 'max-w-lg', lg: 'max-w-2xl', xl: 'max-w-5xl' }

/** A dialog over the page: Escape closes it, Tab stays inside. */
export function Modal({ onClose, title, children, size = 'md', footer }: ModalProps) {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const onCloseRef = useRef(onClose)
  useEffect(() => {
    onCloseRef.current = onClose
  })

  useEffect(() => {
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = ''
    }
  }, [])

  useEffect(() => {
    const node = dialogRef.current
    if (!node) return
    const focusable = node.querySelectorAll<HTMLElement>('button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])')
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    const firstField = node.querySelector<HTMLElement>('input, select, textarea')
    ;(firstField ?? first)?.focus()
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onCloseRef.current()
        return
      }
      if (e.key !== 'Tab' || focusable.length === 0) return
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last?.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first?.focus()
      }
    }
    node.addEventListener('keydown', onKeyDown)
    return () => node.removeEventListener('keydown', onKeyDown)
  }, [])

  return (
    <div className="fixed inset-0 z-50 flex items-start sm:items-center justify-center p-4 bg-black/50" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={`w-full ${WIDTH[size]} max-h-[calc(100vh-2rem)] flex flex-col rounded-xl bg-white dark:bg-gray-800 shadow-2xl border border-gray-200 dark:border-gray-700`}
      >
        <div className="flex items-center gap-3 px-6 py-4 border-b border-gray-100 dark:border-gray-700">
          <h2 className="flex-1 text-lg font-semibold text-gray-900 dark:text-gray-100 truncate">{title}</h2>
          <button type="button" onClick={onClose} className="p-1 rounded-lg text-gray-400 hover:text-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700" aria-label={t('common.close')}>
            <X className="w-5 h-5" />
          </button>
        </div>
        <div className="px-6 py-5 overflow-y-auto">{children}</div>
        {footer && <div className="px-6 py-4 border-t border-gray-100 dark:border-gray-700">{footer}</div>}
      </div>
    </div>
  )
}
