// Pure helpers for the operations screens. No import from
// "@/gen": these run directly under `node --test`.

// The kinds an operation can have, as the screens name them.
export type OpKind = 'talos' | 'kubernetes' | 'ichor' | 'cni' | 'shutdown' | 'ca-rotation' | 'config'

export type OpState = 'pending' | 'running' | 'paused' | 'failed' | 'shutdown' | 'done'

// operationState folds TalosOperation.status.phase and failureReason into what
// a person needs to know. A pause with a failure reason is the controller
// stopping after a machine failed; it never retries by itself. A pause
// without one is somebody's spec.paused.
export function operationState(phase: string, failureReason: string): OpState {
  switch (phase) {
    case 'Completed':
      return 'done'
    case 'ShutDown':
      return 'shutdown'
    case 'Paused':
      return failureReason !== '' ? 'failed' : 'paused'
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
    case 'shutdown':
      return 10_000
    default:
      return 3_000
  }
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

// pauseAction is the button an operation row shows: nothing once it is over or
// failed (a failed operation is fixed, then deleted or replaced; resuming it
// would not retry the machine), "resume" while paused by a person.
export function pauseAction(paused: boolean, state: OpState): 'pause' | 'resume' | null {
  if (state === 'done' || state === 'failed' || state === 'shutdown') return null
  return paused ? 'resume' : 'pause'
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
  const v = input.trim()
  switch (kind) {
    case 'shutdown':
    case 'ca-rotation':
    case 'config':
      return null
    case 'cni':
      return v === '' ? 'required' : v === 'enabled' || v === 'replaced' ? null : 'invalid'
    default:
      return v === '' ? 'required' : versionPattern.test(v) ? null : 'invalid'
  }
}

// confirmMatches is the extra gate of a shutdown: the name of the cluster has
// to be typed.
export function confirmMatches(typed: string, name: string): boolean {
  return name !== '' && typed.trim() === name
}

// kindNeedsTarget is false for the kinds that start without a version or mode.
export function kindNeedsTarget(kind: OpKind): boolean {
  return kind !== 'shutdown' && kind !== 'ca-rotation' && kind !== 'config'
}

// kindNeedsTypedName: the kinds that take every machine through a disruptive
// change ask for the cluster's name to be typed.
export function kindNeedsTypedName(kind: OpKind): boolean {
  return kind === 'shutdown' || kind === 'ca-rotation' || kind === 'config'
}

// The stages status.caRotation.stage goes through, in order.
const caStages = ['Accepting', 'Issuing', 'Dropping', 'Finalizing', 'Done'] as const

// caStageProgress is where a rotation is, as a 1-based position; null before
// the new CAs are saved (empty stage) or for a stage this UI does not know.
export function caStageProgress(stage: string): { index: number; total: number } | null {
  const i = (caStages as readonly string[]).indexOf(stage)
  return i < 0 ? null : { index: i + 1, total: caStages.length }
}

// The stages status.kubernetes.stage goes through, in order.
export const kubernetesStages = ['ControlPlanes', 'KubeProxy', 'Kubelets'] as const
export type KubernetesStage = (typeof kubernetesStages)[number]

// kubernetesStageProgress is where a Kubernetes upgrade is: its stage's name and a
// 1-based position among the three stages. Null before the first stage is recorded or for an unknown one.
export function kubernetesStageProgress(stage: string): { name: KubernetesStage; index: number; total: number } | null {
  const i = (kubernetesStages as readonly string[]).indexOf(stage)
  return i < 0 ? null : { name: kubernetesStages[i], index: i + 1, total: kubernetesStages.length }
}
