// Helpers for the Users page and the account menu (docs/adr/0094). Pure: no DOM, no preact.
import { Code, ConnectError } from '@connectrpc/connect'
import { t } from './i18n.ts'

export const roleNames = ['viewer', 'operator', 'admin'] as const

// toggleRole adds or removes a role and keeps the list in viewer, operator, admin order.
export function toggleRole(roles: string[], role: string, on: boolean): string[] {
  const set = new Set(roles)
  if (on) set.add(role)
  else set.delete(role)
  return roleNames.filter((r) => set.has(r))
}

// The server's own bound (pkg/ichorauth): length only, counted in characters.
export const minPasswordLength = 12

export function passwordLength(p: string): number {
  return [...p].length
}

export function passwordOk(p: string): boolean {
  return passwordLength(p) >= minPasswordLength
}

const alphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'

// generatePassword draws from a crypto source without modulo bias.
export function generatePassword(length = 24, random: (a: Uint8Array<ArrayBuffer>) => Iterable<number> = (a) => crypto.getRandomValues(a)): string {
  const limit = 256 - (256 % alphabet.length)
  let out = ''
  while (out.length < length) {
    for (const b of random(new Uint8Array(length * 2))) {
      if (b < limit && out.length < length) out += alphabet[b % alphabet.length]
    }
  }
  return out
}

// userNameOk mirrors what the CRD accepts for a user name: a DNS-1123 label.
export function userNameOk(name: string): boolean {
  return /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/.test(name)
}

export function isForbidden(err: unknown): boolean {
  return err instanceof ConnectError && err.code === Code.PermissionDenied
}

// userError is the inline message for a failed user call. A 403 names the role the person needs;
// everything else is the server's own message under a short headline.
export function userError(err: unknown): { title: string; detail: string } {
  if (isForbidden(err)) return { title: t('error.notAllowed'), detail: t('users.needAdmin') }
  if (err instanceof ConnectError) return { title: t('error.requestFailed'), detail: err.rawMessage }
  return { title: t('error.requestFailed'), detail: err instanceof Error ? err.message : String(err) }
}

// A wrong old password comes back as PermissionDenied "invalid credentials", which is not a
// missing role, so the password dialog uses this instead of userError.
export function passwordError(err: unknown): { title: string; detail: string } {
  if (isForbidden(err)) return { title: t('error.notAllowed'), detail: t('account.oldPasswordWrong') }
  return userError(err)
}

export function downloadName(filename: string, fallback: string): string {
  const f = filename.replace(/[^A-Za-z0-9._-]/g, '')
  return f !== '' ? f : fallback
}

export function sourceLabel(source: string): string {
  return source === 'ichor' ? t('account.sourceIchor') : source === 'oidc' ? t('account.sourceOidc') : source
}

// rolesOf reads the ichor roles out of the Kubernetes groups (ichor:admin, ...).
export function rolesOf(groups: string[]): string[] {
  return roleNames.filter((r) => groups.includes(`ichor:${r}`))
}
