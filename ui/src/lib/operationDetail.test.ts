import assert from 'node:assert/strict'
import { test } from 'node:test'
import { formatDuration, nodePath, spanMs, summaryFallback, visibleEvents } from './operationDetail.ts'

test('durations read as seconds, minutes or hours', () => {
  assert.equal(formatDuration(0), '0s')
  assert.equal(formatDuration(45_900), '45s')
  assert.equal(formatDuration(185_000), '3m 05s')
  assert.equal(formatDuration(2 * 3600_000 + 4 * 60_000 + 9_000), '2h 04m')
  assert.equal(formatDuration(-1), '-')
  assert.equal(formatDuration(undefined), '-')
  assert.equal(formatDuration(Number.NaN), '-')
})

test('a span without an end runs to now, without a start is unknown', () => {
  const start = new Date('2026-05-01T10:00:00Z')
  const end = new Date('2026-05-01T10:01:00Z')
  const now = new Date('2026-05-01T10:05:00Z')
  assert.equal(spanMs(start, end, now), 60_000)
  assert.equal(spanMs(start, undefined, now), 300_000)
  assert.equal(spanMs(undefined, end, now), undefined)
})

test('the summary falls back to the kind and the machine', () => {
  assert.equal(summaryFallback('op-1', '', undefined), 'op-1')
  assert.equal(summaryFallback('op-1', 'Talos', undefined), 'Talos')
  assert.equal(summaryFallback('op-1', 'Node', 'talos-02'), 'Node talos-02')
})

test('the warning filter keeps only Warnings', () => {
  const events = [{ type: 'Normal' }, { type: 'Warning' }, { type: 'Normal' }]
  assert.equal(visibleEvents(events, false).length, 3)
  assert.deepEqual(visibleEvents(events, true), [{ type: 'Warning' }])
})

test('a machine row links to its node page', () => {
  assert.equal(nodePath('talos-02'), '/nodes/talos-02')
})
