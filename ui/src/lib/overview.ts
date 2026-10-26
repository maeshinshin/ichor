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
