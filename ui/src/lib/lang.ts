// The language the screen is shown in (docs/adr/0064): the saved choice, else
// the browser's. Kept outside any component so plain functions (formatBytes,
// describeError) can read it; components re-render through useT (useT.ts).
// Every global is read inside try/catch or behind a typeof check, because this
// also runs under `node --test` and in private windows with storage blocked.
import { detectLang, isLang, translate, type Key, type Lang, type Params } from './i18n.ts'

export const storageKey = 'ichor-ui-lang'

let current: Lang | undefined
const listeners = new Set<() => void>()

function saved(): string | null {
  try {
    return globalThis.localStorage?.getItem(storageKey) ?? null
  } catch {
    return null
  }
}

export function getLang(): Lang {
  current ??= detectLang(saved(), globalThis.navigator?.languages)
  return current
}

export function setLang(lang: Lang): void {
  if (!isLang(lang) || lang === getLang()) return
  current = lang
  try {
    globalThis.localStorage?.setItem(storageKey, lang)
  } catch {
    // The choice just does not outlive this tab.
  }
  if (typeof document !== 'undefined') document.documentElement.lang = lang
  listeners.forEach((l) => l())
}

export function subscribeLang(l: () => void): () => void {
  listeners.add(l)
  return () => listeners.delete(l)
}

// t translates in the current language. Components use useT() so they redraw
// on a switch; plain functions call this.
export function t(key: Key, params?: Params): string {
  return translate(getLang(), key, params)
}
