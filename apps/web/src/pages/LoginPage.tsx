import { useState, type FormEvent, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getGetSessionQueryKey, useLogin, useLoginMfa, useSetup, type MfaStep } from '@/api/generated/docibis'
import { BrandLogo } from '@/components/layout/BrandLogo'
import { CodeField, TotpEnrollment } from '@/components/profile/TotpEnrollment'
import { LanguagePicker } from '@/i18n/LanguagePicker'
import { useI18n } from '@/i18n/useI18n'

function Shell({ children, onSubmit }: { children: ReactNode; onSubmit: (e: FormEvent) => void }) {
  return (
    <div className="min-h-screen relative overflow-hidden bg-[radial-gradient(circle_at_top,_rgba(255,255,255,0.12),transparent_30%),linear-gradient(135deg,#0b1724_0%,#0f766e_50%,#14b8a6_100%)] text-slate-900">
      <div className="absolute inset-0 bg-[linear-gradient(120deg,rgba(255,255,255,0.06),transparent_36%,rgba(0,0,0,0.12))]" />
      <div className="absolute top-4 right-4 z-10">
        <LanguagePicker />
      </div>
      <div className="relative min-h-screen flex items-center justify-center px-4 py-10">
        <div className="w-full max-w-lg">
          <form className="rounded-[2rem] border border-white/70 bg-white shadow-[0_28px_90px_rgba(0,0,0,0.28)] p-7 sm:p-10 space-y-5 text-slate-900" onSubmit={onSubmit}>
            <div className="mb-2">
              <BrandLogo light className="w-full max-w-[320px] mx-auto" />
            </div>
            {children}
          </form>
        </div>
      </div>
    </div>
  )
}

function Field({ label, type = 'text', value, onChange, autoComplete }: { label: string; type?: string; value: string; onChange: (v: string) => void; autoComplete: string }) {
  return (
    <label className="block">
      <span className="label">{label}</span>
      <input className="input" type={type} value={value} onChange={(e) => onChange(e.target.value)} autoComplete={autoComplete} required />
    </label>
  )
}

function Error({ message }: { message?: string }) {
  if (!message) return null
  return <p className="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-4 py-2" role="alert">{message}</p>
}

export function LoginPage() {
  const queryClient = useQueryClient()
  const { t } = useI18n()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [step, setStep] = useState<MfaStep | null>(null)
  const [code, setCode] = useState('')
  const signedIn = () => queryClient.invalidateQueries({ queryKey: getGetSessionQueryKey() })
  const login = useLogin({
    mutation: {
      onSuccess: (data) => {
        if ('step' in data) {
          setCode('')
          setStep(data)
        } else void signedIn()
      },
    },
  })
  const mfa = useLoginMfa({
    mutation: {
      onSuccess: () => void signedIn(),
      onError: (e) => {
        // An expired sign-in starts again from the password.
        if (e.status === 410) setStep(null)
      },
    },
  })

  if (step) {
    return (
      <Shell onSubmit={(e) => { e.preventDefault(); mfa.mutate({ data: { ticket: step.ticket, code } }) }}>
        <div className="text-center">
          <h1 className="text-xl font-semibold">{step.step === 'enroll' ? t('mfa.enrollTitle') : t('mfa.title')}</h1>
          <p className="text-sm text-gray-500 mt-1">{step.step === 'enroll' ? t('mfa.enrollHelp') : t('mfa.help')}</p>
        </div>
        {step.step === 'enroll' && <TotpEnrollment step={step} />}
        <CodeField code={code} onCode={setCode} />
        <Error message={mfa.error?.message} />
        <button type="submit" className="btn-primary w-full py-3" disabled={mfa.isPending || code.length !== 6}>
          {step.step === 'enroll' ? t('mfa.enrollSubmit') : t('mfa.submit')}
        </button>
        <button type="button" className="btn-ghost w-full text-sm" onClick={() => { setStep(null); setPassword('') }}>
          {t('mfa.back')}
        </button>
      </Shell>
    )
  }

  return (
    <Shell onSubmit={(e) => { e.preventDefault(); login.mutate({ data: { username, password } }) }}>
      <Field label={t('login.username')} value={username} onChange={setUsername} autoComplete="username" />
      <Field label={t('login.password')} type="password" value={password} onChange={setPassword} autoComplete="current-password" />
      <Error message={login.error?.message ?? (mfa.error?.status === 410 ? mfa.error.message : undefined)} />
      <button type="submit" className="btn-primary w-full py-3" disabled={login.isPending}>
        {t('login.submit')}
      </button>
    </Shell>
  )
}

export function SetupPage() {
  const queryClient = useQueryClient()
  const { t } = useI18n()
  const [setupCode, setSetupCode] = useState('')
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const setup = useSetup({ mutation: { onSuccess: () => queryClient.invalidateQueries({ queryKey: getGetSessionQueryKey() }) } })
  const mismatch = confirm !== '' && confirm !== password

  return (
    <Shell onSubmit={(e) => { e.preventDefault(); if (!mismatch) setup.mutate({ data: { setupCode, username, password } }) }}>
      <div className="text-center">
        <h1 className="text-xl font-semibold">{t('setup.title')}</h1>
        <p className="text-sm text-gray-500 mt-1">{t('setup.help')}</p>
      </div>
      <Field label={t('setup.code')} value={setupCode} onChange={setSetupCode} autoComplete="one-time-code" />
      <Field label={t('login.username')} value={username} onChange={setUsername} autoComplete="username" />
      <Field label={t('setup.password')} type="password" value={password} onChange={setPassword} autoComplete="new-password" />
      <Field label={t('setup.confirm')} type="password" value={confirm} onChange={setConfirm} autoComplete="new-password" />
      <Error message={mismatch ? t('setup.mismatch') : setup.error?.message} />
      <button type="submit" className="btn-primary w-full py-3" disabled={setup.isPending || mismatch}>
        {t('setup.submit')}
      </button>
    </Shell>
  )
}
