import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { ActionPermissions } from './actions.ts'
import { availableActions, canStart, wipeChoices, wipeModeOf, mayNodeAction, nodeActions, nodeNameMatches, restartableServices } from './nodeActions.ts'
import { admin, operator, viewer } from './roles.testdata.ts'

const ids = (role: string, perms: ActionPermissions | undefined) =>
  availableActions({ approved: true, deleting: false, role, perms }).map((a) => a.id)

test('each role is offered exactly the node actions it can perform', () => {
  assert.deepEqual(ids('controlplane', undefined), [])
  assert.deepEqual(ids('controlplane', viewer), [])
  // An operator holds create on talosoperations but neither start verb, the override nor delete on talosmachines.
  assert.deepEqual(ids('controlplane', operator), ['restart-service', 'etcd-defrag', 'etcd-forfeit-leadership', 'reboot'])
  assert.deepEqual(ids('worker', operator), ['restart-service', 'reboot'])
  assert.deepEqual(ids('worker', admin), ['restart-service', 'reboot', 'shutdown', 'remove'])
})

test('shutdown and remove each need their own rights, and the operation ones need create', () => {
  assert.equal(mayNodeAction({ ...viewer, startNodeShutdown: true }, 'shutdown'), false)
  assert.equal(mayNodeAction({ ...operator, startNodeShutdown: true }, 'shutdown'), true)
  const rights = { updateMachines: true, deleteMachines: true, startNodeReset: true, overrideMachines: true }
  assert.equal(mayNodeAction({ ...viewer, ...rights }, 'remove'), true)
  for (const missing of Object.keys(rights)) {
    assert.equal(mayNodeAction({ ...viewer, ...rights, [missing]: false }, 'remove'), false, missing)
  }
  assert.equal(mayNodeAction({ ...operator, startNodeReset: true }, 'remove'), false)
})

test('a worker gets no etcd action, a control plane gets them all', () => {
  assert.deepEqual(ids('worker', admin), ['restart-service', 'reboot', 'shutdown', 'remove'])
  assert.deepEqual(ids('controlplane', admin), [
    'restart-service',
    'etcd-defrag',
    'etcd-forfeit-leadership',
    'reboot',
    'shutdown',
    'remove',
  ])
})

test('nothing is offered for a node that is not approved or is being removed', () => {
  assert.deepEqual(availableActions({ approved: false, deleting: false, role: 'worker', perms: admin }), [])
  assert.deepEqual(availableActions({ approved: true, deleting: true, role: 'worker', perms: admin }), [])
})

test('reboot, shutdown and remove are destructive, and the irreversible ones need the name', () => {
  const by = Object.fromEntries(nodeActions.map((a) => [a.id, a]))
  for (const id of ['reboot', 'shutdown', 'remove']) assert.equal(by[id].destructive, true, id)
  for (const id of ['restart-service', 'etcd-defrag', 'etcd-forfeit-leadership']) assert.equal(by[id].destructive, false, id)
  for (const id of ['shutdown', 'remove']) assert.equal(by[id].typedName, true, id)
  assert.equal(by.reboot.typedName, false)
  assert.equal(by.remove.override, true)
  assert.equal(by.reset, undefined, 'reset is part of remove')
})

test('the service list never offers etcd, apid, trustd or machined', () => {
  assert.deepEqual([...restartableServices], ['kubelet', 'containerd', 'cri'])
})

test('the typed name must match exactly, ignoring surrounding spaces', () => {
  assert.equal(nodeNameMatches(' node-1 ', 'node-1'), true)
  assert.equal(nodeNameMatches('Node-1', 'node-1'), false)
})

const base = {
  def: nodeActions.find((a) => a.id === 'remove')!,
  blocked: '',
  planned: true,
  confirmName: 'node-1',
  typed: 'node-1',
  nodeName: 'node-1',
  overridden: true,
  service: '',
  busy: false,
}

test('Start waits for the plan, the typed name and the override tick, and never starts a blocked plan', () => {
  assert.equal(canStart(base), true)
  assert.equal(canStart({ ...base, planned: false }), false)
  assert.equal(canStart({ ...base, blocked: 'another operation is running' }), false)
  assert.equal(canStart({ ...base, typed: 'node-' }), false)
  assert.equal(canStart({ ...base, overridden: false }), false)
  assert.equal(canStart({ ...base, busy: true }), false)
})

test('a reset needs the override ticked', () => {
  assert.equal(canStart({ ...base, overridden: true }), true)
})

test('a service restart needs a service from the allowlist', () => {
  const def = nodeActions.find((a) => a.id === 'restart-service')!
  const o = { ...base, def, confirmName: '', typed: '', overridden: false }
  assert.equal(canStart({ ...o, service: '' }), false)
  assert.equal(canStart({ ...o, service: 'etcd' }), false)
  assert.equal(canStart({ ...o, service: 'kubelet' }), true)
})

test('a remove wipes STATE and EPHEMERAL by default and sends the spec.node.wipeMode names for the bigger wipes', () => {
  assert.deepEqual(wipeChoices.map(wipeModeOf), ['', 'system-disk', 'system-disk-and-user-disks'])
})
