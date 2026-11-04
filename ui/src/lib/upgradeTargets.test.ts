import assert from 'node:assert/strict'
import { test } from 'node:test'
import { hasPatch, patchComponents, pickerOptions, typedVersion, unavailableNotes, type TargetList } from './upgradeTargets.ts'

const list: TargetList = {
  recommended: ['v1.14.3'],
  patch: ['v1.14.2', 'v1.14.1'],
  nextMinor: ['v1.15.0', 'v1.15.1'],
  default: 'v1.14.3',
  available: true,
  unavailableReason: '',
}

test('the picker groups the candidates in order: Recommended, Patch, Next minor', () => {
  assert.deepEqual(pickerOptions(list, '', 'v1.14.3'), [
    { key: 'recommended', items: ['v1.14.3'] },
    { key: 'patch', items: ['v1.14.2', 'v1.14.1'] },
    { key: 'nextMinor', items: ['v1.15.0', 'v1.15.1'] },
  ])
})

test('a group with no match is left out', () => {
  assert.deepEqual(pickerOptions(list, '1.15', 'v1.14.3'), [{ key: 'nextMinor', items: ['v1.15.0', 'v1.15.1'] }])
})

test('typing filters case-insensitively and ignores a leading v on either side', () => {
  assert.deepEqual(pickerOptions(list, '1.14.1', 'v1.14.3'), [{ key: 'patch', items: ['v1.14.1'] }])
  assert.deepEqual(pickerOptions(list, 'V1.14', 'v1.14.3'), [
    { key: 'recommended', items: ['v1.14.3'] },
    { key: 'patch', items: ['v1.14.2', 'v1.14.1'] },
  ])
})

test('the selected version typed back into the input does not filter the list', () => {
  assert.deepEqual(pickerOptions(list, 'v1.14.3', 'v1.14.3'), pickerOptions(list, '', 'v1.14.3'))
})

test('a typed final version the list does not hold is offered first as Use', () => {
  assert.deepEqual(pickerOptions(list, '1.14.4', 'v1.14.3'), [{ key: 'typed', items: ['v1.14.4'] }])
})

test('a typed version the list holds is not offered again as Use', () => {
  const groups = pickerOptions(list, 'v1.15.0', 'v1.14.3')
  assert.equal(groups.some((g) => g.key === 'typed'), false)
  assert.deepEqual(groups, [{ key: 'nextMinor', items: ['v1.15.0'] }])
})

test('text that is not a final version is not offered as Use', () => {
  assert.equal(typedVersion('1.14'), null)
  assert.equal(typedVersion('v1.14.3-rc.1'), null)
  assert.equal(typedVersion(' 1.14.4 '), 'v1.14.4')
  assert.equal(pickerOptions(list, '1.14', 'v1.14.3').some((g) => g.key === 'typed'), false)
})

test('the typed entry still works without a list, while loading or unavailable', () => {
  assert.deepEqual(pickerOptions(null, '1.2.3', ''), [{ key: 'typed', items: ['v1.2.3'] }])
  assert.deepEqual(pickerOptions(null, '', ''), [])
  const offline: TargetList = { recommended: [], patch: [], nextMinor: [], default: '', available: false, unavailableReason: 'offline' }
  assert.deepEqual(pickerOptions(offline, '', ''), [])
})

test('a patch hint shows only for an available list with a newer patch', () => {
  assert.equal(hasPatch(list), true)
  assert.equal(hasPatch({ ...list, patch: [] }), false)
  assert.equal(hasPatch({ ...list, available: false }), false)
  assert.equal(hasPatch(null), false)
})

test('the patch attention names the components that have a patch, in order, and nothing when the fetch failed', () => {
  const failed = null
  assert.deepEqual(patchComponents({ talos: list, kubernetes: list, cilium: list }), ['Talos', 'Kubernetes', 'Cilium'])
  assert.deepEqual(patchComponents({ talos: list, kubernetes: { ...list, patch: [] }, cilium: null }), ['Talos'])
  assert.deepEqual(patchComponents({ talos: failed, kubernetes: failed, cilium: { ...list, patch: [] } }), [])
})

test('the unavailable lists are named with their reason, grouped by reason', () => {
  const off: TargetList = { ...list, patch: [], available: false, unavailableReason: 'check is off' }
  const failed: TargetList = { ...list, patch: [], available: false, unavailableReason: 'quay unreachable' }
  assert.deepEqual(unavailableNotes({ talos: list, kubernetes: list, cilium: list }), [])
  assert.deepEqual(unavailableNotes({ talos: null, kubernetes: null, cilium: null }), [])
  assert.deepEqual(unavailableNotes({ talos: off, kubernetes: off, cilium: failed }), [
    { components: ['Talos', 'Kubernetes'], reason: 'check is off' },
    { components: ['Cilium'], reason: 'quay unreachable' },
  ])
})
