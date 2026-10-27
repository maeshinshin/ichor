import type { Key } from './i18n.ts'
import { titleKey } from './pageTitle.ts'

// One crumb: a dictionary label or a literal object name; `to` is set on every
// crumb except the current page (the last one).
export type Crumb = { label?: Key; text?: string; to?: string }

const sections: Record<string, { label: Key; to: string }> = {
  nodes: { label: 'nav.machines', to: '/nodes' },
  config: { label: 'nav.config', to: '/config' },
  operations: { label: 'nav.operations', to: '/operations' },
}

const top: Record<string, Key> = {
  '/': 'nav.overview',
  '/nodes': 'nav.machines',
  '/config': 'nav.config',
  '/operations': 'nav.operations',
  '/backups': 'nav.backups',
  '/gitops': 'nav.gitops',
  '/users': 'nav.users',
}

// breadcrumbsFor maps a route path to the trail shown in the top bar. Pure so
// it is testable without a DOM; titles maps titleKey(section, namespace, name) to a
// friendlier name a page published (a machine's hostname); the routes mirror renderRoute in app.tsx.
// All objects live in the cluster's namespace, which the caller passes in.
export function breadcrumbsFor(path: string, titles: Readonly<Record<string, string>> = {}, namespace = ''): Crumb[] {
  if (top[path]) return [{ label: top[path] }]
  if (path === '/config/new') return [{ label: 'nav.config', to: '/config' }, { label: 'patch.newTitle' }]
  if (path === '/nodes/add') return [{ label: 'nav.machines', to: '/nodes' }, { label: 'cluster.addNode' }]
  const parts = path.split('/').filter(Boolean)
  const section = sections[parts[0]]
  if (section && parts.length === 2) {
    const name = decodeURIComponent(parts[1])
    const title = titles[titleKey(parts[0], namespace, name)]
    return [{ label: section.label, to: section.to }, { text: title || name }]
  }
  return [{ label: 'common.notFound' }]
}
