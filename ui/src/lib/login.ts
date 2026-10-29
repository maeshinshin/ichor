// The ichor sign-in form (docs/adr/0094): the issuer's authorize endpoint sends the browser to
// /login?ticket=..., this page posts {ticket, username, password} to /oidc/login and follows the
// redirect_uri it answers. Pure helpers only; the password never reaches a log, a URL or storage.
import type { Key } from './i18n.ts'
import { buildAuthUrl, discover, exchangeCode, pkceChallenge, randomString, redirectUriFor, type AuthProvider, type Discovery } from './oidc.ts'

export function ticketFromSearch(search: string): string {
  return new URLSearchParams(search).get('ticket') ?? ''
}

export function loginReady(f: { username: string; password: string }): boolean {
  return f.username.trim() !== '' && f.password !== ''
}

export function loginBody(ticket: string, f: { username: string; password: string }): string {
  return JSON.stringify({ ticket, username: f.username.trim(), password: f.password })
}

// loginErrorKey is the message for a refused login. A wrong password, an unknown user and a
// disabled user all look the same on purpose; only an expired ticket and the back-off differ.
export function loginErrorKey(status: number): Key {
  if (status === 400) return 'login.expired'
  if (status === 429) return 'login.rateLimited'
  if (status >= 500) return 'login.unavailable'
  return 'login.invalid'
}

// redirectTarget reads the answer of /oidc/login. Only an http(s) URL is followed.
export function redirectTarget(body: unknown): string | null {
  const u = (body as { redirect_uri?: unknown } | null)?.redirect_uri
  if (typeof u !== 'string') return null
  try {
    const p = new URL(u)
    return p.protocol === 'https:' || p.protocol === 'http:' ? p.toString() : null
  } catch {
    return null
  }
}

// The in-page sign-in (docs/adr/0094 amendment): no navigation. The browser asks the authorize
// endpoint for a ticket as JSON, posts the credentials, and exchanges the returned code with fetch,
// with the same PKCE, state and nonce checks as the redirect flow.
export class LoginError extends Error {
  key: Key
  constructor(key: Key) {
    super(key)
    this.key = key
  }
}

export type SignInSections = { ichor: AuthProvider | null; oidc: AuthProvider | null }

// signInSections says what the one sign-in card shows: the password form for the ichor provider and
// the identity-provider button for the external one.
export function signInSections(providers: AuthProvider[]): SignInSections {
  return {
    ichor: providers.find((p) => p.id === 'ichor') ?? null,
    oidc: providers.find((p) => p.id === 'oidc') ?? null,
  }
}

export async function passwordSignIn(cfg: AuthProvider, f: { username: string; password: string }): Promise<void> {
  const redirectUri = redirectUriFor(cfg.id, window.location.origin)
  const verifier = randomString()
  const state = randomString()
  const nonce = randomString()
  let d: Discovery
  let ticket: unknown
  try {
    d = await discover(cfg.issuer)
    const url = buildAuthUrl(d.authorization_endpoint, { clientId: cfg.clientId, redirectUri, state, nonce, challenge: await pkceChallenge(verifier) })
    const res = await fetch(url, { headers: { Accept: 'application/json' } })
    ticket = res.ok ? (await res.json()).ticket : ''
  } catch {
    throw new LoginError('login.unavailable')
  }
  if (typeof ticket !== 'string' || !ticket) throw new LoginError('login.unavailable')

  let res: Response
  try {
    res = await fetch('/oidc/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: loginBody(ticket, f) })
  } catch {
    throw new LoginError('login.unavailable')
  }
  if (!res.ok) throw new LoginError(loginErrorKey(res.status))
  const target = redirectTarget(await res.json().catch(() => null))
  const back = target ? new URL(target) : null
  if (!back || back.origin + back.pathname !== redirectUri || back.searchParams.get('state') !== state) throw new LoginError('login.unavailable')
  const code = back.searchParams.get('code')
  if (!code) throw new LoginError('login.unavailable')
  try {
    await exchangeCode(d, cfg, code, verifier, nonce)
  } catch {
    throw new LoginError('login.unavailable')
  }
}
