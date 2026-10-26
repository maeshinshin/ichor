import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  argoApplicationUrl,
  externalBase,
  grafanaDashboardUrl,
  healthBadge,
  needsAttention,
  syncBadge,
  trackingLabel,
} from './gitops.ts'

test('only http(s) bases are linkable, and trailing slashes go', () => {
  assert.equal(externalBase('https://grafana.example.com/'), 'https://grafana.example.com')
  assert.equal(externalBase('http://hubble.local:8080'), 'http://hubble.local:8080')
  assert.equal(externalBase('javascript:alert(1)'), null)
  assert.equal(externalBase('//evil.example'), null)
  assert.equal(externalBase(''), null)
})

test('a sub-path base keeps its path', () => {
  assert.equal(grafanaDashboardUrl('https://example.com/grafana/'), 'https://example.com/grafana/d/ichor-overview')
})

test('no registered URL means no link', () => {
  assert.equal(grafanaDashboardUrl(''), null)
  assert.equal(argoApplicationUrl('', { namespace: 'argocd', name: 'a' }), null)
})

test('an Application in argocd is addressed by name, elsewhere by namespace and name', () => {
  assert.equal(
    argoApplicationUrl('https://argo.example.com', { namespace: 'argocd', name: 'prod-root' }),
    'https://argo.example.com/applications/prod-root',
  )
  assert.equal(
    argoApplicationUrl('https://argo.example.com/', { namespace: 'team a', name: 'x' }),
    'https://argo.example.com/applications/team%20a/x',
  )
})

test('state words map to a badge, unknown ones stay neutral', () => {
  assert.equal(syncBadge('Synced'), 'secondary')
  assert.equal(syncBadge('OutOfSync'), 'destructive')
  assert.equal(syncBadge('Whatever'), 'outline')
  assert.equal(healthBadge('Healthy'), 'secondary')
  assert.equal(healthBadge('Degraded'), 'destructive')
  assert.equal(healthBadge('Progressing'), 'outline')
})

test('attention covers errors, drift and a bad health', () => {
  const ok = { error: '', syncStatus: 'Synced', healthStatus: 'Healthy' }
  assert.equal(needsAttention(ok), false)
  assert.equal(needsAttention({ ...ok, syncStatus: 'OutOfSync' }), true)
  assert.equal(needsAttention({ ...ok, healthStatus: 'Degraded' }), true)
  assert.equal(needsAttention({ ...ok, error: 'x' }), true)
})

test('an untracked CR says it is not in Git', () => {
  assert.equal(trackingLabel(''), 'Not in Git')
  assert.equal(trackingLabel('Argo CD'), 'Argo CD')
})
