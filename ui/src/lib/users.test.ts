import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { downloadName, generatePassword, passwordError, passwordOk, rolesOf, toggleRole, userError, userNameOk } from './users.ts'

test('roles stay in viewer, operator, admin order', () => {
  assert.deepEqual(toggleRole(['admin'], 'viewer', true), ['viewer', 'admin'])
  assert.deepEqual(toggleRole(['viewer', 'admin'], 'viewer', false), ['admin'])
  assert.deepEqual(toggleRole([], 'bogus', true), [])
})

test('the password bound counts characters, as the server does', () => {
  assert.equal(passwordOk('a'.repeat(11)), false)
  assert.equal(passwordOk('a'.repeat(12)), true)
  assert.equal(passwordOk('あ'.repeat(12)), true)
})

test('a generated password has the length asked for and only readable characters', () => {
  const p = generatePassword(24)
  assert.equal(p.length, 24)
  assert.match(p, /^[A-HJ-NP-Za-km-z2-9]+$/)
  assert.notEqual(generatePassword(24), p)
})

test('generatePassword skips bytes that would bias the draw', () => {
  const bytes = [255, 254, 0, 1, 2]
  const p = generatePassword(3, (a) => a.map((_, i) => bytes[i % bytes.length]))
  assert.equal(p, 'ABC')
})

test('user names are DNS labels', () => {
  assert.equal(userNameOk('alice-2'), true)
  for (const bad of ['', 'Alice', '-a', 'a-', 'a_b', 'a.b']) assert.equal(userNameOk(bad), false, bad)
})

test('a 403 names the role to ask for', () => {
  const e = userError(new ConnectError('no', Code.PermissionDenied))
  assert.match(e.detail, /ichor-admin/)
  assert.equal(userError(new ConnectError('the last admin', Code.FailedPrecondition)).detail, 'the last admin')
})

test('a 403 from the password change means the old password was wrong', () => {
  assert.match(passwordError(new ConnectError('invalid credentials', Code.PermissionDenied)).detail, /current password/)
})

test('the saved file name keeps only safe characters', () => {
  assert.equal(downloadName('kubeconfig-prod.yaml', 'k.yaml'), 'kubeconfig-prod.yaml')
  assert.equal(downloadName('../x/y', 'k.yaml'), '..xy')
  assert.equal(downloadName('', 'k.yaml'), 'k.yaml')
})

test('roles are read from the ichor groups', () => {
  assert.deepEqual(rolesOf(['system:authenticated', 'ichor:admin', 'ichor:viewer']), ['viewer', 'admin'])
})
