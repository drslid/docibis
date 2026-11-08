import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { isLocale, useI18nStore } from '@/i18n/useI18n'
import '../index.css'
import { inWord, wordLanguage } from './office'
import { WordPane } from './WordPane'

// Word's own language, unless one was chosen in the console on this computer.
void inWord().then(() => {
  const language = wordLanguage().slice(0, 2)
  if (!useI18nStore.getState().chosen && isLocale(language)) useI18nStore.getState().setLocale(language, false)
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <WordPane />
  </StrictMode>,
)
