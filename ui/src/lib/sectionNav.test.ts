import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeSection } from './sectionNav.ts'

const tops = [
  { id: 'a', top: 0 },
  { id: 'b', top: 400 },
  { id: 'c', top: 900 },
]

test('the last section whose top has passed the offset is active', () => {
  assert.equal(activeSection(tops, 100), 'a')
  assert.equal(activeSection(tops, 400), 'b')
  assert.equal(activeSection(tops, 1000), 'c')
})

test('before the first section, the first is active; none gives empty', () => {
  assert.equal(activeSection([{ id: 'a', top: 300 }], 0), 'a')
  assert.equal(activeSection([], 0), '')
})
