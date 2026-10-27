import assert from 'node:assert/strict'
import { test } from 'node:test'
import { nodeTabSearch, parseNodeTab } from './nodeTabs.ts'

test('parseNodeTab defaults to summary on missing or unknown values', () => {
  assert.equal(parseNodeTab(''), 'summary')
  assert.equal(parseNodeTab('?tab=nope'), 'summary')
  assert.equal(parseNodeTab('?tab=talos'), 'talos')
  assert.equal(parseNodeTab('?x=1&tab=logs'), 'logs')
})

test('nodeTabSearch sets the tab, keeps other params and drops the default', () => {
  assert.equal(nodeTabSearch('', 'talos'), '?tab=talos')
  assert.equal(nodeTabSearch('?x=1', 'config'), '?x=1&tab=config')
  assert.equal(nodeTabSearch('?tab=talos', 'summary'), '')
})
