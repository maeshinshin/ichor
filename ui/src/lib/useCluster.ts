import { useEffect, useState } from 'preact/hooks'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { blockingOperation } from '@/lib/clusterOps'
import { clusterClient, operationClient } from '@/lib/connect'
import { operationState } from '@/lib/operations'
import { describeError, type ErrorShape } from '@/lib/errors'

// Ichor manages one cluster (docs/adr/0097). The list is read once for the whole app and shared:
// the shell and every page that calls useCluster see the same state, and one timer polls while
// anything is mounted. With more than one cluster the first by name is shown; `count` lets the
// app warn.
// operations is the cluster's operation list from the same poll, as the server lists it. opRunning
// is whether the cluster's current operation is running; with the rollout and the conditions it
// decides the health shown in the top bar and on the Overview.
type State = {
  cluster: Cluster | null
  count: number
  operations: Operation[]
  opRunning: boolean
  loading: boolean
  error: ErrorShape | null
  // opsError is a failed operations read; the cluster itself loaded.
  opsError: ErrorShape | null
}

const POLL_MS = 10000
let state: State = { cluster: null, count: 0, operations: [], opRunning: false, loading: true, error: null, opsError: null }

function isOpRunning(operations: Operation[]): boolean {
  const rows = operations.map((op) => ({
    phase: op.phase,
    failureReason: op.failureReason,
    name: op.ref?.name ?? '',
    isNode: op.kind === OperationKind.NODE,
    created: Number(op.createdAt?.seconds ?? 0n),
  }))
  const cur = blockingOperation(rows)
  return cur !== null && operationState(cur.phase, cur.failureReason) === 'running'
}
const listeners = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | undefined

function set(next: State) {
  state = next
  listeners.forEach((l) => l())
}

function load() {
  clusterClient.listClusters({}).then(
    async (res) => {
      const sorted = [...res.clusters].sort((a, b) => (a.ref?.name ?? '').localeCompare(b.ref?.name ?? ''))
      const cluster = sorted[0] ?? null
      // A failed read keeps the last list and reports it; opRunning is false then, as it was before.
      let operations = state.operations
      let opsError: ErrorShape | null = null
      if (cluster) {
        try {
          operations = (await operationClient.listOperations({ cluster: cluster.ref })).operations
        } catch (err) {
          opsError = describeError(err)
        }
      }
      const opRunning = cluster !== null && opsError === null && isOpRunning(operations)
      set({ cluster, count: sorted.length, operations, opRunning, loading: false, error: null, opsError })
    },
    (err) => set({ ...state, loading: false, error: describeError(err) }),
  )
}

export function useCluster(): State & { reload(): void } {
  const [, bump] = useState(0)
  useEffect(() => {
    const l = () => bump((n) => n + 1)
    listeners.add(l)
    if (listeners.size === 1) {
      load()
      timer = setInterval(load, POLL_MS)
    }
    return () => {
      listeners.delete(l)
      if (listeners.size === 0) clearInterval(timer)
    }
  }, [])
  return { ...state, reload: load }
}
