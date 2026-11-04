import assert from 'node:assert/strict'
import { test } from 'node:test'
import { LoginError, loginBody, loginErrorKey, loginReady, passwordSignIn, redirectTarget, signInAgain, ticketFromSearch } from './login.ts'
import { en } from './i18n.ts'
import { base64url, redirectUriFor, storedToken, type AuthProvider } from './oidc.ts'

test('the ticket comes from the query string', () => {
  assert.equal(ticketFromSearch('?ticket=abc.def'), 'abc.def')
  assert.equal(ticketFromSearch(''), '')
})

test('the form needs a user name and a password', () => {
  assert.equal(loginReady({ username: ' ', password: 'x' }), false)
  assert.equal(loginReady({ username: 'alice', password: '' }), false)
  assert.equal(loginReady({ username: 'alice', password: 'x' }), true)
})

test('the body is the ticket, the trimmed user name and the password as typed', () => {
  assert.deepEqual(JSON.parse(loginBody('t', { username: ' alice ', password: ' pw ' })), { ticket: 't', username: 'alice', password: ' pw ' })
})

test('a refused login is generic, whatever the reason', () => {
  assert.equal(loginErrorKey(401), 'login.invalid')
  assert.equal(loginErrorKey(403), 'login.invalid')
  assert.equal(en['login.invalid'], 'Invalid username or password')
  assert.equal(loginErrorKey(400), 'login.expired')
  assert.equal(loginErrorKey(429), 'login.rateLimited')
  assert.equal(loginErrorKey(500), 'login.unavailable')
})

test('only an http(s) redirect_uri is followed', () => {
  assert.equal(redirectTarget({ redirect_uri: 'https://ui.example/auth/callback?code=c&state=s' }), 'https://ui.example/auth/callback?code=c&state=s')
  assert.equal(redirectTarget({ redirect_uri: 'javascript:alert(1)' }), null)
  assert.equal(redirectTarget({ redirect_uri: 5 }), null)
  assert.equal(redirectTarget(null), null)
})

test('the ichor client returns to /auth/callback, the external IdP to the root', () => {
  assert.equal(redirectUriFor('ichor', 'https://ui.example'), 'https://ui.example/auth/callback')
  assert.equal(redirectUriFor('oidc', 'https://ui.example'), 'https://ui.example/')
})

const ichor: AuthProvider = { id: 'ichor', issuer: 'https://ui.example', clientId: 'ichor-ui' }

// Stand-ins for the browser: fetch answers per URL, sessionStorage is a map, and nothing may
// navigate (window.location has no assign).
function stubSignIn(answers: Record<string, (init?: RequestInit, url?: string) => Response>) {
  const store = new Map<string, string>()
  const calls: string[] = []
  const stubs: Record<string, unknown> = {
    sessionStorage: { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) },
    window: { location: { origin: 'https://ui.example' } },
    fetch: async (url: string, init?: RequestInit) => {
      const key = new URL(url, 'https://ui.example').pathname
      calls.push(key)
      return answers[key](init, url)
    },
  }
  for (const [name, value] of Object.entries(stubs)) Object.defineProperty(globalThis, name, { value, configurable: true, writable: true })
  return calls
}

const json = (v: unknown, status = 200) => new Response(JSON.stringify(v), { status, headers: { 'Content-Type': 'application/json' } })
const discovery = () => json({ authorization_endpoint: 'https://ui.example/oidc/authorize', token_endpoint: 'https://ui.example/oidc/token' })
const idToken = (nonce: string) => `h.${base64url(new TextEncoder().encode(JSON.stringify({ nonce, exp: 9999999999 })))}.s`

