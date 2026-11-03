import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatSpeed, healthKey, serviceTone, sortServices, visibleVolumes } from './talosDetails.ts'

test('link speed is shown in Mbit/s or Gbit/s, and unknown speed as a dash', () => {
  assert.equal(formatSpeed(0), '-')
  assert.equal(formatSpeed(-1), '-')
  assert.equal(formatSpeed(100), '100 Mbit/s')
  assert.equal(formatSpeed(1000), '1 Gbit/s')
  assert.equal(formatSpeed(2500), '2.5 Gbit/s')
})

test('a running healthy service is ok, a running unhealthy one is bad, unknown health is neutral', () => {
  assert.equal(serviceTone({ state: 'Running', healthUnknown: false, healthy: true }), 'good')
  assert.equal(serviceTone({ state: 'Running', healthUnknown: false, healthy: false }), 'bad')
  assert.equal(serviceTone({ state: 'Running', healthUnknown: true, healthy: false }), 'neutral')
  assert.equal(serviceTone({ state: 'Failed', healthUnknown: true, healthy: false }), 'bad')
  assert.equal(serviceTone({ state: 'Finished', healthUnknown: true, healthy: false }), 'neutral')
})

test('a service without a health check says so; yes and no stay as they are', () => {
  assert.equal(healthKey({ state: 'Running', healthUnknown: true, healthy: false }), 'talos.noHealthCheck')
  assert.equal(healthKey({ state: 'Running', healthUnknown: false, healthy: true }), 'common.yes')
  assert.equal(healthKey({ state: 'Running', healthUnknown: false, healthy: false }), 'common.no')
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
