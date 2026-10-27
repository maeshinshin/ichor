import assert from 'node:assert/strict'
import { test } from 'node:test'
import { versionLabel } from './ichorVersion.ts'

test('shows nothing without an installation or a reported version', () => {
  assert.equal(versionLabel(null), null)
  assert.equal(versionLabel({ installedVersion: '', targetVersion: 'v0.2.0', updateAvailable: false }), null)
})

test('shows the installed version, and no update when the cluster does not know a newer one', () => {
  assert.deepEqual(versionLabel({ installedVersion: 'v0.1.0', targetVersion: 'v0.1.0', updateAvailable: false }), {
    version: 'v0.1.0',
    update: null,
  })
})

test('names the newer version only when the server says an update is available', () => {
  assert.deepEqual(versionLabel({ installedVersion: 'v0.1.0', targetVersion: 'v0.2.0', updateAvailable: true }), {
    version: 'v0.1.0',
    update: 'v0.2.0',
  })
  // A different target while an upgrade runs (updateAvailable false) is not advertised.
  assert.equal(versionLabel({ installedVersion: 'v0.1.0', targetVersion: 'v0.2.0', updateAvailable: false })?.update, null)
})
