import type { MachineIdentity } from '@/gen/ichor/v1alpha1/machine_pb'

// Mirrors ADR 0011's rule (also re-applied server-side in ApproveMachine,
// ADR 0018): a strong identity needs a UUID, a serial number, and at least
// one permanent MAC address. Checked here too so the approve dialog can
// show *why* before the person ever clicks "Approve", not just relay the
// server's refusal after the fact.
export function isStrongIdentity(identity: MachineIdentity | undefined): boolean {
  return Boolean(identity?.uuid && identity.serial && identity.macAddresses.length > 0)
}

// canApprove is the approve button's gate: a strong identity and a chosen
// role (roleSet is the caller's reading of the MachineRole enum, which keeps
// this file free of runtime ui/src/gen imports).
export function canApprove(identity: MachineIdentity | undefined, roleSet: boolean): boolean {
  return isStrongIdentity(identity) && roleSet
}
