// Pure helpers for the node actions menu. No import from "@/gen": these run
// directly under `node --test`.

import type { ActionPermissions } from './actions.ts'

export type NodeActionId =
  | 'reboot'
  | 'shutdown'
  | 'restart-service'
  | 'etcd-defrag'
  | 'etcd-forfeit-leadership'
  | 'remove'

export type NodeActionDef = {
  id: NodeActionId
  // destructive actions get the destructive styling in the menu and the dialog.
  destructive: boolean
  // typedName: the user types the node's name before the action can start.
  typedName: boolean
  // override: the user must tick the override and needs the override permission (remove).
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
  { id: 'remove', destructive: true, typedName: true, override: true, controlPlaneOnly: false },
]

// What a remove wipes: the Select's choices and the wipe mode each sends. The default sends
// nothing (STATE and EPHEMERAL only); the names match TalosOperation.spec.node.wipeMode.
export type WipeChoice = 'system-partitions' | 'system-disk' | 'system-disk-and-user-disks'
export const wipeChoices: readonly WipeChoice[] = ['system-partitions', 'system-disk', 'system-disk-and-user-disks']

export function wipeModeOf(c: WipeChoice): string {
  return c === 'system-partitions' ? '' : c
}

// The services restart-service may restart: the same allowlist as the controller and the CRD.
export const restartableServices = ['kubelet', 'containerd', 'cri'] as const

// mayNodeAction: every node action but remove is a TalosOperation, so it needs create on talosoperations;
// shutdown also needs its own start verb. Remove wipes the node (which merged reset into it): it
// writes the machine (the marks), deletes it, and needs the reset start verb and the override on the machine.
export function mayNodeAction(p: ActionPermissions | undefined, id: NodeActionId): boolean {
  if (!p) return false
  switch (id) {
    case 'remove':
      return p.updateMachines && p.deleteMachines && p.startNodeReset && p.overrideMachines
    case 'shutdown':
      return p.createOperations && p.startNodeShutdown
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
