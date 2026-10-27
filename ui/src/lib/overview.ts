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

// activeCondition is the message of a condition of this type while it is True, else null.
function activeCondition(conds: readonly { type: string; status: string; message?: string }[], type: string): string | null {
  const c = conds.find((x) => x.type === type && x.status === 'True')
  return c ? c.message || '' : null
}

// expiringCertificatesMessage is the message of the CertificatesExpiring condition while it is True
// (a CA is inside the controller's warning window or expired), else null (ADR 0063).
export function expiringCertificatesMessage(conds: readonly { type: string; status: string; message?: string }[]): string | null {
  return activeCondition(conds, 'CertificatesExpiring')
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

type Snapshots = readonly { snapshots: readonly { time?: { seconds: bigint } }[] }[]

// timeAgo is how long ago a moment was, in the coarsest unit that is at least 1 ("3 h ago").
export function timeAgo(nowMs: number, thenMs: number): string {
  const s = Math.max(0, Math.floor((nowMs - thenMs) / 1000))
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)} min ago`
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`
  return `${Math.floor(s / 86400)} d ago`
}

// newestSnapshotSeconds is the time of the newest snapshot over all backups, or null when there is none.
export function newestSnapshotSeconds(backups: Snapshots): number | null {
  let newest: number | null = null
  for (const b of backups) {
    for (const s of b.snapshots) {
      if (s.time && (newest === null || Number(s.time.seconds) > newest)) newest = Number(s.time.seconds)
    }
  }
  return newest
}

// backupsValue is the Backups tile: the age of the newest snapshot, "No snapshot yet" while a backup
// exists without one, or "Not configured" when there is no backup (null: not loaded yet, a dash).
export function backupsValue(backups: Snapshots | null, nowMs: number): string {
  if (backups === null) return '-'
  if (backups.length === 0) return 'Not configured'
  const newest = newestSnapshotSeconds(backups)
  return newest === null ? 'No snapshot yet' : timeAgo(nowMs, newest * 1000)
}

export type TileTarget = { kind: 'route'; to: string } | { kind: 'anchor'; id: string }

// tileTargets is where each summary tile leads: the running operation's page when there is one.
export function tileTargets(runningOperation: string | null): Record<'nodes' | 'operation' | 'backups' | 'versions', TileTarget> {
  return {
    nodes: { kind: 'route', to: '/nodes' },
    operation: { kind: 'route', to: runningOperation ? `/operations/${runningOperation}` : '/operations' },
    backups: { kind: 'route', to: '/backups' },
    versions: { kind: 'anchor', id: 'versions' },
  }
}

// nodesValue is "x / y ready".
export const nodesValue = (ready: number, total: number): string => `${ready} / ${total} ready`

// versionsValue is "Talos v1.14.3 · K8s v1.37.1"; a version that already starts with "v" gets no second one.
export function versionsValue(talos: string, kubernetes: string): string {
  const v = (s: string) => (s === '' ? '-' : s.startsWith('v') ? s : `v${s}`)
  return `Talos ${v(talos)} · K8s ${v(kubernetes)}`
}
