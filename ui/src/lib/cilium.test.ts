import assert from 'node:assert/strict'
import { test } from 'node:test'
import { canApply, classifyDiffLine, diffLines } from './cilium.ts'

test('diff lines are classified, and file headers are not additions', () => {
  assert.equal(classifyDiffLine('+++ proposed'), 'meta')
  assert.equal(classifyDiffLine('--- current'), 'meta')
  assert.equal(classifyDiffLine('@@ -1,3 +1,4 @@'), 'hunk')
  assert.equal(classifyDiffLine('+  mode: strict'), 'add')
  assert.equal(classifyDiffLine('-  mode: off'), 'del')
  assert.equal(classifyDiffLine(' unchanged'), 'ctx')
  assert.equal(classifyDiffLine(''), 'ctx')
})

test('diffLines drops the trailing newline and keeps blank context lines', () => {
  assert.deepEqual(diffLines(''), [])
  assert.deepEqual(diffLines('@@ -1 +1 @@\n-a\n+b\n\n'), [
    { kind: 'hunk', text: '@@ -1 +1 @@' },
    { kind: 'del', text: '-a' },
    { kind: 'add', text: '+b' },
    { kind: 'ctx', text: '' },
  ])
})

test('apply needs the reviewed text, valid, with a change', () => {
  const r = { text: 'a: 1', valid: true, diff: '+a' }
  assert.equal(canApply('a: 1', r), true)
  assert.equal(canApply('a: 2', r), false)
  assert.equal(canApply('a: 1', null), false)
  assert.equal(canApply('a: 1', { ...r, valid: false }), false)
  assert.equal(canApply('a: 1', { ...r, diff: '' }), false)
})
