// Which actions to offer the caller (DiagnosticsService.GetMyPermissions). Kubernetes RBAC decides; the
// UI only hides what the role cannot do. Pure: no "@/gen", so it runs under `node --test`. Undefined
// permissions (not answered, or the probe failed) offer nothing.
import type { OpKind } from './operations.ts'

export type ActionPermissions = {
  listUsers: boolean
  createOperations: boolean
  startCaRotation: boolean
  startKeyRotation: boolean
  startShutdown: boolean
  startEtcdRestore: boolean
  updateIchorVersion: boolean
  updateBackups: boolean
  clusterUpdate: boolean
  talosconfigReader: boolean
  startNodeReset: boolean
  startNodeShutdown: boolean
  overrideMachines: boolean
  updateMachines: boolean
  deleteMachines: boolean
  approveMachines: boolean
  createPatches: boolean
  updatePatches: boolean
  deletePatches: boolean
}

export function canListUsers(p: ActionPermissions | undefined): boolean {
  return p?.listUsers === true
}

// mayStart: every operation needs create on talosoperations; an Ichor upgrade also changes the
// installation's version, and the destructive kinds each need their own start verb.
export function mayStart(p: ActionPermissions | undefined, kind: OpKind | 'etcd-restore'): boolean {
  if (!p?.createOperations) return false
  switch (kind) {
    case 'ichor':
      return p.updateIchorVersion
    case 'shutdown':
      return p.startShutdown
    case 'ca-rotation':
      return p.startCaRotation
    case 'key-rotation':
      return p.startKeyRotation
    case 'etcd-restore':
      return p.startEtcdRestore
    default:
      return true
  }
}

export type ClusterMenuItem = 'ca-rotation' | 'key-rotation' | 'reader-talosconfig' | 'shutdown'

// clusterMenu is the Overview's "more" menu, in its order, without what the caller may not do.
export function clusterMenu(p: ActionPermissions | undefined): ClusterMenuItem[] {
  const out: ClusterMenuItem[] = []
  if (mayStart(p, 'ca-rotation')) out.push('ca-rotation')
  if (mayStart(p, 'key-rotation')) out.push('key-rotation')
  if (p?.talosconfigReader) out.push('reader-talosconfig')
  if (mayStart(p, 'shutdown')) out.push('shutdown')
  return out
}

// A pending machine: approving needs the approve verb and the write of the machine's spec; rejecting only the write.
export function mayApprove(p: ActionPermissions | undefined): boolean {
  return p?.approveMachines === true && p.updateMachines
}

export function mayReject(p: ActionPermissions | undefined): boolean {
  return p?.updateMachines === true
}
