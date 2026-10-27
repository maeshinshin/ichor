// Route helpers for the single-cluster UI (docs/adr/0097). Pure, so they run under `node --test`.

// redirectFor maps an old multi-cluster URL to its new place, or null when the path is current
// (or unknown). Name segments are passed through as they came, still encoded.
export function redirectFor(path: string): string | null {
  const p = path.split('/').filter(Boolean)
  const [a, b, c, d] = p
  switch (a) {
    case 'clusters':
      if (p.length === 1 || p.length === 3) return '/'
      if (p.length === 4 && d === 'add-node') return '/nodes/add'
      return null
    case 'machines':
      return p.length === 1 ? '/nodes' : null
    case 'nodes':
      return p.length === 3 ? `/nodes/${c}` : null
    case 'operations':
      return p.length === 3 ? `/operations/${c}` : null
    case 'patches':
      if (p.length === 1) return '/config'
      if (p.length === 2 && b === 'new') return '/config/new'
      if (p.length === 3) return `/config/${c}`
      return null
    default:
      return null
  }
}
