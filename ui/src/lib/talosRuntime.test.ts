import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  conditionTone,
  dbInUsePercent,
  etcdTone,
  formatAge,
  formatCpuSeconds,
  formatNanos,
  formatTaint,
  handshakeAge,
  podTone,
  sortPods,
  sortProcesses,
} from './talosRuntime.ts'

test('sortProcesses orders by the chosen key and breaks ties by pid', () => {
  const procs = [
    { pid: 3, residentBytes: 10n, cpuSeconds: 1 },
    { pid: 1, residentBytes: 50n, cpuSeconds: 1 },
    { pid: 2, residentBytes: 50n, cpuSeconds: 9 },
  ]
  assert.deepEqual(sortProcesses(procs, 'memory').map((p) => p.pid), [1, 2, 3])
  assert.deepEqual(sortProcesses(procs, 'cpu').map((p) => p.pid), [2, 1, 3])
  assert.deepEqual(procs.map((p) => p.pid), [3, 1, 2], 'the input is not reordered')
})

test('durations are written in the largest sensible unit', () => {
  assert.equal(formatCpuSeconds(0), '0s')
  assert.equal(formatCpuSeconds(12.34), '12.3s')
  assert.equal(formatCpuSeconds(125), '2m 5s')
  assert.equal(formatCpuSeconds(7500), '2h 5m')
  assert.equal(formatAge(45), '45s')
  assert.equal(formatAge(720n), '12m')
  assert.equal(formatAge(3 * 3600), '3h')
  assert.equal(formatAge(5 * 86400), '5d')
  assert.equal(formatAge(-1), '-')
  assert.equal(formatNanos(0), '0 ms')
  assert.equal(formatNanos(2_500_000n), '2.5 ms')
  assert.equal(formatNanos(-3_000_000_000), '-3.00 s')
})

test('etcd is bad with an alarm, an error or no leader', () => {
  const ok = { alarms: [], statusErrors: [], leaderId: '00ab', dbSizeBytes: 100n, dbSizeInUseBytes: 40n }
  assert.equal(etcdTone(ok), 'ok')
  assert.equal(etcdTone({ ...ok, alarms: ['00ab: NOSPACE'] }), 'bad')
  assert.equal(etcdTone({ ...ok, statusErrors: ['x'] }), 'bad')
  assert.equal(etcdTone({ ...ok, leaderId: '0000000000000000' }), 'bad')
  assert.equal(dbInUsePercent(ok), 40)
  assert.equal(dbInUsePercent({ dbSizeBytes: 0n, dbSizeInUseBytes: 0n }), -1)
})

test('pods with trouble sort first and restarts alone are not red', () => {
  const pod = (name: string, phase: string, ready: number, restarts = 0) => ({
    namespace: 'ns', name, phase, readyContainers: ready, totalContainers: 2, restarts,
  })
  assert.equal(podTone(pod('a', 'Running', 2)), 'ok')
  assert.equal(podTone(pod('a', 'Running', 1)), 'bad')
  assert.equal(podTone(pod('a', 'Pending', 0)), 'neutral')
  assert.equal(podTone(pod('a', 'Failed', 0)), 'bad')
  assert.equal(podTone(pod('a', 'Succeeded', 0)), 'neutral')
  const sorted = sortPods([pod('c', 'Running', 2), pod('b', 'Running', 2, 4), pod('a', 'Running', 1)])
  assert.deepEqual(sorted.map((p) => p.name), ['a', 'b', 'c'])
})

test('node conditions: Ready must be True, pressure must be False', () => {
  assert.equal(conditionTone({ type: 'Ready', status: 'True' }), 'ok')
  assert.equal(conditionTone({ type: 'Ready', status: 'Unknown' }), 'bad')
  assert.equal(conditionTone({ type: 'MemoryPressure', status: 'False' }), 'ok')
  assert.equal(conditionTone({ type: 'DiskPressure', status: 'True' }), 'bad')
  assert.equal(conditionTone({ type: 'Custom', status: 'Unknown' }), 'neutral')
})

test('taints print as key=value:effect and handshakes as an age', () => {
  assert.equal(formatTaint({ key: 'a', value: 'b', effect: 'NoSchedule' }), 'a=b:NoSchedule')
  assert.equal(formatTaint({ key: 'a', value: '', effect: 'NoExecute' }), 'a:NoExecute')
  assert.equal(handshakeAge(0n, 1_000_000), '-')
  assert.equal(handshakeAge(940n, 1_000_000), '1m ago')
})
