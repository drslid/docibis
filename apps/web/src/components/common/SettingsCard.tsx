import type { ReactNode } from 'react'

/** A titled block of settings, as in DodoRelay. */
export function SettingsCard({ title, actions, children }: { title: string; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-xl shadow-sm">
      <header className="flex flex-wrap items-center gap-2 px-5 py-3.5 border-b border-gray-200 dark:border-gray-700">
        <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100">{title}</h2>
        {actions && <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>}
      </header>
      <div className="p-5 text-sm text-gray-700 dark:text-gray-300">{children}</div>
    </section>
  )
}
