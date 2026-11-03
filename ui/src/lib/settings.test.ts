import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  autoPatchChanges,
  autoPatchFormOf,
  autoPatchProblem,
  autoPatchSource,
  toggleComponent,
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

test('the automatic patch form: off with every component by default, then what is in force', () => {
  const off = autoPatchFormOf({ readOnly: false, canEditSettings: true })
  assert.deepEqual(off, { enabled: false, components: ['talos', 'cilium', 'kubernetes'], window: '' })

  const on = autoPatchFormOf({
    readOnly: false,
    canEditSettings: true,
    effective: {
      autoPatch: { value: 'true', source: 'spec' },
      autoPatchComponents: { value: 'talos,kubernetes', source: 'spec' },
      autoPatchWindow: { value: '02:00-05:00', source: 'spec' },
    },
  })
  assert.deepEqual(on, { enabled: true, components: ['talos', 'kubernetes'], window: '02:00-05:00' })
  assert.equal(autoPatchSource({ readOnly: false, canEditSettings: true, effective: { autoPatch: { value: 'true', source: 'deployment' } } }), 'deployment')
  assert.equal(autoPatchSource({ readOnly: false, canEditSettings: true, spec: { autoPatchEnabled: false } }), 'spec')
})

test('components toggle in canonical order', () => {
  assert.deepEqual(toggleComponent(['talos', 'kubernetes'], 'cilium', true), ['talos', 'cilium', 'kubernetes'])
  assert.deepEqual(toggleComponent(['talos', 'cilium', 'kubernetes'], 'cilium', false), ['talos', 'kubernetes'])
})

test('the window must be HH:MM-HH:MM UTC, may wrap midnight, and a component must be selected', () => {
  const form = (window: string, components = ['talos']) => ({ enabled: true, components, window })
  assert.equal(autoPatchProblem(form('')), null)
  assert.equal(autoPatchProblem(form('02:00-05:00')), null)
  assert.equal(autoPatchProblem(form('22:00-02:00')), null)
  assert.equal(autoPatchProblem(form('24:00-02:00')), 'settings.badWindow')
  assert.equal(autoPatchProblem(form('2:00-5:00')), 'settings.badWindow')
  assert.equal(autoPatchProblem(form('03:00-03:00')), 'settings.badWindow')
  assert.equal(autoPatchProblem(form('night')), 'settings.badWindow')
  assert.equal(autoPatchProblem(form('', [])), 'settings.noComponent')
})

test('autoPatchChanges sends only what differs; every component is the empty list', () => {
  const start = { enabled: false, components: ['talos', 'cilium', 'kubernetes'], window: '' }
  assert.deepEqual(autoPatchChanges(start, start), {})
  assert.deepEqual(autoPatchChanges(start, { ...start, enabled: true }), { autoPatchEnabled: true })
  assert.deepEqual(autoPatchChanges(start, { ...start, components: ['talos'] }), { autoPatchComponents: 'talos' })
  assert.deepEqual(autoPatchChanges({ ...start, components: ['talos'] }, start), { autoPatchComponents: '' })
  assert.deepEqual(autoPatchChanges(start, { ...start, window: '02:00-05:00' }), { autoPatchWindow: '02:00-05:00' })
})
