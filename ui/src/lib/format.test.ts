import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatResource } from './format.ts'

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
