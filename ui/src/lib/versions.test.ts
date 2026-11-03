import assert from 'node:assert/strict'
import { test } from 'node:test'
import { t } from './i18n.ts'
import { componentOf, stateKey, stateTone, stepLabel, upgradeLabel, versionPair } from './versions.ts'

const release = { versionContract: '2', ciliumChartVersion: '1.18.3' }

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

test('talos names the release set version and the contract it moves to', () => {
  assert.equal(
    stepLabel({ step: 'talos', from: 'v1.12.4', to: 'v1.14.3' }, release),
    'Talos v1.12.4 → v1.14.3: each machine is upgraded and reboots, one at a time; the version contract moves to 2.',
  )
})

test('config is the same line whatever the versions are', () => {
  assert.equal(
    stepLabel({ step: 'config', from: '', to: '' }, release),
    'Configuration: the pending change the new contract needs is applied; a machine that needs a reboot reboots.',
  )
})

test('kubernetes names the two releases and says nothing reboots', () => {
  assert.equal(
    stepLabel({ step: 'kubernetes', from: 'v1.31.2', to: 'v1.32.0' }, release),
    'Kubernetes v1.31.2 → v1.32.0: control plane components and kubelets restart, no reboot.',
  )
})

test('cilium takes its target from the release set when the step leaves it empty', () => {
  assert.equal(stepLabel({ step: 'cilium', from: '1.17.0', to: '' }, release), 'Cilium 1.17.0 → 1.18.3: the Cilium Helm release is upgraded.')
  assert.equal(stepLabel({ step: 'cilium', from: '', to: '1.18.3' }, release), 'Cilium - → 1.18.3: the Cilium Helm release is upgraded.')
})

test('an unknown step shows its name as the server sent it', () => {
  assert.equal(stepLabel({ step: 'shiny-new', from: '', to: '' }, release), 'shiny-new')
})

test('the button names the Ichor version, or says so when that version is not known', () => {
  assert.equal(upgradeLabel('v0.3.0'), 'Bring the cluster to the versions of Ichor v0.3.0')
  assert.equal(upgradeLabel(null), 'Bring the cluster to the versions of this Ichor')
  assert.equal(upgradeLabel(''), 'Bring the cluster to the versions of this Ichor')
})
