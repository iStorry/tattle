import { useCallback, useState } from 'react'

// The dashboard is dark unless the owner picks the light look. The choice
// is kept in this browser only, so it never reaches the host.

export type Theme = 'dark' | 'light'

const KEY = 'tattle-theme'

function saved(): Theme {
  try {
    return localStorage.getItem(KEY) === 'light' ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

/** Applies the saved look before anything is drawn, so the page does not flash. */
export function applySavedTheme() {
  document.documentElement.dataset.theme = saved()
}

export function useTheme(): [Theme, () => void] {
  const [theme, setTheme] = useState<Theme>(saved)
  const toggle = useCallback(() => {
    setTheme((t) => {
      const next: Theme = t === 'dark' ? 'light' : 'dark'
      document.documentElement.dataset.theme = next
      try {
        localStorage.setItem(KEY, next)
      } catch {
        // Private browsing may refuse; the look still changes for this visit.
      }
      return next
    })
  }, [])
  return [theme, toggle]
}
