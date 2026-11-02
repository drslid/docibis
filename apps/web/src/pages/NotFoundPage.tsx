import { Link } from 'react-router'
import { FileQuestion } from 'lucide-react'
import { EmptyState } from '@/components/common/EmptyState'
import { useI18n } from '@/i18n/useI18n'

export function NotFoundPage() {
  const { t } = useI18n()
  return (
    <EmptyState
      icon={<FileQuestion className="w-12 h-12" />}
      title={t('notFound.title')}
      description={t('notFound.text')}
      action={
        <Link to="/" className="btn-primary">
          {t('notFound.back')}
        </Link>
      }
    />
  )
}