test('the in-page sign-in gets a ticket, logs in and exchanges the code without navigating', async () => {
  let nonce = ''
  let state = ''
  const calls = stubSignIn({
    '/.well-known/openid-configuration': discovery,
    '/oidc/authorize': (init, url) => {
      assert.equal((init?.headers as Record<string, string>).Accept, 'application/json')
      const q = new URL(url!).searchParams
      nonce = q.get('nonce')!
      state = q.get('state')!
      assert.equal(q.get('code_challenge_method'), 'S256')
      return json({ ticket: 't1' })
    },
    '/oidc/login': (init) => {
      assert.deepEqual(JSON.parse(init!.body as string), { ticket: 't1', username: 'ann', password: 'pw' })
      return json({ redirect_uri: `https://ui.example/auth/callback?code=c1&state=${state}` })
    },
    '/oidc/token': (init) => {
      const b = init!.body as URLSearchParams
      assert.equal(b.get('code'), 'c1')
      assert.ok(b.get('code_verifier'))
      return json({ id_token: idToken(nonce) })
    },
  })
  await passwordSignIn(ichor, { username: 'ann', password: 'pw' })
  assert.deepEqual(calls, ['/.well-known/openid-configuration', '/oidc/authorize', '/oidc/login', '/oidc/token'])
  assert.ok(storedToken())
})

test('the in-page sign-in maps refusals to the card messages and stores nothing', async () => {
  for (const [status, key] of [[401, 'login.invalid'], [429, 'login.rateLimited'], [400, 'login.expired']] as const) {
    stubSignIn({
      '/.well-known/openid-configuration': discovery,
      '/oidc/authorize': () => json({ ticket: 't1' }),
      '/oidc/login': () => json({ error: 'x' }, status),
    })
    await assert.rejects(passwordSignIn(ichor, { username: 'ann', password: 'bad' }), (e: unknown) => e instanceof LoginError && e.key === key)
    assert.equal(storedToken(), null)
  }
})

test('the in-page sign-in refuses a redirect with the wrong state or another origin', async () => {
  for (const redirect of ['https://ui.example/auth/callback?code=c1&state=other', 'https://evil.example/auth/callback?code=c1&state=x']) {
    stubSignIn({
      '/.well-known/openid-configuration': discovery,
      '/oidc/authorize': () => json({ ticket: 't1' }),
      '/oidc/login': () => json({ redirect_uri: redirect }),
    })
    await assert.rejects(passwordSignIn(ichor, { username: 'ann', password: 'pw' }), LoginError)
  }
})

// A password change ends the tab's own ID token (docs/adr/0108), so the SPA signs in again with
// the new password through the normal flow, as the user the old token names.
test('signing in again after a password change reuses the user of the stored token and the ichor provider', async () => {
  let nonce = ''
  let state = ''
  const old = `h.${base64url(new TextEncoder().encode(JSON.stringify({ sub: 'ann', exp: 9999999999 })))}.s`
  const calls = stubSignIn({
    '/auth/config': () => json({ enabled: true, providers: [{ id: 'oidc', issuer: 'https://idp.example', clientId: 'x' }, ichor] }),
    '/.well-known/openid-configuration': discovery,
    '/oidc/authorize': (_i, url) => {
      nonce = new URL(url!).searchParams.get('nonce')!
      state = new URL(url!).searchParams.get('state')!
      return json({ ticket: 't1' })
    },
    '/oidc/login': (init) => {
      assert.deepEqual(JSON.parse(init!.body as string), { ticket: 't1', username: 'ann', password: 'new-pw' })
      return json({ redirect_uri: `https://ui.example/auth/callback?code=c1&state=${state}` })
    },
    '/oidc/token': () => json({ id_token: idToken(nonce) }),
  })
  sessionStorage.setItem('ichor.idToken', old)
  await signInAgain('new-pw')
  assert.deepEqual(calls, ['/auth/config', '/.well-known/openid-configuration', '/oidc/authorize', '/oidc/login', '/oidc/token'])
  assert.notEqual(storedToken(), old)
})

test('signing in again fails, for the caller to sign out, without an ichor provider or a user', async () => {
  stubSignIn({ '/auth/config': () => json({ enabled: true, providers: [] }) })
  sessionStorage.setItem('ichor.idToken', `h.${base64url(new TextEncoder().encode(JSON.stringify({ sub: 'ann' })))}.s`)
  await assert.rejects(signInAgain('pw'), LoginError)

  stubSignIn({ '/auth/config': () => json({ enabled: true, providers: [ichor] }) })
  await assert.rejects(signInAgain('pw'), LoginError)
})
