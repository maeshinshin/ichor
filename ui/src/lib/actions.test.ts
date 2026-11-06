import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canListUsers, clusterMenu, mayStart, type ActionPermissions } from './actions.ts'

const none: ActionPermissions = {
  listUsers: false,
  createOperations: false,
  startCaRotation: false,
  startKeyRotation: false,
  startShutdown: false,
  startEtcdRestore: false,
  updateIchorVersion: false,
  updateBackups: false,
  clusterUpdate: false,
  talosconfigReader: false,
}
// The built-in roles (config/rbac/ichor_*_role.yaml).
const viewer = none
const operator: ActionPermissions = { ...none, createOperations: true, updateBackups: true, clusterUpdate: true, talosconfigReader: true }
const admin: ActionPermissions = {
  ...operator,
  listUsers: true,
  startCaRotation: true,
  startKeyRotation: true,
  startShutdown: true,
  startEtcdRestore: true,
  updateIchorVersion: true,
}

test('nothing is offered until the permissions are known', () => {
  assert.equal(canListUsers(undefined), false)
  for (const k of ['talos', 'kubernetes', 'cni', 'config', 'ichor', 'shutdown', 'ca-rotation', 'key-rotation', 'etcd-restore'] as const) {
    assert.equal(mayStart(undefined, k), false, k)
  }
  assert.deepEqual(clusterMenu(undefined), [])
})

test('only an admin lists users', () => {
  assert.equal(canListUsers(admin), true)
  assert.equal(canListUsers(operator), false)
  assert.equal(canListUsers(viewer), false)
})

test('a viewer starts nothing; an operator the routine kinds; an admin all', () => {
  const routine = ['talos', 'kubernetes', 'cni', 'config'] as const
  const guarded = ['ichor', 'shutdown', 'ca-rotation', 'key-rotation', 'etcd-restore'] as const
  for (const k of routine) {
    assert.equal(mayStart(viewer, k), false, `viewer ${k}`)
    assert.equal(mayStart(operator, k), true, `operator ${k}`)
    assert.equal(mayStart(admin, k), true, `admin ${k}`)
  }
  for (const k of guarded) {
    assert.equal(mayStart(viewer, k), false, `viewer ${k}`)
    assert.equal(mayStart(operator, k), false, `operator ${k}`)
    assert.equal(mayStart(admin, k), true, `admin ${k}`)
  }
})

test('the start verb alone is not enough without create', () => {
  assert.equal(mayStart({ ...none, startShutdown: true }, 'shutdown'), false)
})

test('the cluster menu lists only what the role may do', () => {
  assert.deepEqual(clusterMenu(viewer), [])
  assert.deepEqual(clusterMenu(operator), ['reader-talosconfig'])
  assert.deepEqual(clusterMenu(admin), ['ca-rotation', 'key-rotation', 'reader-talosconfig', 'shutdown'])
})
