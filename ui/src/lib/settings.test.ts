import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  changesOf,
  durationOptions,
  formOf,
  lockedReason,
  normalizeDuration,
  parseDuration,
  sourceOf,
  usersProblem,
  type SettingsView,
} from './settings.ts'

test('durations are read and shortened', () => {
  assert.equal(parseDuration('15m0s'), 900)
  assert.equal(parseDuration('1h0m0s'), 3600)
  assert.equal(parseDuration('soon'), null)
  assert.equal(normalizeDuration('15m0s'), '15m')
  assert.equal(normalizeDuration('720h0m0s'), '720h')
  assert.equal(normalizeDuration('weird'), 'weird')
})

test('a current value outside the presets is offered, in order', () => {
  assert.deepEqual(durationOptions(['5m', '1h'], '10m'), ['5m', '10m', '1h'])
  assert.deepEqual(durationOptions(['5m', '1h'], '5m'), ['5m', '1h'])
})

const view: SettingsView = {
  readOnly: false,
  canEditSettings: false,
  spec: { tokenTtl: '30m0s' },
  effective: {
    tokenTtl: { value: '30m0s', source: 'spec' },
    refreshLifetime: { value: '168h0m0s', source: 'deployment' },
    driftPolicy: { value: 'autoCorrect', source: 'spec' },
  },
}

test('the form starts from spec, then what is in force', () => {
  assert.deepEqual(formOf(view), { tokenTtl: '30m', refreshLifetime: '168h', driftPolicy: 'autoCorrect' })
})

test('the source is spec only where the spec or the report says so', () => {
  assert.equal(sourceOf(view, 'tokenTtl'), 'spec')
  assert.equal(sourceOf(view, 'refreshLifetime'), 'deployment')
  assert.equal(sourceOf(view, 'driftPolicy'), 'spec')
  assert.equal(sourceOf({ ...view, effective: undefined }, 'refreshLifetime'), 'deployment')
})

test('only changed fields are sent', () => {
  const before = formOf(view)
  assert.deepEqual(changesOf(before, { ...before, tokenTtl: '15m' }), { tokenTtl: '15m' })
  assert.deepEqual(changesOf(before, before), {})
  assert.deepEqual(changesOf(before, { ...before, driftPolicy: 'warnOnly' }), { driftPolicy: 'warnOnly' })
})

test('lifetimes are checked against the bounds', () => {
  assert.equal(usersProblem({ tokenTtl: '15m', refreshLifetime: '24h' }), null)
  assert.equal(usersProblem({ tokenTtl: '30s', refreshLifetime: '24h' }), 'settings.badTokenTtl')
  assert.equal(usersProblem({ tokenTtl: '15m', refreshLifetime: '30m' }), 'settings.badRefreshLifetime')
  assert.equal(usersProblem({ tokenTtl: '15m', refreshLifetime: '721h' }), 'settings.badRefreshLifetime')
})

test('controls are locked for read-only mode and a missing verb', () => {
  assert.equal(lockedReason({ ...view, canEditSettings: true }), null)
  assert.equal(lockedReason(view), 'settings.noPermissionAlert')
  assert.equal(lockedReason({ ...view, readOnly: true }), 'settings.readOnlyAlert')
})
