import { useEffect, useState } from 'preact/hooks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { finishLogin, startLogin, storedToken, tokenUsable, type AuthConfig } from '@/lib/oidc'
import { useT } from '@/lib/useT'
import { Separator } from '@/components/ui/separator'
import { ClusterDetail } from '@/pages/ClusterDetail'
import { ClusterList } from '@/pages/ClusterList'
import { GitOps } from '@/pages/GitOps'
import { MachineList } from '@/pages/MachineList'
import { NodeDetail } from '@/pages/NodeDetail'
import { OperationDetail } from '@/pages/OperationDetail'
import { OperationList } from '@/pages/OperationList'
import { PatchDetail } from '@/pages/PatchDetail'
import { PatchList } from '@/pages/PatchList'
import { Placeholder } from '@/pages/Placeholder'
import { LanguageSwitch } from '@/components/LanguageSwitch'
import { Link, matchRoute, usePath } from '@/router'
import type { Key } from '@/lib/i18n'
import { t as translateNow } from '@/lib/lang'

// Top-level nav. Cluster detail (/clusters/:namespace/:name), node detail
// (/nodes/:namespace/:name) and patch edit (/patches/:namespace/:name) are
// reached from a list row, not from here, same as there's no nav entry for
// an individual GitHub PR page.
const navRoutes: { path: string; label: Key }[] = [
  { path: '/', label: 'nav.clusters' },
  { path: '/machines', label: 'nav.machines' },
  { path: '/patches', label: 'nav.patches' },
  { path: '/operations', label: 'nav.operations' },
  { path: '/gitops', label: 'nav.gitops' },
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
      setError(e instanceof Error ? e.message : translateNow('app.loginFailed'))
      setState('error')
    })
  }, [])

  return { state, error }
}

export function App() {
  const t = useT()
  const path = usePath()
  const login = useLogin()

  if (login.state === 'loading') return <p class="p-8 text-sm text-muted-foreground">{t('app.signingIn')}</p>
  if (login.state === 'error') return <ErrorAlert title={t('app.signInFailed')} detail={login.error} />

  return (
    <div class="mx-auto min-h-screen max-w-5xl px-4">
      <header class="flex items-center gap-2 py-4">
        <span class="text-base font-semibold">Ichor</span>
        <nav class="ml-6 flex gap-1">
          {navRoutes.map((r) => (
            <Button key={r.path} asChild variant={path === r.path ? 'secondary' : 'ghost'} size="sm">
              <Link to={r.path}>{t(r.label)}</Link>
            </Button>
          ))}
        </nav>
        <div class="ml-auto">
          <LanguageSwitch />
        </div>
      </header>
      <Separator />
      {login.state === 'open' && (
        <Alert className="mt-4">
          <AlertTitle>{t('app.noLoginTitle')}</AlertTitle>
          <AlertDescription>{t('app.noLoginBody')}</AlertDescription>
        </Alert>
      )}
      <main>{renderRoute(path, t('common.notFound'))}</main>
    </div>
  )
}

function renderRoute(path: string, notFound: string) {
  if (path === '/') return <ClusterList />
  if (path === '/machines') return <MachineList />
  if (path === '/patches') return <PatchList />
  if (path === '/patches/new') return <PatchDetail />
  if (path === '/gitops') return <GitOps />
  if (path === '/operations') return <OperationList />

  const cluster = matchRoute('/clusters/:namespace/:name', path)
  if (cluster) return <ClusterDetail namespace={cluster.namespace} name={cluster.name} />

  const node = matchRoute('/nodes/:namespace/:name', path)
  if (node) return <NodeDetail namespace={node.namespace} name={node.name} />

  const operation = matchRoute('/operations/:namespace/:name', path)
  if (operation) return <OperationDetail namespace={operation.namespace} name={operation.name} />

  const patch = matchRoute('/patches/:namespace/:name', path)
  if (patch) return <PatchDetail namespace={patch.namespace} name={patch.name} />

  return <Placeholder title={notFound} />
}
