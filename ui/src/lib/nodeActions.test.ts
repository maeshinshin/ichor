import assert from 'node:assert/strict'
import { test } from 'node:test'
import { availableActions, canStart, nodeActions, nodeNameMatches, restartableServices } from './nodeActions.ts'

const ids = (role: string) => availableActions({ approved: true, deleting: false, role }).map((a) => a.id)

test('a worker gets no etcd action, a control plane gets them all', () => {
  assert.deepEqual(ids('worker'), ['restart-service', 'reboot', 'shutdown', 'remove', 'reset'])
  assert.deepEqual(ids('controlplane'), [
    'restart-service',
    'etcd-defrag',
    'etcd-forfeit-leadership',
    'reboot',
    'shutdown',
    'remove',
    'reset',
  ])
})

test('nothing is offered for a node that is not approved or is being removed', () => {
  assert.deepEqual(availableActions({ approved: false, deleting: false, role: 'worker' }), [])
  assert.deepEqual(availableActions({ approved: true, deleting: true, role: 'worker' }), [])
})

test('reboot, shutdown, remove and reset are destructive, and the irreversible ones need the name', () => {
  const by = Object.fromEntries(nodeActions.map((a) => [a.id, a]))
  for (const id of ['reboot', 'shutdown', 'remove', 'reset']) assert.equal(by[id].destructive, true, id)
  for (const id of ['restart-service', 'etcd-defrag', 'etcd-forfeit-leadership']) assert.equal(by[id].destructive, false, id)
  for (const id of ['shutdown', 'remove', 'reset']) assert.equal(by[id].typedName, true, id)
  assert.equal(by.reboot.typedName, false)
  assert.equal(by.reset.override, true)
  assert.equal(by.remove.override, false)
})

test('the service list never offers etcd, apid, trustd or machined', () => {
  assert.deepEqual([...restartableServices], ['kubelet', 'containerd', 'cri'])
})

test('the typed name must match exactly, ignoring surrounding spaces', () => {
  assert.equal(nodeNameMatches(' node-1 ', 'node-1'), true)
  assert.equal(nodeNameMatches('Node-1', 'node-1'), false)
})

const base = {
  def: nodeActions.find((a) => a.id === 'reset')!,
  blocked: '',
  planned: true,
  confirmName: 'node-1',
  typed: 'node-1',
  nodeName: 'node-1',
  reason: 'node is dead',
  service: '',
  busy: false,
}

test('Start waits for the plan, the typed name and the reason, and never starts a blocked plan', () => {
  assert.equal(canStart(base), true)
  assert.equal(canStart({ ...base, planned: false }), false)
  assert.equal(canStart({ ...base, blocked: 'another operation is running' }), false)
  assert.equal(canStart({ ...base, typed: 'node-' }), false)
  assert.equal(canStart({ ...base, reason: '  ' }), false)
  assert.equal(canStart({ ...base, busy: true }), false)
})

test('a service restart needs a service from the allowlist', () => {
  const def = nodeActions.find((a) => a.id === 'restart-service')!
  const o = { ...base, def, confirmName: '', typed: '', reason: '' }
  assert.equal(canStart({ ...o, service: '' }), false)
  assert.equal(canStart({ ...o, service: 'etcd' }), false)
  assert.equal(canStart({ ...o, service: 'kubelet' }), true)
})
