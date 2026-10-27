import { useEffect, useState } from 'preact/hooks'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { OperationKind } from '@/gen/ichor/v1alpha1/operation_pb'
import { currentOperation } from '@/lib/clusterOps'
import { clusterClient, operationClient } from '@/lib/connect'
import { operationState } from '@/lib/operations'
import { describeError } from '@/lib/errors'

// Ichor manages one cluster. The list is read once for the whole app and shared:
// the shell and every page that calls useCluster see the same state, and one timer polls while
// anything is mounted. With more than one cluster the first by name is shown; `count` lets the
// app warn.
// opRunning is whether the cluster's current operation is running; with the rollout and the
// conditions it decides the health shown in the top bar and on the Overview.
type State = {
  cluster: Cluster | null
  count: number
  opRunning: boolean
  loading: boolean
  error: { title: string; detail: string } | null
}

const POLL_MS = 10000
let state: State = { cluster: null, count: 0, opRunning: false, loading: true, error: null }

async function isOpRunning(cluster: Cluster): Promise<boolean> {
  try {
    const res = await operationClient.listOperations({ cluster: cluster.ref })
    const rows = res.operations.map((op) => ({
      phase: op.phase,
      failureReason: op.failureReason,
      name: op.ref?.name ?? '',
      isNode: op.kind === OperationKind.NODE,
      created: Number(op.createdAt?.seconds ?? 0n),
    }))
    const cur = currentOperation(rows)
    return cur !== null && operationState(cur.phase, cur.failureReason) === 'running'
  } catch {
    return false
  }
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
      const opRunning = cluster ? await isOpRunning(cluster) : false
      set({ cluster, count: sorted.length, opRunning, loading: false, error: null })
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
