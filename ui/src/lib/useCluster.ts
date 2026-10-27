import { useEffect, useState } from 'preact/hooks'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { clusterClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'

// Ichor manages one cluster. The list is read once for the whole app and shared:
// the shell and every page that calls useCluster see the same state, and one timer polls while
// anything is mounted. With more than one cluster the first by name is shown; `count` lets the
// app warn.
type State = { cluster: Cluster | null; count: number; loading: boolean; error: { title: string; detail: string } | null }

const POLL_MS = 10000
let state: State = { cluster: null, count: 0, loading: true, error: null }
const listeners = new Set<() => void>()
let timer: ReturnType<typeof setInterval> | undefined

function set(next: State) {
  state = next
  listeners.forEach((l) => l())
}

function load() {
  clusterClient.listClusters({}).then(
    (res) => {
      const sorted = [...res.clusters].sort((a, b) => (a.ref?.name ?? '').localeCompare(b.ref?.name ?? ''))
      set({ cluster: sorted[0] ?? null, count: sorted.length, loading: false, error: null })
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
