// Pure helpers for the Talos live-state cards; no DOM, no
// preact, so they run under `node --test`.

import type { Tone } from './talosDetails.ts'

export type ProcessSort = 'memory' | 'cpu'

type ProcessLike = { pid: number; residentBytes: bigint; cpuSeconds: number }

// Heaviest first by the chosen key, ties broken by pid so the order is stable.
export function sortProcesses<T extends ProcessLike>(procs: readonly T[], by: ProcessSort): T[] {
  const key = (p: T): number => (by === 'cpu' ? p.cpuSeconds : Number(p.residentBytes))
  return [...procs].sort((a, b) => key(b) - key(a) || a.pid - b.pid)
}

// Cumulative CPU time as h/m/s, since it is not a percentage.
export function formatCpuSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0s'
  if (seconds < 60) return `${seconds.toFixed(1)}s`
  const s = Math.floor(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  return h > 0 ? `${h}h ${m}m` : `${m}m ${s % 60}s`
}

// Age of an object in the largest sensible unit: 45s, 12m, 3h, 5d.
export function formatAge(seconds: bigint | number): string {
  const n = Number(seconds)
  if (!Number.isFinite(n) || n < 0) return '-'
  if (n < 60) return `${Math.floor(n)}s`
  if (n < 3600) return `${Math.floor(n / 60)}m`
  if (n < 86400) return `${Math.floor(n / 3600)}h`
  return `${Math.floor(n / 86400)}d`
}

export function formatNanos(ns: bigint | number): string {
  const n = Number(ns)
  if (!Number.isFinite(n) || n === 0) return '0 ms'
  const abs = Math.abs(n)
  if (abs < 1e6) return `${(n / 1e3).toFixed(0)} µs`
  if (abs < 1e9) return `${(n / 1e6).toFixed(1)} ms`
  return `${(n / 1e9).toFixed(2)} s`
}

type EtcdLike = {
  alarms: readonly string[]
  statusErrors: readonly string[]
  leaderId: string
  dbSizeBytes: bigint
  dbSizeInUseBytes: bigint
}

// Alarms and member errors are bad; no known leader is bad; otherwise ok.
export function etcdTone(e: EtcdLike): Tone {
  if (e.alarms.length > 0 || e.statusErrors.length > 0) return 'bad'
  if (!e.leaderId || /^0+$/.test(e.leaderId)) return 'bad'
  return 'good'
}

// Share of the database file that is live data; a low value means a
// defragmentation would give the space back. -1 when unknown.
export function dbInUsePercent(e: Pick<EtcdLike, 'dbSizeBytes' | 'dbSizeInUseBytes'>): number {
  const total = Number(e.dbSizeBytes)
  if (!(total > 0)) return -1
  return Math.round((Number(e.dbSizeInUseBytes) / total) * 100)
}

type PodLike = { phase: string; readyContainers: number; totalContainers: number; restarts: number }

// A pod that is not running or finished, or is running with containers not
// ready, is bad; restarts alone are shown but not red.
export function podTone(p: PodLike): Tone {
  if (p.phase === 'Succeeded') return 'neutral'
  if (p.phase === 'Failed' || p.phase === 'Unknown') return 'bad'
  if (p.phase === 'Running') return p.readyContainers < p.totalContainers ? 'bad' : 'good'
  return 'neutral'
}

// Pods with trouble first, then running ones, then the rest (finished pods
// last), each by namespace and name.
export function sortPods<T extends PodLike & { namespace: string; name: string }>(pods: readonly T[]): T[] {
  const rank = (p: T) => (podTone(p) === 'bad' ? 0 : p.restarts > 0 ? 1 : p.phase === 'Running' ? 2 : 3)
  return [...pods].sort((a, b) => rank(a) - rank(b) || a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name))
}

const GOOD_WHEN_TRUE = new Set(['Ready'])

// Ready=True is ok; any pressure or unavailable condition that is True, or
// Ready that is not True, is bad.
export function conditionTone(c: { type: string; status: string }): Tone {
  if (GOOD_WHEN_TRUE.has(c.type)) return c.status === 'True' ? 'good' : 'bad'
  return c.status === 'False' ? 'good' : c.status === 'True' ? 'bad' : 'neutral'
}

export function formatTaint(t: { key: string; value: string; effect: string }): string {
  return `${t.key}${t.value ? `=${t.value}` : ''}:${t.effect}`
}

// Seconds since a handshake, or a dash when there has been none.
export function handshakeAge(unix: bigint | number, nowMs: number): string {
  const at = Number(unix)
  if (!(at > 0)) return '-'
  return `${formatAge(Math.max(0, Math.floor(nowMs / 1000) - at))} ago`
}
