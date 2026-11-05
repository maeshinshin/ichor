import { getCollection } from 'astro:content'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { repoUrl } from '../../plugins/docs.mjs'

export { repoUrl }

export const base = import.meta.env.BASE_URL.replace(/\/?$/, '/')

/** The release the docs describe: DOCS_VERSION, else the newest heading of CHANGELOG.md. */
export function docsVersion(): string {
  if (process.env.DOCS_VERSION) return process.env.DOCS_VERSION
  try {
    return /^## (v\S+)/m.exec(readFileSync(resolve(process.cwd(), '../CHANGELOG.md'), 'utf8'))?.[1] ?? 'dev'
  } catch {
    return 'dev'
  }
}

export type Doc = {
  route: string
  url: string
  title: string
  label: string
  order?: number
  description?: string
  editUrl: string
  body: string
  entry: Awaited<ReturnType<typeof getCollection<'docs'>>>[number]
}

export type NavNode = { label: string; href?: string; children?: NavNode[]; collapsed?: boolean }

const plain = (s: string) =>
  s
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[`*_]/g, '')
    .trim()

/** Section names and order for the directories the docs have today; any other directory is Title-Cased and listed after them. */
const dirs: Record<string, { label: string; order: number; newestFirst?: boolean }> = {
  guides: { label: 'Guides', order: 20 },
  concepts: { label: 'Concepts', order: 30 },
  design: { label: 'Design', order: 40 },
  reference: { label: 'Reference', order: 60 },
  cli: { label: 'CLI', order: 50 },
  crds: { label: 'Custom resources', order: 50 },
  adr: { label: 'Decisions', order: 90, newestFirst: true },
}
const titleCase = (s: string) => s.replace(/[-_]/g, ' ').replace(/^./, (c) => c.toUpperCase())

export async function getDocs(): Promise<Doc[]> {
  const entries = await getCollection('docs')
  return entries.map((entry) => {
    const route = entry.id === 'index' ? '' : entry.id
    const h1 = /^# (.+)$/m.exec((entry.body ?? '').replace(/^```[\s\S]*?^```/gm, ''))?.[1]
    const title = entry.data.title ?? (h1 ? plain(h1) : titleCase(route.split('/').pop() || 'Overview'))
    return {
      route,
      url: route ? `${base}${route}/` : base,
      title,
      label: entry.data.sidebar_label ?? title,
      order: entry.data.sidebar_order,
      description: entry.data.description,
      editUrl: `${repoUrl}/edit/main/docs/${entry.filePath?.split('/docs/').pop()}`,
      body: entry.body ?? '',
      entry,
    }
  })
}

type Tree = { page?: Doc; kids: Map<string, Tree> }

function tree(docs: Doc[]): Tree {
  const root: Tree = { kids: new Map() }
  for (const d of docs) {
    let n = root
    for (const seg of d.route.split('/').filter(Boolean)) {
      if (!n.kids.has(seg)) n.kids.set(seg, { kids: new Map() })
      n = n.kids.get(seg)!
    }
    n.page = d
  }
  return root
}

const count = (t: Tree): number => (t.page ? 1 : 0) + [...t.kids.values()].reduce((a, k) => a + count(k), 0)

function nodes(t: Tree, parent: string): NavNode[] {
  const newestFirst = dirs[parent]?.newestFirst
  const rank = ([seg, k]: [string, Tree]) =>
    k.page?.order ?? (k.kids.size ? (dirs[seg]?.order ?? 50) : 1000)
  const items = [...t.kids.entries()].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      (newestFirst ? b[0].localeCompare(a[0]) : (a[1].page?.label ?? a[0]).localeCompare(b[1].page?.label ?? b[0], 'en', { numeric: true })),
  )
  return items.map(([seg, k]) => {
    if (!k.kids.size) return { label: k.page!.label, href: k.page!.url }
    const children = nodes(k, seg)
    if (k.page) children.unshift({ label: k.page.label, href: k.page.url })
    return {
      label: k.page?.entry.data.sidebar_label ?? dirs[seg]?.label ?? titleCase(seg),
      children,
      collapsed: count(k) > 15,
    }
  })
}

/** Sidebar: the root page and loose root pages first, then one group per directory. */
export function buildNav(docs: Doc[]): NavNode[] {
  const t = tree(docs)
  const all = nodes(t, '')
  const loose = all.filter((n) => !n.children)
  const groups = all.filter((n) => n.children)
  const overview: NavNode[] = t.page ? [{ label: t.page.entry.data.sidebar_label ?? 'Overview', href: t.page.url }] : []
  const start = [...overview, ...loose]
  return [...(start.length ? [{ label: '', children: start }] : []), ...groups]
}

export function flatten(nav: NavNode[]): NavNode[] {
  return nav.flatMap((n) => (n.children ? flatten(n.children) : [n]))
}

export function crumbs(nav: NavNode[], href: string): NavNode[] {
  for (const n of nav) {
    if (n.href === href) return [n]
    const sub = n.children && crumbs(n.children, href)
    if (sub) return [n, ...sub]
  }
  return []
}
