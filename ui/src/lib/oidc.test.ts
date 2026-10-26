import assert from 'node:assert/strict'
import { test } from 'node:test'
import { base64url, buildAuthUrl, idTokenClaims, pkceChallenge, tokenUsable } from './oidc.ts'

function jwt(claims: object): string {
  return `h.${base64url(new TextEncoder().encode(JSON.stringify(claims)))}.s`
}

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
