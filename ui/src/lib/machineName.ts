import type { Machine } from '@/gen/ichor/v1alpha1/machine_pb'

// machineDisplayName names a machine for people: its hostname, else the Kubernetes node name the
// controller recorded, else its TalosMachine name (a UUID). Links keep using ref.name.
export function machineDisplayName(m: Pick<Machine, 'hostname' | 'nodeName' | 'ref'>): string {
  return m.hostname || m.nodeName || m.ref?.name || '-'
}
