import { conditionTone } from './conditions.ts'

export type MachineLite = { approval: 'approved' | 'pending' | 'rejected'; ready: string }

// machineCounts counts nodes by state: approval first, then the Ready condition
// of the approved ones.
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

// activeCondition is the message of a condition of this type while it is True, else null.
function activeCondition(conds: readonly { type: string; status: string; message?: string }[], type: string): string | null {
  const c = conds.find((x) => x.type === type && x.status === 'True')
  return c ? c.message || '' : null
}

// expiringCertificatesMessage is the message of the CertificatesExpiring condition while it is True
// (a CA is inside the controller's warning window or expired), else null.
export function expiringCertificatesMessage(conds: readonly { type: string; status: string; message?: string }[]): string | null {
  return activeCondition(conds, 'CertificatesExpiring')
}

export type ClusterStatus = 'ready' | 'degraded' | 'upgrading'

// clusterStatus is the badge at the top of the home page: a rollout or a running
// operation is "upgrading" whatever else is true; else Ready decides.
export function clusterStatus(conds: readonly Cond[], rolling: boolean, opRunning: boolean): ClusterStatus {
  if (rolling || opRunning) return 'upgrading'
  return clusterIsReady(conds) ? 'ready' : 'degraded'
}

export type Attention =
  | { kind: 'certs'; message: string }
  | { kind: 'backupStale'; message: string }
  | { kind: 'pending'; count: number }
  | { kind: 'config'; count: number }
  | { kind: 'opFailed' }
  | { kind: 'opPaused' }

// attentionItems lists, in order, only what applies to the cluster now.
export function attentionItems(i: {
  conditions: readonly { type: string; status: string; message?: string }[]
  pending: number
  configWaiting: number
  opState: string | null
}): Attention[] {
  const out: Attention[] = []
  if (i.opState === 'failed') out.push({ kind: 'opFailed' })
  if (i.opState === 'paused') out.push({ kind: 'opPaused' })
  const certs = expiringCertificatesMessage(i.conditions)
  if (certs !== null) out.push({ kind: 'certs', message: certs })
  const stale = activeCondition(i.conditions, 'BundleBackupStale')
  if (stale !== null) out.push({ kind: 'backupStale', message: stale })
  if (i.pending > 0) out.push({ kind: 'pending', count: i.pending })
  if (i.configWaiting > 0) out.push({ kind: 'config', count: i.configWaiting })
  return out
}
