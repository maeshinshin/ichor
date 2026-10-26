import { useEffect, useState } from 'preact/hooks'
import { AppShell } from '@/components/AppShell'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Toaster } from '@/components/ui/sonner'
import { finishLogin, startLogin, storedToken, tokenUsable, type AuthConfig } from '@/lib/oidc'
import { useT } from '@/lib/useT'
import { AddNode } from '@/pages/AddNode'
import { ClusterDetail } from '@/pages/ClusterDetail'
import { ClusterList } from '@/pages/ClusterList'
import { GitOps } from '@/pages/GitOps'
import { MachineList } from '@/pages/MachineList'
import { Overview } from '@/pages/Overview'
import { NodeDetail } from '@/pages/NodeDetail'
import { OperationDetail } from '@/pages/OperationDetail'
import { OperationList } from '@/pages/OperationList'
import { PatchDetail } from '@/pages/PatchDetail'
import { PatchList } from '@/pages/PatchList'
import { Placeholder } from '@/pages/Placeholder'
import { matchRoute, usePath } from '@/router'
import { t as translateNow } from '@/lib/i18n'

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
    <>
      <AppShell path={path} signInNotConfigured={login.state === 'open'}>
        {renderRoute(path, t('common.notFound'))}
      </AppShell>
      <Toaster />
    </>
  )
}

function renderRoute(path: string, notFound: string) {
  if (path === '/') return <Overview />
  if (path === '/clusters') return <ClusterList />
  if (path === '/machines') return <MachineList />
  if (path === '/patches') return <PatchList />
  if (path === '/patches/new') return <PatchDetail />
  if (path === '/gitops') return <GitOps />
  if (path === '/operations') return <OperationList />

  const addNode = matchRoute('/clusters/:namespace/:name/add-node', path)
  if (addNode) return <AddNode namespace={addNode.namespace} name={addNode.name} />

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
