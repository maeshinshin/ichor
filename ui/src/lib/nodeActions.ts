// Pure helpers for the node actions menu. No import from "@/gen": these run
// directly under `node --test`.

export type NodeActionId =
  | 'reboot'
  | 'shutdown'
  | 'restart-service'
  | 'etcd-defrag'
  | 'etcd-forfeit-leadership'
  | 'reset'
  | 'remove'

export type NodeActionDef = {
  id: NodeActionId
  // destructive actions get the destructive styling in the menu and the dialog.
  destructive: boolean
  // typedName: the user types the node's name before the action can start.
  typedName: boolean
  // override: the user also gives a reason and needs the override permission (reset).
  override: boolean
  controlPlaneOnly: boolean
}

// In the order the menu shows them: harmless first, destructive last.
export const nodeActions: readonly NodeActionDef[] = [
  { id: 'restart-service', destructive: false, typedName: false, override: false, controlPlaneOnly: false },
  { id: 'etcd-defrag', destructive: false, typedName: false, override: false, controlPlaneOnly: true },
  { id: 'etcd-forfeit-leadership', destructive: false, typedName: false, override: false, controlPlaneOnly: true },
  { id: 'reboot', destructive: true, typedName: false, override: false, controlPlaneOnly: false },
  { id: 'shutdown', destructive: true, typedName: true, override: false, controlPlaneOnly: false },
  { id: 'remove', destructive: true, typedName: true, override: false, controlPlaneOnly: false },
  { id: 'reset', destructive: true, typedName: true, override: true, controlPlaneOnly: false },
]

// The services restart-service may restart: the same allowlist as the controller and the CRD.
export const restartableServices = ['kubelet', 'containerd', 'cri'] as const

// availableActions hides what cannot apply to the node: nothing for a machine that is not approved
// or is already being removed, and the etcd actions on a worker. What a user's own permissions
// allow is Kubernetes' to say; a refusal is shown as the server words it.
export function availableActions(m: { approved: boolean; deleting: boolean; role: string }): NodeActionDef[] {
  if (!m.approved || m.deleting) return []
  return nodeActions.filter((a) => !a.controlPlaneOnly || m.role === 'controlplane')
}

export function nodeNameMatches(typed: string, name: string): boolean {
  return typed.trim() === name
}

// canStart folds everything the dialog needs before its Start button is enabled.
export function canStart(opts: {
  def: NodeActionDef
  blocked: string
  planned: boolean
  confirmName: string
  typed: string
  nodeName: string
  reason: string
  service: string
  busy: boolean
}): boolean {
  if (!opts.planned || opts.blocked !== '' || opts.busy) return false
  if (opts.def.id === 'restart-service' && !(restartableServices as readonly string[]).includes(opts.service)) return false
  if (opts.confirmName !== '' && !nodeNameMatches(opts.typed, opts.nodeName)) return false
  if (opts.def.override && opts.reason.trim() === '') return false
  return true
}
