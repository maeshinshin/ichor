import assert from 'node:assert/strict'
import { test } from 'node:test'
import { base64url, buildAuthUrl, clearToken, exchangeCode, finishLogin, idTokenClaims, pkceChallenge, storedToken, tokenUsable } from './oidc.ts'

function jwt(claims: object): string {
  return `h.${base64url(new TextEncoder().encode(JSON.stringify(claims)))}.s`
}

// A minimal browser stand-in for finishLogin: a sessionStorage map, the callback URL, and a record
// of history.replaceState calls. Globals are restored by the returned cleanup.
function stubBrowser(search: string, pending?: string) {
  const store = new Map<string, string>()
  if (pending !== undefined) store.set('ichor.oidcPending', pending)
  const replaced: string[] = []
  const stubs: Record<string, unknown> = {
    sessionStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    },
    window: {
      location: { search, origin: 'https://ui.example' },
      history: { replaceState: (_s: unknown, _t: string, url: string) => void replaced.push(url) },
      // The router re-reads the path only on popstate, so the URL change must announce itself.
      dispatchEvent: (e: Event) => void replaced.push(`event:${e.type}`),
    },
  }
  for (const [name, value] of Object.entries(stubs)) {
    Object.defineProperty(globalThis, name, { value, configurable: true, writable: true })
  }
  return {
    store,
    replaced,
    cleanup: () => {
      for (const name of Object.keys(stubs)) Reflect.deleteProperty(globalThis, name)
    },
  }
}

const providers = [{ id: 'oidc', issuer: 'https://idp.example', clientId: 'ichor' }]

test('a callback with no pending login is stale: it clears the URL and returns false, without throwing', async () => {
  const b = stubBrowser('?code=abc&state=xyz')
  try {
    assert.equal(await finishLogin(providers), false)
    assert.deepEqual(b.replaced, ['/', 'event:popstate'])
  } finally {
    b.cleanup()
  }
})

test('a callback whose pending login names an unknown provider returns false, without throwing', async () => {
  const b = stubBrowser('?code=abc&state=xyz', JSON.stringify({ verifier: 'v', state: 'xyz', nonce: 'n', provider: 'gone' }))
  try {
    assert.equal(await finishLogin(providers), false)
    assert.equal(b.store.has('ichor.oidcPending'), false)
  } finally {
    b.cleanup()
  }
})

test('PKCE S256 matches the RFC 7636 appendix B vector', async () => {
  assert.equal(
    await pkceChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk'),
    'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM',
  )
})

test('the authorization URL is a code flow with PKCE and no client secret', () => {
  const u = new URL(
    buildAuthUrl('https://idp.example/auth', {
      clientId: 'ichor',
      redirectUri: 'https://ui.example/',
      state: 's',
      nonce: 'n',
      challenge: 'c',
    }),
  )
  assert.equal(u.searchParams.get('response_type'), 'code')
  assert.equal(u.searchParams.get('code_challenge_method'), 'S256')
  assert.equal(u.searchParams.get('client_secret'), null)
})

test('an expired, missing or malformed token is not usable', () => {
  const now = 1_000_000_000_000
  assert.equal(tokenUsable(jwt({ exp: now / 1000 + 3600 }), now), true)
  assert.equal(tokenUsable(jwt({ exp: now / 1000 - 1 }), now), false)
  assert.equal(tokenUsable(null, now), false)
  assert.equal(tokenUsable('garbage', now), false)
  assert.equal(idTokenClaims('garbage'), null)
})

const buildArgs = { clientId: 'ichor-ui', redirectUri: 'https://ui.example/auth/callback', state: 's', nonce: 'n', challenge: 'c' }

// ADR 0094 amendment (2026-11-04): the ichor issuer only hands out a refresh token for offline_access.
test('the ichor sign-in asks for offline_access, the external IdP request does not', () => {
  const scope = (offline?: boolean) => new URL(buildAuthUrl('https://ui.example/oidc/authorize', { ...buildArgs, offline })).searchParams.get('scope')!.split(' ')
  assert.ok(scope(true).includes('offline_access'))
  assert.ok(scope(true).includes('openid'))
  assert.equal(scope(false).includes('offline_access'), false)
  assert.equal(scope().includes('offline_access'), false)
})

const ichorCfg = { id: 'ichor', issuer: 'https://ui.example', clientId: 'ichor-ui' }
const discovered = { authorization_endpoint: 'https://ui.example/oidc/authorize', token_endpoint: 'https://ui.example/oidc/token' }

function stubToken(body: unknown) {
  const b = stubBrowser('')
  Object.defineProperty(globalThis, 'fetch', { value: async () => new Response(JSON.stringify(body), { status: 200 }), configurable: true, writable: true })
  return b
}

test('the code exchange keeps the refresh token with its token endpoint and client, for the ichor provider only', async () => {
  const b = stubToken({ id_token: jwt({ nonce: 'n', exp: 9999999999 }), refresh_token: 'r1' })
  try {
    await exchangeCode(discovered, ichorCfg, 'c', 'v', 'n')
    assert.deepEqual(JSON.parse(b.store.get('ichor.refresh')!), { token: 'r1', endpoint: discovered.token_endpoint, clientId: 'ichor-ui' })
    assert.ok(storedToken())
    clearToken()
    assert.equal(b.store.size, 0)
  } finally {
    b.cleanup()
  }
  const x = stubToken({ id_token: jwt({ nonce: 'n', exp: 9999999999 }), refresh_token: 'r1' })
  try {
    await exchangeCode(discovered, { id: 'oidc', issuer: 'https://idp.example', clientId: 'ichor' }, 'c', 'v', 'n')
    assert.equal(x.store.has('ichor.refresh'), false)
  } finally {
    x.cleanup()
  }
})

test('a new sign-in drops the refresh token of the one before it', async () => {
  const b = stubToken({ id_token: jwt({ nonce: 'n', exp: 9999999999 }) })
  b.store.set('ichor.refresh', JSON.stringify({ token: 'old', endpoint: 'e', clientId: 'c' }))
  try {
    await exchangeCode(discovered, ichorCfg, 'c', 'v', 'n')
    assert.equal(b.store.has('ichor.refresh'), false)
  } finally {
    b.cleanup()
  }
})
