import assert from 'node:assert/strict'
import { test } from 'node:test'
import { configSections, previewKey } from './patchDocs.ts'

test('previewKey changes with anything the preview depends on, and with nothing else', () => {
  const base = { cluster: 'ns/c', layer: 2, priority: 0, target: 'role:worker', config: '{}' }
  assert.equal(previewKey(base), previewKey({ ...base }))
  assert.notEqual(previewKey(base), previewKey({ ...base, priority: 1 }))
  assert.notEqual(previewKey(base), previewKey({ ...base, config: '{"machine":{}}' }))
})

test('configSections lists the sections a config sets, and none for text that is not an object', () => {
  assert.deepEqual(configSections('{"machine":{},"cluster":{}}'), ['cluster', 'machine'])
  assert.deepEqual(configSections('{}'), [])
  assert.deepEqual(configSections('[]'), [])
  assert.deepEqual(configSections('{oops'), [])
})
