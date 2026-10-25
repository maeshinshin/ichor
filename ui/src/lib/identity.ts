import type { MachineIdentity } from '@/gen/ichor/v1alpha1/machine_pb'

// Mirrors's rule (also re-applied server-side in ApproveMachine): a strong identity needs a UUID, a serial number, and at least
// one permanent MAC address. Checked here too so the approve dialog can
// show *why* before the person ever clicks "Approve", not just relay the
// server's refusal after the fact.
export function isStrongIdentity(identity: MachineIdentity | undefined): boolean {
  return Boolean(identity?.uuid && identity.serial && identity.macAddresses.length > 0)
}
