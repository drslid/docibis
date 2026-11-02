// What the user has already read of the "Nouveautés" page: the
// newest note seen is kept in the browser, and the menu shows a dot until a
// newer one is read.
import { create } from 'zustand'

const KEY = 'docibis_last_seen_release'

function stored(): number {
  try {
    return Number(localStorage.getItem(KEY)) || 0
  } catch {
    return 0
  }
}

interface SeenState {
  seen: number
  markSeen: (id: number) => void
}

export const useReleaseSeen = create<SeenState>((set) => ({
  seen: stored(),
  markSeen: (id) => {
    try {
      localStorage.setItem(KEY, String(id))
    } catch {
      // Remembered until the page is closed.
    }
    set({ seen: id })
  },
}))
