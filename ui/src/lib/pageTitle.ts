// A page that knows a friendlier name for its object than the one in the URL
// (a machine's hostname instead of its UUID) publishes it here; the top bar
// shows it in the breadcrumb. Keys are "<section>/<name>".

let titles: Record<string, string> = {}
const listeners = new Set<() => void>()

export function titleKey(section: string, ...rest: string[]): string {
  return [section, ...rest].join('/')
}

export function setPageTitle(key: string, title: string | undefined): void {
  const next = { ...titles }
  if (title) next[key] = title
  else delete next[key]
  titles = next
  listeners.forEach((l) => l())
}

export function getPageTitles(): Readonly<Record<string, string>> {
  return titles
}

export function subscribePageTitles(fn: () => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}
