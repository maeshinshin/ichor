// Pure helpers for the node actions menu (docs/adr/0085). No import from "@/gen": these run
// directly under `node --test` (docs/adr/0045).

import type { ActionPermissions } from './actions.ts'

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
  // override: the user must tick the override and needs the override permission (reset).
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

// mayNodeAction: every node action but remove is a TalosOperation, so it needs create on talosoperations;
// shutdown and reset also need their own start verb (reset also the override on the machine); remove
// writes the machine (the allow-delete mark) and deletes it.
export function mayNodeAction(p: ActionPermissions | undefined, id: NodeActionId): boolean {
  if (!p) return false
  switch (id) {
    case 'remove':
      return p.updateMachines && p.deleteMachines
    case 'shutdown':
      return p.createOperations && p.startNodeShutdown
    case 'reset':
      return p.createOperations && p.startNodeReset && p.overrideMachines
    default:
      return p.createOperations
  }
}

// availableActions hides what cannot apply to the node: nothing for a machine that is not approved
// or is already being removed, the etcd actions on a worker, and what the caller's role may not do
// (undefined permissions offer nothing). The server asks again; a refusal is shown as it words it.
export function availableActions(m: { approved: boolean; deleting: boolean; role: string; perms: ActionPermissions | undefined }): NodeActionDef[] {
  if (!m.approved || m.deleting) return []
  return nodeActions.filter((a) => (!a.controlPlaneOnly || m.role === 'controlplane') && mayNodeAction(m.perms, a.id))
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
  overridden: boolean
  service: string
  busy: boolean
}): boolean {
  if (!opts.planned || opts.blocked !== '' || opts.busy) return false
  if (opts.def.id === 'restart-service' && !(restartableServices as readonly string[]).includes(opts.service)) return false
  if (opts.confirmName !== '' && !nodeNameMatches(opts.typed, opts.nodeName)) return false
  if (opts.def.override && !opts.overridden) return false
  return true
}
