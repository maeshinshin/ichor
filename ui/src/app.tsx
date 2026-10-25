import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { ClusterDetail } from '@/pages/ClusterDetail'
import { ClusterList } from '@/pages/ClusterList'
import { MachineList } from '@/pages/MachineList'
import { NodeDetail } from '@/pages/NodeDetail'
import { Placeholder } from '@/pages/Placeholder'
import { Link, matchRoute, usePath } from '@/router'

// Top-level nav. Cluster detail (/clusters/:namespace/:name) and node
// detail (/nodes/:namespace/:name) are reached from a list row, not from
// here, same as there's no nav entry for an individual GitHub PR page.
const navRoutes: { path: string; label: string }[] = [
  { path: '/', label: 'Clusters' },
  { path: '/machines', label: 'Machines' },
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
  if (path === '/gitops') return <Placeholder title="GitOps" />

  const cluster = matchRoute('/clusters/:namespace/:name', path)
  if (cluster) return <ClusterDetail namespace={cluster.namespace} name={cluster.name} />

  const node = matchRoute('/nodes/:namespace/:name', path)
  if (node) return <NodeDetail namespace={node.namespace} name={node.name} />

  return <Placeholder title="Not found" />
}
