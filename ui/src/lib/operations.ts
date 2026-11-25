// Pure helpers for the operations screens. No import from
// "@/gen": these run directly under `node --test`.

// The kinds an operation can have, as the screens name them.
export type OpKind = 'talos' | 'kubernetes' | 'ichor' | 'cni' | 'shutdown' | 'ca-rotation' | 'config' | 'key-rotation'

export type OpState = 'pending' | 'running' | 'paused' | 'blocked' | 'failed' | 'shutdown' | 'done'

// operationState folds TalosOperation.status.phase and failureReason into what
// a person needs to know. A pause with a failure reason is the controller
// stopping after a machine failed; it never retries by itself. A pause
// without one is somebody's spec.paused, unless the Paused condition's reason is OwnNodeUnread: the controller
// cannot tell which machine it runs on, nothing is to be resumed, and it lifts the pause itself.
export function operationState(phase: string, failureReason: string, conditions: readonly { type: string; reason: string }[] = []): OpState {
  switch (phase) {
    case 'Completed':
      return 'done'
    case 'ShutDown':
      return 'shutdown'
    case 'Paused':
      if (failureReason !== '') return 'failed'
      return conditions.some((c) => c.type === 'Paused' && c.reason === 'OwnNodeUnread') ? 'blocked' : 'paused'
    case 'InProgress':
      return 'running'
    default:
      return 'pending'
  }
}

// pollIntervalMs is how long to wait before reading an operation again, or null
// when nothing more will change by itself. A failed operation waits for a
// person, so it stops; a paused one may be resumed from another screen, so it
// slows down.
export function pollIntervalMs(state: OpState): number | null {
  switch (state) {
    case 'done':
    case 'failed':
      return null
    case 'paused':
    case 'blocked':
    case 'shutdown':
      return 10_000
    default:
      return 3_000
  }
}

// CONTROL_GRACE_MS is how long a stopped operation keeps being read after a pause, resume or approve: the
// answer to the action carries the old status, and the controller needs a moment to move it.
export const CONTROL_GRACE_MS = 30_000

// nextPollMs is pollIntervalMs, except that a done or failed operation is still read for CONTROL_GRACE_MS after
// a control action (sinceChangeMs, null when there was none).
export function nextPollMs(state: OpState, sinceChangeMs: number | null): number | null {
  const next = pollIntervalMs(state)
  return next === null && sinceChangeMs !== null && sinceChangeMs < CONTROL_GRACE_MS ? 3_000 : next
}

export function progressCounts(machines: readonly { phase: string }[]): { done: number; running: number } {
  let done = 0
  let running = 0
  for (const m of machines) {
    if (m.phase === 'Completed') done++
    else if (m.phase === 'InProgress') running++
  }
  return { done, running }
}

// pauseAction is the button an operation row shows: nothing once it is over,
// "resume" while paused by a person or after a failed step (resume then runs that step again).
export function pauseAction(paused: boolean, state: OpState): 'pause' | 'resume' | null {
  if (state === 'done' || state === 'shutdown' || state === 'blocked') return null
  return paused || state === 'failed' ? 'resume' : 'pause'
}

// awaitingApproval is true for an operation that is paused and has run no step: the controller marks it with the
// Paused condition's reason AwaitingApproval, and a person approves it instead of resuming it.
export function awaitingApproval(phase: string, failureReason: string, conditions: readonly { type: string; reason: string }[] = []): boolean {
  return phase === 'Paused' && failureReason === '' && conditions.some((c) => c.type === 'Paused' && c.reason === 'AwaitingApproval')
}

export type PlanStep = { step: number; role: string; names: string[] }

// planSteps groups the plan's machines by step, in order. Machines sharing a
// step are handled together.
export function planSteps(machines: readonly { name: string; role: string; step: number }[]): PlanStep[] {
  const steps: PlanStep[] = []
  for (const m of machines) {
    const last = steps[steps.length - 1]
    if (last && last.step === m.step) last.names.push(m.name)
    else steps.push({ step: m.step, role: m.role, names: [m.name] })
  }
  return steps
}

export function planCounts(machines: readonly { role: string }[]): { controlPlanes: number; workers: number } {
  const controlPlanes = machines.filter((m) => m.role === 'controlplane').length
  return { controlPlanes, workers: machines.length - controlPlanes }
}

const versionPattern = /^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/

// targetError says what is wrong with the target typed for a kind, or null.
// The server checks the same rules; this only keeps the button disabled.
export function targetError(kind: OpKind, input: string): 'required' | 'invalid' | null {
  if (kindNeedsTypedName(kind)) return null
  const v = input.trim()
  if (v === '') return 'required'
  if (kind === 'cni') return v === 'enabled' || v === 'replaced' ? null : 'invalid'
  return versionPattern.test(v) ? null : 'invalid'
}

// confirmMatches is the extra gate of a shutdown: the name of the cluster has
// to be typed.
export function confirmMatches(typed: string, name: string): boolean {
  return name !== '' && typed.trim() === name
}

// kindNeedsTypedName: the kinds that take every machine through a disruptive
// change ask for the cluster's name to be typed.
export function kindNeedsTypedName(kind: OpKind): boolean {
  return kind === 'shutdown' || kind === 'ca-rotation' || kind === 'config' || kind === 'key-rotation'
}

// stageProgress is where a multi-stage operation is: the stage's name and a 1-based position among the
// stages; null for an empty stage (nothing recorded yet) or one this UI does not know.
export function stageProgress<S extends string>(stages: readonly S[], stage: string): { name: S; index: number; total: number } | null {
  const i = (stages as readonly string[]).indexOf(stage)
  return i < 0 ? null : { name: stages[i], index: i + 1, total: stages.length }
}

// The stages status.caRotation.stage goes through, in order.
const caStages = ['Accepting', 'AwaitingCredentials', 'Issuing', 'Dropping', 'Finalizing', 'Done'] as const
export const caStageProgress = (stage: string) => stageProgress(caStages, stage)

// The stages status.keyRotation.stage goes through, in order. A rotation of one key skips one of them.
const keyStages = ['Accepting', 'Issuing', 'Reencrypting', 'SettlingTokens', 'Dropping', 'Finalizing', 'Done'] as const
export const keyStageProgress = (stage: string) => stageProgress(keyStages, stage)

// The stages status.kubernetes.stage goes through, in order.
export const kubernetesStages = ['APIServers', 'ControlPlaneComponents', 'KubeProxy', 'Kubelets'] as const
export const kubernetesStageProgress = (stage: string) => stageProgress(kubernetesStages, stage)
