// Pure helpers for the Operations section of the cluster page.
// No import from "@/gen": these run directly under `node --test`.

// OpRow is what the section needs of one TalosOperation.
export type OpRow = { name: string; phase: string; failureReason: string; isNode: boolean; created: number }

function newestFirst<T extends OpRow>(ops: readonly T[]): T[] {
  return ops.slice().sort((a, b) => b.created - a.created)
}

// blockingOperation is the operation that stops a new one from starting, or null. It is also the one
// to show at the top of the section: the newest that is not finished. Unfinished mirrors what the server
// refuses on (uiapi unfinishedOperation): not Completed, except a node shut down on purpose.
export function blockingOperation<T extends OpRow>(ops: readonly T[]): T | null {
  return newestFirst(ops).find((o) => o.phase !== 'Completed' && !(o.isNode && o.phase === 'ShutDown')) ?? null
}

export function recentOperations<T extends OpRow>(ops: readonly T[], n: number): T[] {
  return newestFirst(ops).slice(0, n)
}

const semver = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$/

type Parsed = { nums: [number, number, number]; pre: string[] }

function parse(v: string): Parsed | null {
  const m = semver.exec(v.trim())
  if (!m) return null
  return { nums: [Number(m[1]), Number(m[2]), Number(m[3])], pre: m[4] ? m[4].split('.') : [] }
}

function comparePre(a: string[], b: string[]): number {
  if (a.length === 0 || b.length === 0) return a.length === b.length ? 0 : a.length === 0 ? 1 : -1 // a release is newer
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    const x = a[i]
    const y = b[i]
    const xn = /^\d+$/.test(x)
    const yn = /^\d+$/.test(y)
    if (xn && yn) {
      if (Number(x) !== Number(y)) return Number(x) < Number(y) ? -1 : 1
    } else if (xn !== yn) {
      return xn ? -1 : 1 // a number sorts before text
    } else if (x !== y) {
      return x < y ? -1 : 1
    }
  }
  return a.length === b.length ? 0 : a.length < b.length ? -1 : 1
}

function compare(a: Parsed, b: Parsed): number {
  for (let i = 0; i < 3; i++) if (a.nums[i] !== b.nums[i]) return a.nums[i] < b.nums[i] ? -1 : 1
  return comparePre(a.pre, b.pre)
}

// versionProblem says what is wrong with an upgrade target, or null. The server
// refuses a downgrade too; this only keeps the button disabled and says why.
export function versionProblem(current: string, target: string): 'required' | 'invalid' | 'same' | 'older' | null {
  if (target.trim() === '') return 'required'
  const t = parse(target)
  if (!t) return 'invalid'
  const c = parse(current)
  if (!c) return null
  const d = compare(t, c)
  return d === 0 ? 'same' : d < 0 ? 'older' : null
}

// rebootPendingCount is how many nodes wait for a reboot to take a config change:
// status.rebootPaths is set, or Ready says RebootRequired (an older backend sends only that).
// An API server restart is not a reboot; apiRestartPendingCount counts it.
export function rebootPendingCount(machines: readonly { rebootPaths: readonly string[]; readyReason: string }[]): number {
  return machines.filter((m) => m.rebootPaths.length > 0 || m.readyReason === 'RebootRequired').length
}

// apiRestartPendingCount is how many nodes wait for their kube-apiserver to restart to take a config change.
export function apiRestartPendingCount(machines: readonly { readyReason: string }[]): number {
  return machines.filter((m) => m.readyReason === 'APIServerRestartRequired').length
}
