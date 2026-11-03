import assert from 'node:assert/strict'
import { test } from 'node:test'
import { t } from './i18n.ts'
import { componentOf, stateKey, stateTone, versionPair } from './versions.ts'

test('a state is good when up to date, a warning when behind, and neutral otherwise', () => {
  assert.equal(stateTone('up-to-date'), 'good')
  assert.equal(stateTone('behind'), 'warn')
  assert.equal(stateTone('ahead'), 'neutral')
  assert.equal(stateTone('unknown'), 'neutral')
  assert.equal(stateTone('a state from a newer server'), 'neutral')
})

test('a state is named in English, and an unknown one reads as unknown', () => {
  assert.equal(t(stateKey('up-to-date')), 'Up to date')
  assert.equal(t(stateKey('behind')), 'Behind')
  assert.equal(t(stateKey('ahead')), 'Ahead')
  assert.equal(t(stateKey('')), 'Unknown')
})

test('componentOf finds a component by its name, and nothing for a name the cluster does not report', () => {
  const list = [
    { name: 'talos', current: 'v1.12.4' },
    { name: 'cilium', current: '' },
  ]
  assert.equal(componentOf(list, 'talos')?.current, 'v1.12.4')
  assert.equal(componentOf(list, 'kubernetes'), undefined)
})

test('versionPair shows current to target, and a dash for a missing side', () => {
  assert.equal(versionPair('v1.12.4', 'v1.14.3'), 'v1.12.4 → v1.14.3')
  assert.equal(versionPair('', 'v1.14.3'), '- → v1.14.3')
  assert.equal(versionPair('v1.14.3', ''), 'v1.14.3')
  assert.equal(versionPair('', ''), '-')
})
