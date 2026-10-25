import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { ClusterDetail } from '@/pages/ClusterDetail'
import { ClusterList } from '@/pages/ClusterList'
import { MachineList } from '@/pages/MachineList'
import { NodeDetail } from '@/pages/NodeDetail'
import { PatchDetail } from '@/pages/PatchDetail'
import { PatchList } from '@/pages/PatchList'
import { Placeholder } from '@/pages/Placeholder'
import { Link, matchRoute, usePath } from '@/router'

// Top-level nav. Cluster detail (/clusters/:namespace/:name), node detail
// (/nodes/:namespace/:name) and patch edit (/patches/:namespace/:name) are
// reached from a list row, not from here, same as there's no nav entry for
// an individual GitHub PR page.
const navRoutes: { path: string; label: string }[] = [
  { path: '/', label: 'Clusters' },
  { path: '/machines', label: 'Machines' },
  { path: '/patches', label: 'Patches' },
  { path: '/gitops', label: 'GitOps' },
]

export function App() {
  const path = usePath()

  return (
    <div class="mx-auto min-h-screen max-w-5xl px-4">
      <header class="flex items-center gap-2 py-4">
        <span class="text-base font-semibold">Ichor</span>
        <nav class="ml-6 flex gap-1">
          {navRoutes.map((r) => (
            <Button key={r.path} asChild variant={path === r.path ? 'secondary' : 'ghost'} size="sm">
              <Link to={r.path}>{r.label}</Link>
            </Button>
          ))}
        </nav>
      </header>
      <Separator />
      <main>{renderRoute(path)}</main>
    </div>
  )
}

function renderRoute(path: string) {
  if (path === '/') return <ClusterList />
  if (path === '/machines') return <MachineList />
  if (path === '/patches') return <PatchList />
  if (path === '/patches/new') return <PatchDetail />
  if (path === '/gitops') return <Placeholder title="GitOps" />

  const cluster = matchRoute('/clusters/:namespace/:name', path)
  if (cluster) return <ClusterDetail namespace={cluster.namespace} name={cluster.name} />

  const node = matchRoute('/nodes/:namespace/:name', path)
  if (node) return <NodeDetail namespace={node.namespace} name={node.name} />

  const patch = matchRoute('/patches/:namespace/:name', path)
  if (patch) return <PatchDetail namespace={patch.namespace} name={patch.name} />

  return <Placeholder title="Not found" />
}
