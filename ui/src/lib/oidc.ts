// OIDC login for the SPA: authorization code flow with PKCE
// as a public client, so there is no client secret anywhere in the browser or
// the chart. The ID token this ends with is sent to ichor-ui, which verifies
// it (signature, issuer, audience, expiry) -- nothing here is trusted; the
// checks below only make the page behave sensibly. The token is for Ichor,
// never a Kubernetes or Talos credential.

// A sign-in choice from /auth/config: 'oidc' is the external IdP,
// 'ichor' is ichor-ui's own issuer.
export type AuthProvider = { id: string; issuer: string; clientId: string }
// anonymous is set when no sign-in is configured: 'readonly' (reads only) or 'open' (--allow-unauthenticated).
export type AuthConfig = { enabled: boolean; providers: AuthProvider[]; anonymous?: 'readonly' | 'open' }

// The redirect URI each provider has registered: the issuer's UI client takes /auth/callback, the
// external IdP keeps the page root.
export function redirectUriFor(providerId: string, origin: string): string {
  return origin + (providerId === 'ichor' ? '/auth/callback' : '/')
}

export function base64url(bytes: Uint8Array): string {
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function randomString(): string {
  return base64url(crypto.getRandomValues(new Uint8Array(32)))
}

// RFC 7636 S256: BASE64URL(SHA256(verifier)).
export async function pkceChallenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64url(new Uint8Array(digest))
}

export function buildAuthUrl(
  authorizationEndpoint: string,
  p: { clientId: string; redirectUri: string; state: string; nonce: string; challenge: string; offline?: boolean },
): string {
  const u = new URL(authorizationEndpoint)
  u.searchParams.set('response_type', 'code')
  u.searchParams.set('client_id', p.clientId)
  u.searchParams.set('redirect_uri', p.redirectUri)
  // offline_access is what makes the ichor issuer return a refresh token (2026-11-04);
  // an external IdP is not asked for one.
  u.searchParams.set('scope', 'openid email profile groups' + (p.offline ? ' offline_access' : ''))
  u.searchParams.set('state', p.state)
  u.searchParams.set('nonce', p.nonce)
  u.searchParams.set('code_challenge', p.challenge)
  u.searchParams.set('code_challenge_method', 'S256')
  return u.toString()
}

// Claims of a JWT, without verifying it (the server does that).
export function idTokenClaims(token: string): Record<string, unknown> | null {
  try {
    const payload = token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
    return JSON.parse(atob(payload))
  } catch {
    return null
  }
}

export function tokenUsable(token: string | null, nowMs: number): boolean {
  if (!token) return false
  const exp = idTokenClaims(token)?.exp
  return typeof exp === 'number' && exp * 1000 > nowMs + 30_000
}

const KEY = 'ichor.idToken'
const REFRESH = 'ichor.refresh'
const PENDING = 'ichor.oidcPending'

// sessionStorage: gone when the tab closes, and a failure to use it just means
// logging in again.
export function storedToken(): string | null {
  try {
    return sessionStorage.getItem(KEY)
  } catch {
    return null
  }
}

// The refresh token of the ichor issuer's session, with where to present it (2026-11-04). Same storage as the ID token: tab-scoped and gone when the tab closes.
export type StoredRefresh = { token: string; endpoint: string; clientId: string }

export function storedRefresh(): StoredRefresh | null {
  try {
    const r = JSON.parse(sessionStorage.getItem(REFRESH) ?? 'null')
    return r && typeof r.token === 'string' && typeof r.endpoint === 'string' && typeof r.clientId === 'string' ? r : null
  } catch {
    return null
  }
}

export function storeTokens(idToken: string, refresh: StoredRefresh | null) {
  sessionStorage.setItem(KEY, idToken)
  if (refresh) sessionStorage.setItem(REFRESH, JSON.stringify(refresh))
  else sessionStorage.removeItem(REFRESH)
}

