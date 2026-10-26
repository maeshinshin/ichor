import { conditionTone } from './conditions.ts'
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

type Cond = { type: string; status: string }

// clusterIsReady: the Ready condition when the cluster reports one. A TalosCluster
// reports Transitioning but no Ready today, so then it is ready when it is not
// transitioning and no health-style condition (Available, Healthy ...) is False.
export function clusterIsReady(conds: readonly Cond[]): boolean {
  const ready = conds.find((c) => c.type === 'Ready')
  if (ready) return ready.status === 'True'
  if (conds.some((c) => c.type === 'Transitioning' && c.status === 'True')) return false
  return !conds.some((c) => conditionTone(c.type, c.status) === 'bad')
}

// clusterReadiness counts the ready clusters.
export function clusterReadiness(clusters: readonly (readonly Cond[])[]) {
  return { total: clusters.length, ready: clusters.filter(clusterIsReady).length }
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
