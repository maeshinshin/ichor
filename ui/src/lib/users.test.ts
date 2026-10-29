import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Code, ConnectError } from '@connectrpc/connect'
import { downloadName, passwordError, passwordOk, rolesOf, toggleRole, userError, userNameOk } from './users.ts'

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
