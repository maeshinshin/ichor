// Pure helpers for "Restore from backup". No import from "@/gen": these run directly
// under `node --test`.

import { confirmMatches, stageProgress } from './operations.ts'

export type Ref = { namespace: string; name: string }

// SnapshotChoice is one snapshot the user can restore, with the backup that holds it.
export type SnapshotChoice = {
  // key identifies the choice in the select: the same snapshot name could exist in two backups.
  key: string
  backup: Ref
  name: string
  seconds: number
  size: number
}

type BackupLike = {
  ref?: Ref
  snapshots: readonly { name: string; time?: { seconds: bigint }; size: bigint }[]
}

// snapshotChoices flattens the backups' snapshots into one list, newest first.
export function snapshotChoices(backups: readonly BackupLike[]): SnapshotChoice[] {
  const out: SnapshotChoice[] = []
  for (const b of backups) {
    if (!b.ref) continue
    for (const s of b.snapshots) {
      out.push({
        key: `${b.ref.namespace}/${b.ref.name}/${s.name}`,
        backup: { namespace: b.ref.namespace, name: b.ref.name },
        name: s.name,
        seconds: Number(s.time?.seconds ?? 0n),
        size: Number(s.size),
      })
    }
  }
  return out.sort((a, b) => b.seconds - a.seconds || a.key.localeCompare(b.key))
}

// canReview: a snapshot is chosen and nothing is in flight.
export function canReview(o: { choice: SnapshotChoice | undefined; busy: boolean }): boolean {
  return o.choice !== undefined && !o.busy
}

// canRestore folds everything the plan step needs before its button is enabled: the plan came back
// without a blocker, the cluster's name was typed, and the override was ticked.
export function canRestore(o: {
  planned: boolean
  blocked: string
  clusterName: string
  typed: string
  overridden: boolean
  busy: boolean
}): boolean {
  return o.planned && o.blocked === '' && !o.busy && confirmMatches(o.typed, o.clusterName) && o.overridden
}

// The steps status.etcdRestore.step goes through, in order.
const restoreSteps = ['Wiping', 'Recovering', 'Rejoining', 'Reconciling'] as const
export const restoreStepProgress = (step: string) => stageProgress(restoreSteps, step)
