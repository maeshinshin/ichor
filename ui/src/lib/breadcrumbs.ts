import type { Key } from './i18n.ts'

// One crumb: a dictionary label or a literal object name; `to` is set on every
// crumb except the current page (the last one).
export type Crumb = { label?: Key; text?: string; to?: string }

const sections: Record<string, { label: Key; to: string }> = {
  nodes: { label: 'nodes.title', to: '/nodes' },
  config: { label: 'config.title', to: '/config' },
  operations: { label: 'nav.operations', to: '/operations' },
}

const top: Record<string, Key> = {
  '/': 'nav.overview',
  '/nodes': 'nodes.title',
  '/config': 'config.title',
  '/operations': 'nav.operations',
  '/cilium': 'nav.cilium',
  '/backups': 'nav.backups',
  '/users': 'nav.users',
  '/settings': 'nav.settings',
}

// breadcrumbsFor maps a route path to the trail shown in the top bar. Pure so
// it is testable without a DOM; titles maps "<section>/<name>" to a
// friendlier name a page published (a machine's hostname); the routes mirror renderRoute in app.tsx.
export function breadcrumbsFor(path: string, titles: Readonly<Record<string, string>> = {}): Crumb[] {
  if (top[path]) return [{ label: top[path] }]
  if (path === '/config/new') return [{ label: 'config.title', to: '/config' }, { label: 'patch.newTitle' }]
  if (path === '/nodes/add') return [{ label: 'nodes.title', to: '/nodes' }, { label: 'addNode.pageTitle' }]
  const parts = path.split('/').filter(Boolean)
  const section = sections[parts[0]]
  if (section && parts.length === 2) {
    const name = decodeURIComponent(parts[1])
    return [{ label: section.label, to: section.to }, { text: titles[`${parts[0]}/${name}`] || name }]
  }
  return [{ label: 'common.notFound' }]
}
