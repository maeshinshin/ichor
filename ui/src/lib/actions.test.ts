import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canListUsers, clusterMenu, mayApprove, mayReject, mayStart } from './actions.ts'
import { admin, operator, viewer } from './roles.testdata.ts'

const none = viewer

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

test('a pending machine: a viewer does neither, an operator and an admin approve and reject', () => {
  assert.equal(mayApprove(undefined), false)
  assert.equal(mayReject(undefined), false)
  assert.equal(mayApprove(viewer), false)
  assert.equal(mayReject(viewer), false)
  assert.equal(mayApprove(operator), true)
  assert.equal(mayReject(operator), true)
  assert.equal(mayApprove(admin), true)
  assert.equal(mayReject(admin), true)
  // The approve verb alone cannot write the machine.
  assert.equal(mayApprove({ ...none, approveMachines: true }), false)
  assert.equal(mayApprove({ ...none, updateMachines: true }), false)
})
