import assert from 'node:assert/strict'
import { test } from 'node:test'
import { activeSection, pickActive } from './sectionNav.ts'

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

test('a section stopped at the scroll margin counts as reached', () => {
  const s = [{ id: 'a', top: -300 }, { id: 'b', top: 96 }]
  assert.equal(pickActive(s, 96, false, null), 'b')
  assert.equal(pickActive([{ id: 'a', top: -300 }, { id: 'b', top: 97.5 }], 96, false, null), 'b')
})

test('a clicked section wins over the at-bottom rule until released', () => {
  const s = [{ id: 'a', top: -500 }, { id: 'b', top: 300 }, { id: 'c', top: 500 }]
  assert.equal(pickActive(s, 96, true, 'b'), 'b')
  assert.equal(pickActive(s, 96, true, null), 'c')
  assert.equal(pickActive(s, 96, true, 'gone'), 'c')
})
