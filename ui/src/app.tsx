import { useEffect, useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { finishLogin, startLogin, storedToken, tokenUsable, type AuthConfig } from '@/lib/oidc'
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

// useLogin gates the app on OIDC (docs/adr/0058). 'open' means ichor-ui has no
// login configured and writes with its own ServiceAccount, which the banner says.
function useLogin(): { state: 'loading' | 'open' | 'ready' | 'error'; error: string } {
  const [state, setState] = useState<'loading' | 'open' | 'ready' | 'error'>('loading')
  const [error, setError] = useState('')

  useEffect(() => {
    ;(async () => {
      const cfg = (await (await fetch('/auth/config')).json()) as AuthConfig
      if (!cfg.enabled) return setState('open')
      if (await finishLogin(cfg)) return setState('ready')
      if (tokenUsable(storedToken(), Date.now())) return setState('ready')
      await startLogin(cfg)
    })().catch((e) => {
      setError(e instanceof Error ? e.message : 'Login failed')
      setState('error')
    })
  }, [])

  return { state, error }
}

export function App() {
  const path = usePath()
  const login = useLogin()

  if (login.state === 'loading') return <p class="p-8 text-sm text-muted-foreground">Signing in...</p>
  if (login.state === 'error') return <ErrorAlert title="Sign-in failed" detail={login.error} />

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
      {login.state === 'open' && (
        <Alert className="mt-4">
          <AlertTitle>Sign-in is not configured</AlertTitle>
          <AlertDescription>
            Anyone who can reach this page can make changes, using ichor-ui's own permissions. Enable ichorUI.oidc in the chart so
            changes are made, and audited, as you.
          </AlertDescription>
        </Alert>
      )}
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
