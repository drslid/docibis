import type { MfaStep } from '@/api/generated/docibis'
import { CopyButton } from '@/components/common/CopyButton'
import { useI18n } from '@/i18n/useI18n'

/** The 6-digit code of the authenticator app. */
export function CodeField({ code, onCode }: { code: string; onCode: (code: string) => void }) {
  const { t } = useI18n()
  return (
    <label className="block">
      <span className="label">{t('mfa.code')}</span>
      <input
        type="text"
        inputMode="numeric"
        autoComplete="one-time-code"
        maxLength={6}
        value={code}
        onChange={(e) => onCode(e.target.value.replace(/\D/g, ''))}
        className="input text-center text-2xl tracking-[0.5em] font-semibold"
        placeholder="000000"
        required
        autoFocus
      />
    </label>
  )
}

/** QR code and manual key to enrol an authenticator app. */
export function TotpEnrollment({ step }: { step: MfaStep }) {
  const { t } = useI18n()
  return (
    <div className="flex flex-col items-center gap-3">
      <p className="text-sm text-gray-600 dark:text-gray-300 text-center">{t('mfa.scan')}</p>
      {step.qrPng && <img src={step.qrPng} alt={t('mfa.qr')} className="w-48 h-48 rounded-lg border border-gray-200 bg-white p-1" />}
      {step.secret && (
        <div className="w-full">
          <div className="label">{t('mfa.secret')}</div>
          <div className="flex gap-2 items-center">
            <code className="flex-1 rounded-lg bg-gray-100 dark:bg-gray-900 px-3 py-2 text-xs break-all">{step.secret}</code>
            <CopyButton text={step.secret} />
          </div>
        </div>
      )}
    </div>
  )
}