// clearToken ends this tab's sign-in: the ID token and the refresh token.
export function clearToken() {
  try {
    sessionStorage.removeItem(KEY)
    sessionStorage.removeItem(REFRESH)
  } catch {
    // nothing to clear
  }
}

export type Discovery = { authorization_endpoint: string; token_endpoint: string }

export async function discover(issuer: string): Promise<Discovery> {
  const res = await fetch(`${issuer.replace(/\/$/, '')}/.well-known/openid-configuration`)
  if (!res.ok) throw new Error('OIDC discovery failed')
  return res.json()
}

export async function startLogin(cfg: AuthProvider): Promise<never> {
  const d = await discover(cfg.issuer)
  const verifier = randomString()
  const state = randomString()
  const nonce = randomString()
  sessionStorage.setItem(PENDING, JSON.stringify({ verifier, state, nonce, provider: cfg.id }))
  window.location.assign(
    buildAuthUrl(d.authorization_endpoint, {
      clientId: cfg.clientId,
      redirectUri: redirectUriFor(cfg.id, window.location.origin),
      state,
      nonce,
      challenge: await pkceChallenge(verifier),
      offline: cfg.id === 'ichor',
    }),
  )
  return new Promise(() => {})
}

// finishLogin completes the redirect back from the IdP, if this page load is
// one. It returns true when it stored a token. A stale callback (no pending
// login, or one naming an unknown provider) returns false so the caller starts
// a fresh sign-in; a state mismatch or a failed exchange still throws.
export async function finishLogin(providers: AuthProvider[]): Promise<boolean> {
  const q = new URLSearchParams(window.location.search)
  const code = q.get('code')
  if (!code) return false

  const raw = sessionStorage.getItem(PENDING)
  sessionStorage.removeItem(PENDING)
  // Same as router.replace: the router only re-reads the path on popstate, so without the event it
  // keeps rendering the callback path (/auth/callback), which is no route.
  window.history.replaceState({}, '', '/')
  window.dispatchEvent(new Event('popstate'))
  // No pending login means this is a stale callback (a reopened or restored tab, the back button,
  // another tab: sessionStorage is per tab). It is not an attack, so start a fresh sign-in instead.
  if (!raw) return false
  const pending = JSON.parse(raw) as { verifier: string; state: string; nonce: string; provider?: string }
  if (q.get('state') !== pending.state) throw new Error('Login response did not match this session')
  const cfg = providers.find((p) => p.id === (pending.provider ?? 'oidc'))
  if (!cfg) return false

  await exchangeCode(await discover(cfg.issuer), cfg, code, pending.verifier, pending.nonce)
  return true
}

// exchangeCode trades an authorization code for the ID token at the token endpoint and stores it,
// after checking the nonce. Shared by the redirect callback and the in-page sign-in.
export async function exchangeCode(d: Discovery, cfg: AuthProvider, code: string, verifier: string, nonce: string) {
  const res = await fetch(d.token_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({
      grant_type: 'authorization_code',
      code,
      redirect_uri: redirectUriFor(cfg.id, window.location.origin),
      client_id: cfg.clientId,
      code_verifier: verifier,
    }),
  })
  if (!res.ok) throw new Error('Login failed at the identity provider')
  const body = await res.json()
  const idToken = body.id_token as string | undefined
  if (!idToken || idTokenClaims(idToken)?.nonce !== nonce) throw new Error('Login response did not match this session')
  const refresh = cfg.id === 'ichor' && typeof body.refresh_token === 'string' ? body.refresh_token : ''
  storeTokens(idToken, refresh ? { token: refresh, endpoint: d.token_endpoint, clientId: cfg.clientId } : null)
}

// signOut drops the ID token and the refresh token and reloads, which shows the sign-in screen. The
// issuer keeps no browser session of its own, so there is nothing to call.
export function signOut() {
  clearToken()
  window.location.assign('/')
}
