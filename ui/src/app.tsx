import { useEffect, useState } from 'preact/hooks'
import { AppShell } from '@/components/AppShell'
import { ErrorAlert } from '@/components/ErrorAlert'
import { MustChangePassword } from '@/components/ChangePassword'
import type { WhoAmIResponse } from '@/gen/ichor/v1alpha1/user_pb'
import { userClient } from '@/lib/connect'
import { clearToken, finishLogin, signOut, startLogin, storedRefresh, storedToken, tokenUsable, type AuthConfig, type AuthProvider } from '@/lib/oidc'
import { claimSession, refreshSession, startRefreshing } from '@/lib/session'
import { t } from '@/lib/i18n'
import { AddNode } from '@/pages/AddNode'
import { MachineList } from '@/pages/MachineList'
import { Backups } from '@/pages/Backups'
import { Cilium } from '@/pages/Cilium'
import { Overview } from '@/pages/Overview'
import { NodeDetail } from '@/pages/NodeDetail'
import { OperationDetail } from '@/pages/OperationDetail'
import { OperationList } from '@/pages/OperationList'
import { PatchDetail } from '@/pages/PatchDetail'
import { PatchList } from '@/pages/PatchList'
import { Login } from '@/pages/Login'
import { Placeholder } from '@/pages/Placeholder'
import { SignIn } from '@/pages/SignIn'
import { passwordSignIn } from '@/lib/login'
import { Users } from '@/pages/Users'
import { Settings } from '@/pages/Settings'
import { matchRoute, replace, usePath } from '@/router'
import { redirectFor } from '@/lib/routes'
import { useCluster } from '@/lib/useCluster'
import { PageSkeleton } from '@/components/PageStates'
import { Alert, AlertDescription } from '@/components/ui/alert'

type LoginState = 'loading' | 'open' | 'choose' | 'ready' | 'error'

// useLogin gates the app on sign-in (docs/adr/0058, 0094). 'open' means ichor-ui has no login
// configured: ichor-ui then serves reads only, or writes with its own ServiceAccount when started with
// --allow-unauthenticated (docs/adr/0108); the banner says which. 'choose' shows the
// one sign-in card (password form and/or the external IdP button); an external IdP that is the only
// choice is entered directly, as before.
function useLogin(): { state: LoginState; error: string; providers: AuthProvider[]; anonymous: 'readonly' | 'open'; signedIn: () => void } {
  const [state, setState] = useState<LoginState>('loading')
  const [error, setError] = useState('')
  const [providers, setProviders] = useState<AuthProvider[]>([])
  const [anonymous, setAnonymous] = useState<'readonly' | 'open'>('open')

  useEffect(() => {
    ;(async () => {
      const cfg = (await (await fetch('/auth/config')).json()) as AuthConfig
      const list = cfg.providers ?? []
      setProviders(list)
      setAnonymous(cfg.anonymous ?? 'open')
      if (list.length === 0) return setState('open')
      if (await finishLogin(list)) return setState('ready')
      // A tab copied from another one (same sessionStorage) must not share its refresh token.
      if (!(await claimSession())) clearToken()
      // A reload after the ID token ended (a slept laptop, a restored tab) signs in with the refresh token.
      if (tokenUsable(storedToken(), Date.now()) || (storedRefresh() && (await refreshSession()) === 'ok')) return setState('ready')
      if (list.length === 1 && list[0].id === 'oidc') await startLogin(list[0])
      else setState('choose')
    })().catch((e) => {
      setError(e instanceof Error ? e.message : t('app.loginFailed'))
      setState('error')
    })
  }, [])

  return { state, error, providers, anonymous, signedIn: () => setState('ready') }
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
  // An old multi-cluster URL is replaced by its new place (docs/adr/0097).
  const to = redirectFor(path)
  useEffect(() => {
    if (to) replace(to)
  }, [to])
  if (to) return null
  // The login form is outside the gate: the person is not signed in yet.
  if (path === '/login') return <Login />
  return <Authenticated path={path} />
}

function Authenticated({ path }: { path: string }) {
  const login = useLogin()
  const signedIn = login.state === 'ready'
  const who = useWhoAmI(signedIn)
  // Keeps the ichor session going until its absolute lifetime; the end of it signs the tab out.
  useEffect(() => (signedIn ? startRefreshing(signOut) : undefined), [signedIn])
  const usersEnabled = login.providers.some((p) => p.id === 'ichor')

  if (login.state === 'loading' || (signedIn && !who.loaded)) return <p class="p-8 text-sm text-muted-foreground">{t('app.signingIn')}</p>
  if (login.state === 'error') return <ErrorAlert title={t('app.signInFailed')} detail={login.error} />
  if (login.state === 'choose') {
    const ichor = login.providers.find((p) => p.id === 'ichor')
    const oidc = login.providers.find((p) => p.id === 'oidc') ?? null
    return <SignIn ichor={!!ichor} oidc={oidc} signIn={async (f) => { await passwordSignIn(ichor!, f); login.signedIn() }} />
  }
  if (who.me?.mustChangePassword) return <MustChangePassword />

  return (
    <AppShell
      path={path}
      signInNotConfigured={login.state === 'open' ? login.anonymous : false}
      me={who.me}
      usersEnabled={usersEnabled}
    >
      <Routes path={path} usersEnabled={usersEnabled} />
    </AppShell>
  )
}

// Routes gates the cluster pages on the one cluster: none shows the empty state, several a
// warning. /users and /settings do not need a cluster.
function Routes({ path, usersEnabled }: { path: string; usersEnabled: boolean }) {
  const { cluster, count, loading, error } = useCluster()
  if (path === '/users' && usersEnabled) return <Users />
  if (path === '/settings') return <Settings />
  if (error && !cluster) return <ErrorAlert {...error} />
  if (loading) return <PageSkeleton label={t('shell.loadingCluster')} />
  if (!cluster) return <p class="p-8 text-sm text-muted-foreground">{t('shell.noCluster')}</p>
  return (
    <>
      {count > 1 && (
        <Alert className="mb-4">
          <AlertDescription>{t('shell.manyClusters', { name: cluster.ref?.name ?? '' })}</AlertDescription>
        </Alert>
      )}
      {renderRoute(path, t('common.notFound'))}
    </>
  )
}

function renderRoute(path: string, notFound: string) {
  if (path === '/') return <Overview />
  if (path === '/nodes') return <MachineList />
  if (path === '/nodes/add') return <AddNode />
  if (path === '/operations') return <OperationList />
  if (path === '/backups') return <Backups />
  if (path === '/config') return <PatchList />
  if (path === '/config/new') return <PatchDetail />
  if (path === '/cilium') return <Cilium />

  const node = matchRoute('/nodes/:name', path)
  if (node) return <NodeDetail name={node.name} />

  const operation = matchRoute('/operations/:name', path)
  if (operation) return <OperationDetail name={operation.name} />

  const patch = matchRoute('/config/:name', path)
  if (patch) return <PatchDetail name={patch.name} />

  return <Placeholder title={notFound} />
}
