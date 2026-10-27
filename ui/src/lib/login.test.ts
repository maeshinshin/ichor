import assert from 'node:assert/strict'
import { test } from 'node:test'
import { loginBody, loginErrorKey, loginReady, redirectTarget, ticketFromSearch } from './login.ts'
import { en } from './i18n.ts'
import { redirectUriFor } from './oidc.ts'

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
