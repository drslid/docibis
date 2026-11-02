// Dark mode (zustand), kept in localStorage.
import { create } from 'zustand'

const STORAGE_KEY = 'docibis_theme'
type Theme = 'light' | 'dark'

function initialTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'dark' || stored === 'light') return stored
    if (window.matchMedia('(prefers-color-scheme: dark)').matches) return 'dark'
  } catch {
    // Storage or matchMedia unavailable: light theme.
  }
  return 'light'
}

function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle('dark', theme === 'dark')
}

applyTheme(initialTheme())

interface ThemeState {
  theme: Theme
  toggle: () => void
}

export const useTheme = create<ThemeState>((set, get) => ({
  theme: initialTheme(),
  toggle: () => {
    const next = get().theme === 'dark' ? 'light' : 'dark'
    try {
      localStorage.setItem(STORAGE_KEY, next)
    } catch {
      // The choice lasts until the page is closed.
    }
    applyTheme(next)
    set({ theme: next })
  },
}))
