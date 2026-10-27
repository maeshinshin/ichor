import { conditionTone } from './conditions.ts'

// The one place that decides the cluster's status, for the top bar's badge and the Overview row
// (docs/adr/0097).
export type Health = 'ready' | 'degraded' | 'upgrading'

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

// clusterHealth: a rollout or a running operation is "upgrading" whatever else is true; else
// readiness decides.
export function clusterHealth(conds: readonly Cond[], rolling: boolean, opRunning: boolean): Health {
  if (rolling || opRunning) return 'upgrading'
  return clusterIsReady(conds) ? 'ready' : 'degraded'
}
