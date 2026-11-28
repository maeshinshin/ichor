// Pure helpers for the operation's own page. No import from "@/gen": these run directly under
// `node --test`.

import { operationState } from './operations.ts'

// formatDuration writes a span of milliseconds the way a person reads it: "45s", "3m 05s", "2h 04m".
// A negative or missing span is a dash.
export function formatDuration(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms) || ms < 0) return '-'
  const total = Math.floor(ms / 1000)
  if (total < 60) return `${total}s`
  const minutes = Math.floor(total / 60)
  const seconds = total % 60
  if (minutes < 60) return `${minutes}m ${String(seconds).padStart(2, '0')}s`
  const hours = Math.floor(minutes / 60)
  return `${hours}h ${String(minutes % 60).padStart(2, '0')}m`
}

// spanMs is the time from start to end, or to now while there is no end yet. No start is no span.
// A finished item with no recorded end (created before finishedAt existed) has no known span.
export function spanMs(start: Date | undefined, end: Date | undefined, now: Date, finished = false): number | undefined {
  if (!start || (finished && !end)) return undefined
  return (end ?? now).getTime() - start.getTime()
}

// summaryFallback names an operation when the server sent no summary (an older backend): the kind
// and, for a node action, the machine.
export function summaryFallback(name: string, kindLabel: string, machine?: string): string {
  if (!kindLabel) return name
  return machine ? `${kindLabel} ${machine}` : kindLabel
}

export type EventLike = { type: string }

// visibleEvents keeps every event, or only the Warnings.
export function visibleEvents<T extends EventLike>(events: T[], warningsOnly: boolean): T[] {
  return warningsOnly ? events.filter((e) => e.type === 'Warning') : events
}

// nodePath is where a machine table row links to.
export function nodePath(name: string): string {
  return `/nodes/${encodeURIComponent(name)}`
}

// operationWaiting is the message that says why a running or paused operation is not moving, or undefined. A pause
// without a failure reason keeps its cause only in the Paused condition; the kube-proxy switch's wait for a person
// (the OnDelete remedy) lives only in VerifyingNewSide, which is True while it waits and False once verified.
// shortcut: only that one running wait is known; another operation that waits in its own condition needs adding here.
export function operationWaiting(op: { phase: string; failureReason: string; conditions: readonly { type: string; status: string; reason: string; message: string }[] }): string | undefined {
  const state = operationState(op.phase, op.failureReason, op.conditions)
  const type = state === 'paused' ? 'Paused' : state === 'running' ? 'VerifyingNewSide' : ''
  return op.conditions.find((c) => c.type === type && c.status === 'True' && c.reason !== 'AwaitingApproval')?.message || undefined
}
