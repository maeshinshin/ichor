import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clipValue, formatResource, hardwareName } from './format.ts'

test('clipValue keeps short values and clips long ones', () => {
  assert.equal(clipValue('abc'), 'abc')
  assert.equal(clipValue('x'.repeat(250)), `${'x'.repeat(200)}…`)
})

test('formatResource shows memory and storage as sizes and cpu as cores', () => {
  assert.equal(formatResource('memory', '3992140Ki'), '3.8 GB')
  assert.equal(formatResource('ephemeral-storage', '8359247872'), '7.8 GB')
  assert.equal(formatResource('memory', '2Gi'), '2.0 GB')
  assert.equal(formatResource('cpu', '4'), '4 cores')
  assert.equal(formatResource('cpu', '1'), '1 core')
  assert.equal(formatResource('cpu', '1500m'), '1.5 cores')
  assert.equal(formatResource('pods', '110'), '110')
  assert.equal(formatResource('memory', ''), '-')
  assert.equal(formatResource('memory', 'weird'), 'weird')
})

test('hardwareName leaves out an empty or placeholder manufacturer or model', () => {
  assert.equal(hardwareName('Acme', 'None'), 'Acme')
  assert.equal(hardwareName('Dell', 'PowerEdge R640'), 'Dell PowerEdge R640')
  assert.equal(hardwareName('', ' '), '')
  assert.equal(hardwareName('None', 'None'), '')
})
