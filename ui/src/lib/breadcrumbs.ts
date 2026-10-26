import type { Key } from './i18n.ts'

// One crumb: a dictionary label or a literal object name; `to` is set on every
// crumb except the current page (the last one).
export type Crumb = { label?: Key; text?: string; to?: string }

const sections: Record<string, { label: Key; to: string }> = {
  clusters: { label: 'nav.clusters', to: '/' },
  nodes: { label: 'nav.machines', to: '/machines' },
  patches: { label: 'nav.patches', to: '/patches' },
  operations: { label: 'nav.operations', to: '/operations' },
}

const top: Record<string, Key> = {
  '/': 'nav.clusters',
  '/machines': 'nav.machines',
  '/patches': 'nav.patches',
  '/operations': 'nav.operations',
  '/gitops': 'nav.gitops',
}

// breadcrumbsFor maps a route path to the trail shown in the top bar. Pure so
// it is testable without a DOM; the routes mirror renderRoute in app.tsx.
export function breadcrumbsFor(path: string): Crumb[] {
  if (top[path]) return [{ label: top[path] }]
  if (path === '/patches/new') return [{ label: 'nav.patches', to: '/patches' }, { label: 'patch.newTitle' }]
  const parts = path.split('/').filter(Boolean)
  const section = sections[parts[0]]
  if (section && parts.length === 3) {
    return [{ label: section.label, to: section.to }, { text: decodeURIComponent(parts[2]) }]
  }
  return [{ label: 'common.notFound' }]
}
