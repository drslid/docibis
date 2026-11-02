import { useEffect, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Search, X } from 'lucide-react'
import { useI18n } from '@/i18n/useI18n'

/** The value, once it stopped changing for ms milliseconds (search as you type). */
export function useDebounced<T>(value: T, ms: number): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setSettled(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return settled
}

export function SearchBox({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder: string }) {
  return (
    <div className="relative flex-1 min-w-[14rem]">
      <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
      <input className="input pl-9" type="search" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={placeholder} />
    </div>
  )
}

export function Select({ value, onChange, label, children }: { value: string; onChange: (v: string) => void; label: string; children: ReactNode }) {
  return (
    <select className="input w-auto pr-8" value={value} onChange={(e) => onChange(e.target.value)} aria-label={label}>
      {children}
    </select>
  )
}

export function ResetFilters({ show, onClick }: { show: boolean; onClick: () => void }) {
  const { t } = useI18n()
  if (!show) return null
  return (
    <button type="button" className="btn-ghost inline-flex items-center gap-1.5 text-sm" onClick={onClick}>
      <X className="w-4 h-4" /> {t('common.resetFilters')}
    </button>
  )
}

export function Pager({ offset, limit, total, onChange }: { offset: number; limit: number; total: number; onChange: (offset: number) => void }) {
  const { t } = useI18n()
  if (total === 0) return null
  return (
    <div className="flex items-center justify-between gap-4 pt-4 text-sm text-gray-500 dark:text-gray-400">
      <span>{t('common.range', { from: offset + 1, to: Math.min(offset + limit, total), total })}</span>
      {total > limit && (
        <div className="flex gap-2">
          <button type="button" className="btn-ghost inline-flex items-center gap-1 px-3 py-1.5" disabled={offset === 0} onClick={() => onChange(Math.max(0, offset - limit))}>
            <ChevronLeft className="w-4 h-4" /> {t('common.previous')}
          </button>
          <button type="button" className="btn-ghost inline-flex items-center gap-1 px-3 py-1.5" disabled={offset + limit >= total} onClick={() => onChange(offset + limit)}>
            {t('common.next')} <ChevronRight className="w-4 h-4" />
          </button>
        </div>
      )}
    </div>
  )
}

/** Table frame shared by the lists. */
export function Table({ head, children }: { head: ReactNode; children: ReactNode }) {
  return (
    <div className="overflow-x-auto -mx-6">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-left text-xs uppercase tracking-wide text-gray-500 dark:text-gray-400 border-b border-gray-200 dark:border-gray-700">{head}</tr>
        </thead>
        <tbody className="divide-y divide-gray-100 dark:divide-gray-700/60">{children}</tbody>
      </table>
    </div>
  )
}

export const th = 'px-4 first:pl-6 last:pr-6 py-2 font-semibold whitespace-nowrap'
export const td = 'px-4 first:pl-6 last:pr-6 py-2.5 align-top'
