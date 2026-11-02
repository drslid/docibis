import { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { useI18n } from '@/i18n/useI18n'

export function CopyButton({ text, label, className = '' }: { text: string; label?: string; className?: string }) {
  const { t } = useI18n()
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={`btn-secondary text-sm inline-flex items-center gap-1.5 py-1.5 ${className}`}
      onClick={() => navigator.clipboard.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1500) })}
    >
      {done ? <Check className="w-4 h-4 text-green-600" /> : <Copy className="w-4 h-4" />} {done ? t('common.copied') : (label ?? t('common.copy'))}
    </button>
  )
}
