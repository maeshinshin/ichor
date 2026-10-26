import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createBatcher, LogBuffer, truncateMessage } from './logBuffer.ts'

test('keeps only the newest lines once over capacity', () => {
  const b = new LogBuffer<number>(3)
  b.push([1, 2])
  b.push([3, 4, 5])
  assert.deepEqual(
    b.entries().map((e) => e.item),
    [3, 4, 5],
  )
})

test('a single push larger than the capacity keeps its tail with increasing ids', () => {
  const b = new LogBuffer<number>(2)
  b.push([1, 2, 3, 4, 5])
  const entries = b.entries()
  assert.deepEqual(
    entries.map((e) => e.item),
    [4, 5],
  )
  assert.ok(entries[0].id < entries[1].id, 'ids increase')
  assert.deepEqual(
    entries.map((e) => e.id),
    [3, 4],
  )
})

test('ids keep growing across eviction so list keys stay stable', () => {
  const b = new LogBuffer<string>(2)
  b.push(['a', 'b', 'c'])
  assert.deepEqual(
    b.entries().map((e) => e.id),
    [1, 2],
  )
})

test('clear empties the buffer', () => {
  const b = new LogBuffer<number>(2)
  b.push([1])
  b.clear()
  assert.deepEqual(b.entries(), [])
})

test('truncateMessage leaves short messages and cuts long ones', () => {
  assert.deepEqual(truncateMessage('short', 10), { text: 'short', truncated: false })
  const r = truncateMessage('x'.repeat(50), 10)
  assert.equal(r.text, 'x'.repeat(10))
  assert.equal(r.truncated, true)
})

test('the batcher flushes many pushes in one scheduled flush', () => {
  const flushed: number[][] = []
  const scheduled: (() => void)[] = []
  const batcher = createBatcher<number>(
    (items) => flushed.push(items),
    (cb) => {
      scheduled.push(cb)
      return () => {}
    },
  )
  for (let i = 0; i < 1000; i++) batcher.push(i)
  assert.equal(scheduled.length, 1, 'one flush scheduled for 1000 lines')
  assert.equal(flushed.length, 0)
  scheduled[0]()
  assert.equal(flushed.length, 1)
  assert.equal(flushed[0].length, 1000)
  batcher.push(1000)
  assert.equal(scheduled.length, 2, 'a new flush is scheduled after the first ran')
})

test('cancel drops pending items and the scheduled flush', () => {
  let cancelled = 0
  const flushed: number[][] = []
  const batcher = createBatcher<number>(
    (items) => flushed.push(items),
    () => () => {
      cancelled++
    },
  )
  batcher.push(1)
  batcher.cancel()
  assert.equal(cancelled, 1)
  assert.deepEqual(flushed, [])
})
