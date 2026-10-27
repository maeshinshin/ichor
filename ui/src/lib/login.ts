// The ichor sign-in form: the issuer's authorize endpoint sends the browser to
// /login?ticket=..., this page posts {ticket, username, password} to /oidc/login and follows the
// redirect_uri it answers. Pure helpers only; the password never reaches a log, a URL or storage.
import type { Key } from './i18n.ts'

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
