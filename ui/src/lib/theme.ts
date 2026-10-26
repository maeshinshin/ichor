// Light/dark choice: the saved choice, else the system
// preference. The class on <html> is what index.css keys on. index.html has a
// copy of the first-paint part of this so a dark page does not flash light;
// keep the two in step.
export type Theme = 'light' | 'dark'

export const storageKey = 'ichor-ui-theme'

export function chooseTheme(saved: string | null | undefined, prefersDark: boolean): Theme {
  if (saved === 'light' || saved === 'dark') return saved
  return prefersDark ? 'dark' : 'light'
}

export function currentTheme(): Theme {
  let saved: string | null = null
  try {
    saved = localStorage.getItem(storageKey)
  } catch {
    // Storage blocked: follow the system.
  }
  return chooseTheme(saved, globalThis.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false)
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle('dark', theme === 'dark')
}

export function saveTheme(theme: Theme): void {
  try {
    localStorage.setItem(storageKey, theme)
  } catch {
    // The choice just does not outlive this tab.
  }
}
