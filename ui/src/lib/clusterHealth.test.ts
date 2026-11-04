import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clusterHealth, clusterIsReady } from './clusterHealth.ts'

test('clusterIsReady uses Ready when there is one, else steady and not unhealthy', () => {
  assert.equal(clusterIsReady([{ type: 'Ready', status: 'True' }]), true)
  assert.equal(clusterIsReady([{ type: 'Ready', status: 'Unknown' }]), false)
  assert.equal(clusterIsReady([{ type: 'Transitioning', status: 'False' }]), true)
  assert.equal(clusterIsReady([]), true)
  assert.equal(clusterIsReady([{ type: 'Transitioning', status: 'True' }]), false)
  assert.equal(clusterIsReady([{ type: 'Available', status: 'False' }]), false)
})

test('clusterHealth: upgrading beats ready and degraded', () => {
  const ready = [{ type: 'Ready', status: 'True' }]
  const down = [{ type: 'Ready', status: 'False' }]
  assert.equal(clusterHealth(ready, false, false), 'ready')
  assert.equal(clusterHealth(down, false, false), 'degraded')
  assert.equal(clusterHealth(down, true, false), 'upgrading')
  assert.equal(clusterHealth(ready, false, true), 'upgrading')
})

test('the live demo condition set (CiliumUpgradeAvailable=False) is ready', () => {
  const demo = [
    { type: 'Transitioning', status: 'False' },
    { type: 'CNIHandover', status: 'True' },
    { type: 'CNIValuesApplied', status: 'True' },
    { type: 'CiliumUpgradeAvailable', status: 'False' },
    { type: 'CertificatesExpiring', status: 'False' },
  ]
  assert.equal(clusterHealth(demo, false, false), 'ready')
})

test('a cluster still waiting for kubelet serving certificates is ready (ADR 0125)', () => {
  const waiting = [
    { type: 'KubeletServingCertificates', status: 'False' },
    { type: 'KubeletServingVerified', status: 'False' },
  ]
  assert.equal(clusterHealth(waiting, false, false), 'ready')
})

test('a cluster with only CertificatesExpiring=False and Transitioning=False is ready', () => {
  const demo = [
    { type: 'CertificatesExpiring', status: 'False' },
    { type: 'Transitioning', status: 'False' },
  ]
  assert.equal(clusterHealth(demo, false, false), 'ready')
})
