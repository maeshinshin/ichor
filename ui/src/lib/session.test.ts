import assert from 'node:assert/strict'
import { mock, test } from 'node:test'
import { base64url, storedToken } from './oidc.ts'
import { claimFamily, claimSession, ensureFresh, refreshDelayMs, refreshSession, startRefreshing } from './session.ts'

const jwt = (claims: object) => `h.${base64url(new TextEncoder().encode(JSON.stringify(claims)))}.s`
const NOW = 1_800_000_000_000
const idAt = (iat: number, ttlSec: number) => jwt({ iat: iat / 1000, exp: iat / 1000 + ttlSec })
const refreshJwt = (jti: string, fam = 'fam1') => jwt({ jti, fam })
const ENDPOINT = 'https://ui.example/oidc/token'

type Answer = (body: URLSearchParams) => Response | Promise<Response>

// A browser stand-in: sessionStorage as a map, fetch answered by one function, no navigation.
function stub(answer: Answer, session?: { token: string; id: string }) {
  const store = new Map<string, string>()
  if (session) {
    store.set('ichor.idToken', session.id)
    store.set('ichor.refresh', JSON.stringify({ token: session.token, endpoint: ENDPOINT, clientId: 'ichor-ui' }))
  }
  const bodies: URLSearchParams[] = []
  const stubs: Record<string, unknown> = {
    sessionStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) },
    fetch: async (url: string, init: RequestInit) => {
      assert.equal(url, ENDPOINT)
      const b = init.body as URLSearchParams
      bodies.push(b)
      return answer(b)
    },
  }
  for (const [name, value] of Object.entries(stubs)) Object.defineProperty(globalThis, name, { value, configurable: true, writable: true })
  return { store, bodies }
}

const ok = (id: string, refresh?: string) => new Response(JSON.stringify({ id_token: id, refresh_token: refresh }), { status: 200 })
const status = (s: number) => new Response('{}', { status: s })

test('a token is refreshed a minute before it ends, a third of its life before for a short one', () => {
  assert.equal(refreshDelayMs(idAt(NOW, 300), NOW), 240_000)
  assert.equal(refreshDelayMs(idAt(NOW, 300), NOW + 100_000), 140_000)
  assert.equal(refreshDelayMs(idAt(NOW, 60), NOW), 40_000)
  assert.equal(refreshDelayMs(idAt(NOW, 300), NOW + 250_000), 0)
  assert.equal(refreshDelayMs(null, NOW), 0)
  assert.equal(refreshDelayMs('garbage', NOW), 0)
})

test('a refresh trades the refresh token and stores the new ID token and the rotated refresh token', async () => {
  const fresh = idAt(NOW, 300)
  const s = stub(() => ok(fresh, 'r2'), { token: 'r1', id: idAt(NOW - 290_000, 300) })
  assert.equal(await refreshSession(), 'ok')
  assert.deepEqual(Object.fromEntries(s.bodies[0]), { grant_type: 'refresh_token', refresh_token: 'r1', client_id: 'ichor-ui' })
  assert.equal(storedToken(), fresh)
  assert.equal(JSON.parse(s.store.get('ichor.refresh')!).token, 'r2')
})

test('parallel refreshes share one request, so one token is never presented twice', async () => {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  const s = stub(async () => {
    await gate
    return ok(idAt(NOW, 300), 'r2')
  }, { token: 'r1', id: idAt(NOW - 290_000, 300) })
  const all = Promise.all([refreshSession(), refreshSession(), ensureFresh(NOW)])
  release()
  assert.deepEqual((await all).slice(0, 2), ['ok', 'ok'])
  assert.equal(s.bodies.length, 1)
  // The next one is a new request with the rotated token.
  stub(() => ok(idAt(NOW, 300), 'r3'), { token: 'r2', id: idAt(NOW - 290_000, 300) })
  assert.equal(await refreshSession(), 'ok')
})

test('a refresh the issuer refuses ends the session: both tokens are dropped', async () => {
  for (const code of [400, 401, 403]) {
    const s = stub(() => status(code), { token: 'r1', id: idAt(NOW, 300) })
    assert.equal(await refreshSession(), 'ended')
    assert.equal(s.store.size, 0)
  }
})

test('a failure that is not a refusal keeps the session for another try', async () => {
  for (const answer of [() => status(500), () => status(429), () => ok(''), () => Promise.reject(new TypeError('offline'))] as Answer[]) {
    const s = stub(answer, { token: 'r1', id: idAt(NOW, 300) })
    assert.equal(await refreshSession(), 'failed')
    assert.equal(JSON.parse(s.store.get('ichor.refresh')!).token, 'r1')
    assert.ok(storedToken())
  }
})

test('without a refresh token there is nothing to refresh', async () => {
  const s = stub(() => ok('x'))
  s.store.set('ichor.idToken', idAt(NOW, 300))
  assert.equal(await refreshSession(), 'ended')
  assert.equal(s.bodies.length, 0)
})

