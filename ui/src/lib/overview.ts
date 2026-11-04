import { newestSnapshot } from './backups.ts'

// activeCondition is the message of a condition of this type while it is True, else null.
function activeCondition(conds: readonly { type: string; status: string; message?: string }[], type: string): string | null {
  const c = conds.find((x) => x.type === type && x.status === 'True')
  return c ? c.message || '' : null
}

export type Attention =
  | { kind: 'certs'; message: string }
  | { kind: 'backupStale'; message: string }
  | { kind: 'clientsNotRestarted'; message: string }
  | { kind: 'pending'; count: number }
  | { kind: 'config'; count: number }
  | { kind: 'opFailed' }
  | { kind: 'opPaused' }
  | { kind: 'patch'; components: string[] }
  | { kind: 'release'; version: string }
  | { kind: 'autoPatchHalted'; operation: string }

// attentionItems lists, in order, only what applies to the cluster now.
export function attentionItems(i: {
  conditions: readonly { type: string; status: string; message?: string }[]
  pending: number
  configWaiting: number
  opState: string | null
  // patches names the components with a newer patch of their minor; one item lists them (docs/adr/0115).
  patches?: string[]
  // release is the newer Ichor release the cluster knows, or null (docs/adr/0114).
  release?: string | null
  // autoPatchHalted is the automatic operation (namespace/name) that stopped automatic patching, or empty (docs/adr/0116).
  autoPatchHalted?: string
}): Attention[] {
  const out: Attention[] = []
  if (i.opState === 'failed') out.push({ kind: 'opFailed' })
  if (i.opState === 'paused') out.push({ kind: 'opPaused' })
  if (i.autoPatchHalted) out.push({ kind: 'autoPatchHalted', operation: i.autoPatchHalted })
  const certs = activeCondition(i.conditions, 'CertificatesExpiring')
  if (certs !== null) out.push({ kind: 'certs', message: certs })
  const stale = activeCondition(i.conditions, 'BundleBackupStale')
  if (stale !== null) out.push({ kind: 'backupStale', message: stale })
  const clients = activeCondition(i.conditions, 'CAClientsNotRestarted')
  if (clients !== null) out.push({ kind: 'clientsNotRestarted', message: clients })
  if (i.pending > 0) out.push({ kind: 'pending', count: i.pending })
  if (i.configWaiting > 0) out.push({ kind: 'config', count: i.configWaiting })
  if (i.patches && i.patches.length > 0) out.push({ kind: 'patch', components: i.patches })
  if (i.release) out.push({ kind: 'release', version: i.release })
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

// backupsValue is the Backups tile: the age of the newest snapshot, "No snapshot yet" while a backup
// exists without one, or "Not configured" when there is no backup (null: not loaded yet, a dash).
export function backupsValue(backups: Snapshots | null, nowMs: number): string {
  if (backups === null) return '-'
  if (backups.length === 0) return 'Not configured'
  const time = newestSnapshot(backups.flatMap((b) => b.snapshots))?.time
  return time ? timeAgo(nowMs, Number(time.seconds) * 1000) : 'No snapshot yet'
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

// versionLabel shows a version with one leading "v"; an empty one is "-".
export const versionLabel = (s: string): string => (s === '' ? '-' : s.startsWith('v') ? s : `v${s}`)
