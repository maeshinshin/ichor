import { useEffect, useState } from 'preact/hooks'
import { AppShell } from '@/components/AppShell'
import { ErrorAlert } from '@/components/ErrorAlert'
import { MustChangePassword } from '@/components/ChangePassword'
import type { WhoAmIResponse } from '@/gen/ichor/v1alpha1/user_pb'
import { userClient } from '@/lib/connect'
import { finishLogin, providersOf, startLogin, storedToken, tokenUsable, type AuthConfig, type AuthProvider } from '@/lib/oidc'
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
import { Login } from '@/pages/Login'
import { Placeholder } from '@/pages/Placeholder'
import { SignIn } from '@/pages/SignIn'
import { Users } from '@/pages/Users'
import { matchRoute, usePath } from '@/router'
import { t as translateNow } from '@/lib/i18n'

type LoginState = 'loading' | 'open' | 'choose' | 'ready' | 'error'

// useLogin gates the app on sign-in (docs/adr/0058, 0094). 'open' means ichor-ui has no login
// configured and writes with its own ServiceAccount, which the banner says. 'choose' shows the
// sign-in screen; an external IdP that is the only choice is entered directly, as before.
function useLogin(): { state: LoginState; error: string; providers: AuthProvider[] } {
  const [state, setState] = useState<LoginState>('loading')
  const [error, setError] = useState('')
  const [providers, setProviders] = useState<AuthProvider[]>([])

  useEffect(() => {
    ;(async () => {
      const cfg = (await (await fetch('/auth/config')).json()) as AuthConfig
      const list = providersOf(cfg)
      setProviders(list)
      if (list.length === 0) return setState('open')
      if (await finishLogin(list)) return setState('ready')
      if (tokenUsable(storedToken(), Date.now())) return setState('ready')
      if (list.length === 1 && list[0].id === 'oidc') await startLogin(list[0])
      else setState('choose')
    })().catch((e) => {
      setError(e instanceof Error ? e.message : translateNow('app.loginFailed'))
      setState('error')
    })
  }, [])

  return { state, error, providers }
}

// useWhoAmI reads who the backend thinks the caller is, once signed in. A failure is not fatal:
// the app still works, it just has no account menu.
function useWhoAmI(signedIn: boolean): { me: WhoAmIResponse | null; loaded: boolean; reload: () => void } {
  const [me, setMe] = useState<WhoAmIResponse | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [n, setN] = useState(0)

  useEffect(() => {
    if (!signedIn) return
    userClient.whoAmI({}).then(
      (res) => {
        setMe(res)
        setLoaded(true)
      },
      () => setLoaded(true),
    )
  }, [signedIn, n])

  return { me, loaded, reload: () => setN((v) => v + 1) }
}

export function App() {
  const path = usePath()
  // The login form is outside the gate: the person is not signed in yet.
  if (path === '/login') return <Login />
  return <Authenticated path={path} />
}

function Authenticated({ path }: { path: string }) {
  const t = useT()
  const login = useLogin()
  const signedIn = login.state === 'ready'
  const who = useWhoAmI(signedIn)

  if (login.state === 'loading' || (signedIn && !who.loaded)) return <p class="p-8 text-sm text-muted-foreground">{t('app.signingIn')}</p>
  if (login.state === 'error') return <ErrorAlert title={t('app.signInFailed')} detail={login.error} />
  if (login.state === 'choose') return <SignIn providers={login.providers} />
  if (who.me?.mustChangePassword) return <MustChangePassword onDone={who.reload} />

  return (
    <AppShell
      path={path}
      signInNotConfigured={login.state === 'open'}
      me={who.me}
      usersEnabled={login.providers.some((p) => p.id === 'ichor')}
    >
      {renderRoute(path, t('common.notFound'), login.providers.some((p) => p.id === 'ichor'))}
    </AppShell>
  )
}

function renderRoute(path: string, notFound: string, usersEnabled: boolean) {
  if (path === '/') return <Overview />
  if (path === '/clusters') return <ClusterList />
  if (path === '/machines') return <MachineList />
  if (path === '/patches') return <PatchList />
  if (path === '/patches/new') return <PatchDetail />
  if (path === '/gitops') return <GitOps />
  if (path === '/operations') return <OperationList />
  if (path === '/users' && usersEnabled) return <Users />

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