test('an answer for a session that was replaced meanwhile is dropped, not stored over the new one', async () => {
  const s: ReturnType<typeof stub> = stub(() => {
    // Another sign-in of this tab lands while the refresh is in flight.
    s.store.set('ichor.refresh', JSON.stringify({ token: 'new-family', endpoint: ENDPOINT, clientId: 'ichor-ui' }))
    s.store.set('ichor.idToken', 'new-id')
    return status(400)
  }, { token: 'r1', id: idAt(NOW, 300) })
  assert.equal(await refreshSession(), 'failed')
  assert.equal(storedToken(), 'new-id')
  assert.equal(JSON.parse(s.store.get('ichor.refresh')!).token, 'new-family')
})

test('ensureFresh refreshes only a token that is about to end', async () => {
  const s = stub(() => ok(idAt(NOW, 300), 'r2'), { token: 'r1', id: idAt(NOW, 300) })
  await ensureFresh(NOW + 100_000)
  assert.equal(s.bodies.length, 0)
  await ensureFresh(NOW + 280_000)
  assert.equal(s.bodies.length, 1)
})

test('the loop refreshes on schedule, schedules the next one from the new token, and stops', async () => {
  mock.timers.enable({ apis: ['setTimeout', 'Date'], now: NOW })
  try {
    let n = 0
    const s = stub(() => ok(idAt(Date.now(), 300), `r${++n + 1}`), { token: 'r1', id: idAt(NOW, 300) })
    let ended = 0
    const stop = startRefreshing(() => ended++)
    mock.timers.tick(239_000)
    await Promise.resolve()
    assert.equal(s.bodies.length, 0)
    mock.timers.tick(1_000)
    await new Promise((r) => setImmediate(r))
    assert.equal(s.bodies.length, 1)
    mock.timers.tick(240_000)
    await new Promise((r) => setImmediate(r))
    assert.equal(s.bodies.length, 2)
    assert.equal(s.bodies[1].get('refresh_token'), 'r2')
    stop()
    mock.timers.tick(600_000)
    await new Promise((r) => setImmediate(r))
    assert.equal(s.bodies.length, 2)
    assert.equal(ended, 0)
  } finally {
    mock.timers.reset()
  }
})

test('the loop tells its caller when the issuer ends the session, and retries a transient failure', async () => {
  mock.timers.enable({ apis: ['setTimeout', 'Date'], now: NOW })
  try {
    let answer: Answer = () => status(503)
    const s = stub((b) => answer(b), { token: 'r1', id: idAt(NOW, 300) })
    let ended = 0
    startRefreshing(() => ended++)
    mock.timers.tick(240_000)
    await new Promise((r) => setImmediate(r))
    assert.equal(s.bodies.length, 1)
    assert.equal(ended, 0)
    answer = () => status(400)
    mock.timers.tick(15_000)
    await new Promise((r) => setImmediate(r))
    assert.equal(s.bodies.length, 2)
    assert.equal(ended, 1)
  } finally {
    mock.timers.reset()
  }
})

// A duplicated tab copies sessionStorage, so two tabs can hold the same refresh-token family; the
// second must not use it (the first rotation would make the other present a retired token and
// revoke the family). The holder keeps a Web Lock named after the family for as long as its page lives.
function fakeLocks() {
  const held = new Set<string>()
  return {
    held,
    request: (name: string, opts: { signal?: AbortSignal }, cb: () => unknown): Promise<unknown> => {
      if (!held.has(name)) {
        held.add(name)
        return Promise.resolve(cb())
      }
      return new Promise((_res, rej) => opts.signal?.addEventListener('abort', () => rej(Object.assign(new Error('aborted'), { name: 'AbortError' }))))
    },
  }
}

test('the first tab to claim a family gets it, a copy of that tab does not', async () => {
  const locks = fakeLocks()
  assert.equal(await claimFamily('fam1', locks, 5), true)
  assert.equal(await claimFamily('fam1', locks, 5), false)
  assert.equal(await claimFamily('fam2', locks, 5), true)
})

test('without Web Locks every tab may use its family; a lock that fails for another reason does not sign anyone out', async () => {
  assert.equal(await claimFamily('fam1', null, 5), true)
  assert.equal(await claimFamily('fam1', { request: () => Promise.reject(Object.assign(new Error('x'), { name: 'SecurityError' })) }, 5), true)
})

test('a tab whose family another tab holds neither refreshes nor keeps the copied tokens', async () => {
  const s = stub(() => ok(idAt(NOW, 300), 'r2'), { token: refreshJwt('j1'), id: idAt(NOW, 300) })
  const locks = fakeLocks()
  locks.held.add('ichor-session-fam1')
  assert.equal(await claimSession(locks, 5), false)
  assert.equal(await refreshSession(locks, 5), 'ended')
  assert.equal(s.bodies.length, 0)
  assert.equal(s.store.size, 0)
})
