import assert from 'node:assert/strict'
import { test } from 'node:test'
import { base64url, buildAuthUrl, finishLogin, idTokenClaims, pkceChallenge, tokenUsable } from './oidc.ts'

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
    assert.deepEqual(b.replaced, ['/'])
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
