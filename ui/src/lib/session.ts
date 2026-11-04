// The browser session of the ichor issuer (docs/adr/0094, amendment 2026-11-04). The ID token lives
// a few minutes; the refresh token (from the offline_access sign-in) lives the issuer's absolute
// refresh lifetime and is traded for new ID tokens shortly before they end, so a tab that is in use
// stays signed in for as long as the CLI would. The issuer rotates the refresh token on every use
// and revokes the family if a retired one is presented, so two requests must never present the same
// token: one refresh at a time per tab (single flight), and one tab per family (the claim below).
import { clearToken, idTokenClaims, storeTokens, storedRefresh, storedToken, tokenUsable } from './oidc.ts'

// ok: new tokens are stored. ended: the session is over (refused by the issuer, nothing to refresh
// with, or the family belongs to another tab) and the caller signs out. failed: try again later.
export type RefreshResult = 'ok' | 'ended' | 'failed'

const MARGIN_MS = 60_000
const MIN_REARM_MS = 10_000
const RETRY_MS = 15_000

// refreshDelayMs is how long to wait before refreshing idToken: a minute before it ends, or a third
// of its life before for a token shorter than three minutes. 0 means now.
export function refreshDelayMs(idToken: string | null, nowMs: number): number {
  const c = idToken ? idTokenClaims(idToken) : null
  if (typeof c?.exp !== 'number') return 0
  const life = typeof c.iat === 'number' ? (c.exp - c.iat) * 1000 : 3 * MARGIN_MS
  return Math.max(0, c.exp * 1000 - Math.min(MARGIN_MS, life / 3) - nowMs)
}

// A duplicated tab copies sessionStorage, so two tabs can hold one family and the first rotation
// would make the other present a retired token and revoke it for both. The tab that has a family
// holds a Web Lock named after it for as long as its page lives; a copy cannot get it and drops its
// tokens. Without Web Locks, or if the lock fails for another reason, the tab goes ahead.
type Locks = { request(name: string, opts: { signal?: AbortSignal }, cb: () => unknown): Promise<unknown> }
const defaultLocks = (): Locks | null => (globalThis.navigator as { locks?: Locks } | undefined)?.locks ?? null

export function claimFamily(family: string, locks: Locks | null = defaultLocks(), timeoutMs = 1500): Promise<boolean> {
  if (!locks) return Promise.resolve(true)
  return new Promise((resolve) => {
    // The page this one replaced (a reload) releases its lock as it goes; give it a moment.
    const ac = new AbortController()
    const timer = setTimeout(() => ac.abort(), timeoutMs)
    locks
      .request(`ichor-session-${family}`, { signal: ac.signal }, () => {
        clearTimeout(timer)
        resolve(true)
        return new Promise<never>(() => {}) // held until the page goes
      })
      .catch((e: { name?: string }) => {
        clearTimeout(timer)
        resolve(e?.name !== 'AbortError')
      })
  })
}

const claims = new Map<string, Promise<boolean>>()

export function claimSession(locks?: Locks | null, timeoutMs?: number): Promise<boolean> {
  const family = idTokenClaims(storedRefresh()?.token ?? '')?.fam
  if (typeof family !== 'string' || !family) return Promise.resolve(true)
  let p = claims.get(family)
  if (!p) claims.set(family, (p = claimFamily(family, locks, timeoutMs)))
  return p
}

let inflight: Promise<RefreshResult> | null = null

// refreshSession trades the refresh token for a new ID token and a new refresh token. Callers that
// arrive while one is running share its answer.
export function refreshSession(locks?: Locks | null, timeoutMs?: number): Promise<RefreshResult> {
  return (inflight ??= run(locks, timeoutMs).finally(() => (inflight = null)))
}

async function run(locks?: Locks | null, timeoutMs?: number): Promise<RefreshResult> {
  if (!(await claimSession(locks, timeoutMs))) {
    clearToken()
    return 'ended'
  }
  const r = storedRefresh()
  if (!r) return 'ended'
  let res: Response
  try {
    res = await fetch(r.endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: new URLSearchParams({ grant_type: 'refresh_token', refresh_token: r.token, client_id: r.clientId }),
    })
  } catch {
    return 'failed'
  }
  // A sign-in of this tab that landed meanwhile owns the storage now; this answer is stale.
  const current = storedRefresh()?.token === r.token
  if (res.status === 400 || res.status === 401 || res.status === 403) {
    if (!current) return 'failed'
    clearToken()
    return 'ended'
  }
  if (!res.ok) return 'failed'
  const body = await res.json().catch(() => null)
  if (typeof body?.id_token !== 'string' || !body.id_token || !current) return 'failed'
  storeTokens(body.id_token, { ...r, token: typeof body.refresh_token === 'string' && body.refresh_token ? body.refresh_token : r.token })
  return 'ok'
}

// ensureFresh refreshes first when the ID token is about to end, for a request that is about to
// carry it (a background tab's timer may not have run).
export async function ensureFresh(nowMs = Date.now()): Promise<void> {
  if (storedRefresh() && !tokenUsable(storedToken(), nowMs)) await refreshSession()
}

// startRefreshing keeps the tab signed in: it refreshes before each ID token ends and again when the
// tab becomes visible, and calls onEnded when the session is over. It returns the stop function.
export function startRefreshing(onEnded: () => void): () => void {
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  void claimSession() // a fresh sign-in's family is claimed at once, before any copy of this tab can use it
  const arm = (ms: number) => {
    clearTimeout(timer)
    if (!stopped) timer = setTimeout(tick, ms)
  }
  const tick = async () => {
    clearTimeout(timer)
    if (!storedRefresh()) return
    const r = await refreshSession()
    if (stopped) return
    if (r === 'ended') return onEnded()
    arm(r === 'ok' ? Math.max(MIN_REARM_MS, refreshDelayMs(storedToken(), Date.now())) : RETRY_MS)
  }
  const wake = () => {
    if (document.visibilityState === 'visible' && refreshDelayMs(storedToken(), Date.now()) === 0) void tick()
  }
  const hasDocument = typeof document !== 'undefined'
  if (hasDocument) document.addEventListener('visibilitychange', wake)
  arm(refreshDelayMs(storedToken(), Date.now()))
  return () => {
    stopped = true
    clearTimeout(timer)
    if (hasDocument) document.removeEventListener('visibilitychange', wake)
  }
}
