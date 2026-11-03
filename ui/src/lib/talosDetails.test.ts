import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatSpeed, serviceStatus, serviceTone, sortServices, visibleVolumes } from './talosDetails.ts'

test('link speed is shown in Mbit/s or Gbit/s, and unknown speed as a dash', () => {
  assert.equal(formatSpeed(0), '-')
  assert.equal(formatSpeed(-1), '-')
  assert.equal(formatSpeed(100), '100 Mbit/s')
  assert.equal(formatSpeed(1000), '1 Gbit/s')
  assert.equal(formatSpeed(2500), '2.5 Gbit/s')
})

test('a running service is ok when healthy or without a health check, bad when unhealthy', () => {
  assert.equal(serviceTone({ state: 'Running', healthUnknown: false, healthy: true }), 'good')
  assert.equal(serviceTone({ state: 'Running', healthUnknown: false, healthy: false }), 'bad')
  assert.equal(serviceTone({ state: 'Running', healthUnknown: true, healthy: false }), 'good')
  assert.equal(serviceTone({ state: 'Failed', healthUnknown: true, healthy: false }), 'bad')
  assert.equal(serviceTone({ state: 'Finished', healthUnknown: true, healthy: false }), 'neutral')
})

test('a running service reads Healthy, Unhealthy with its message, or Running without a health check', () => {
  assert.deepEqual(serviceStatus({ state: 'Running', healthUnknown: false, healthy: true }), {
    label: 'Healthy',
    labelKey: 'talos.healthy',
    tone: 'good',
    message: '',
  })
  assert.deepEqual(serviceStatus({ state: 'Running', healthUnknown: false, healthy: false, healthMessage: 'timeout' }), {
    label: 'Unhealthy',
    labelKey: 'talos.unhealthy',
    tone: 'bad',
    message: 'timeout',
  })
  assert.deepEqual(serviceStatus({ state: 'Running', healthUnknown: false, healthy: false }).message, '')
  assert.deepEqual(serviceStatus({ state: 'Running', healthUnknown: true, healthy: false }), {
    label: 'Running',
    tone: 'good',
    message: '',
  })
})

test('any other state is shown as Talos reports it, with its tone', () => {
  assert.deepEqual(serviceStatus({ state: 'Failed', healthUnknown: true, healthy: false }), {
    label: 'Failed',
    tone: 'bad',
    message: '',
  })
  assert.deepEqual(serviceStatus({ state: 'Waiting', healthUnknown: true, healthy: false }), {
    label: 'Waiting',
    tone: 'neutral',
    message: '',
  })
})

test('services needing attention sort first, then by id', () => {
  const svc = (id: string, state: string, healthy: boolean) => ({ id, state, healthUnknown: false, healthy })
  const sorted = sortServices([svc('b', 'Running', true), svc('z', 'Running', false), svc('a', 'Running', true)])
  assert.deepEqual(
    sorted.map((s) => s.id),
    ['z', 'a', 'b'],
  )
})


test('volumes: only disk-backed ones by default, all on request', () => {
  const vols = [{ type: 'partition' }, { type: 'directory' }, { type: 'overlay' }, { type: 'symlink' }, { type: 'disk' }]
  assert.deepEqual(visibleVolumes(vols, false).map((v) => v.type), ['partition', 'disk'])
  assert.equal(visibleVolumes(vols, true).length, 5)
})
