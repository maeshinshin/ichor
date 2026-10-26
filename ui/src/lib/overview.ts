import type { OpState } from './operations.ts'

export type MachineLite = { approval: 'approved' | 'pending' | 'rejected'; ready: string }

// machineCounts is the Overview's machines-by-state tile: approval first, then
// the Ready condition of the approved ones.
export function machineCounts(ms: readonly MachineLite[]) {
  const c = { total: ms.length, ready: 0, notReady: 0, unknown: 0, pending: 0, rejected: 0 }
  for (const m of ms) {
    if (m.approval === 'pending') c.pending++
    else if (m.approval === 'rejected') c.rejected++
    else if (m.ready === 'True') c.ready++
    else if (m.ready === 'False') c.notReady++
    else c.unknown++
  }
  return c
}

// clusterReadiness counts the clusters whose Ready condition is True.
export function clusterReadiness(readies: readonly string[]) {
  return { total: readies.length, ready: readies.filter((r) => r === 'True').length }
}

// operationsLine lists which statements the Operations tile shows, in order.
// "allCompleted" only when nothing is running, failed or paused.
export function operationsLine(c: ReturnType<typeof operationCounts>): Array<'none' | 'allCompleted' | 'running' | 'failed' | 'paused'> {
  if (c.total === 0) return ['none']
  const out: Array<'running' | 'failed' | 'paused'> = []
  if (c.running > 0) out.push('running')
  if (c.failed > 0) out.push('failed')
  if (c.paused > 0) out.push('paused')
  return out.length > 0 ? out : ['allCompleted']
}

export function operationCounts(states: readonly OpState[]) {
  const c = { total: states.length, running: 0, failed: 0, paused: 0, done: 0 }
  for (const s of states) {
    if (s === 'running') c.running++
    else if (s === 'failed') c.failed++
    else if (s === 'paused') c.paused++
    else if (s === 'done') c.done++
  }
  return c
}
