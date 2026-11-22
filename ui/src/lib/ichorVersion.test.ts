import assert from 'node:assert/strict'
import { test } from 'node:test'
import { installerUnverified, releaseNotice, uiIsStale, versionLabel } from './ichorVersion.ts'

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

test('offers the release the controller found only when it is newer than the running version', () => {
  assert.equal(releaseNotice(null), null)
  assert.equal(releaseNotice({ latestVersion: '', releaseAvailable: false }), null)
  assert.equal(releaseNotice({ latestVersion: 'v0.2.0', releaseAvailable: false }), null)
  assert.equal(releaseNotice({ latestVersion: 'v0.2.0', releaseAvailable: true }), 'v0.2.0')
})

test('says nothing about a verified installer image, and names an unchecked one with its digest', () => {
  const quiet = { installerVerification: 'Verified', installerDigest: 'sha256:a', installerSignatureSkip: false, installerImageUnverified: false }
  assert.equal(installerUnverified(null), null)
  assert.equal(installerUnverified(quiet), null)
  assert.equal(installerUnverified({ ...quiet, installerVerification: '' }), null)
  assert.deepEqual(installerUnverified({ ...quiet, installerVerification: 'Skipped' }), {
    digest: 'sha256:a',
    pending: false,
    ever: false,
  })
  // The spec says skip but no upgrade has run since: the next one will not check.
  assert.deepEqual(installerUnverified({ ...quiet, installerVerification: '', installerDigest: '', installerSignatureSkip: true }), {
    digest: '',
    pending: true,
    ever: false,
  })
})

test('keeps saying an unchecked image once ran after a verified upgrade cleared the skip', () => {
  const verified = {
    installerVerification: 'Verified',
    installerDigest: 'sha256:b',
    installerSignatureSkip: false,
    installerImageUnverified: true,
  }
  assert.deepEqual(installerUnverified(verified), { digest: '', pending: false, ever: true })
})

test('a page is stale only when its bundle and the backend are both versioned and differ', () => {
  assert.equal(uiIsStale('v0.1.0', 'v0.2.0'), true)
  assert.equal(uiIsStale('v0.2.0', 'v0.2.0'), false)
  // No header from an older backend, and an unversioned build, are not a judgement.
  assert.equal(uiIsStale('v0.1.0', null), false)
  assert.equal(uiIsStale('dev', 'v0.2.0'), false)
  assert.equal(uiIsStale('v0.1.0', 'dev'), false)
})
