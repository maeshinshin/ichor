import type { Disk } from '@/gen/ichor/v1alpha1/node_pb'

// The RFC 1123 subdomain TalosMachine.spec.hostname is validated against; the same
// rule runs in the CRD and in ApproveMachine, repeated here so the form says so before submit.
const hostnameRe = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/

export function isValidHostname(h: string): boolean {
  return h === '' || (h.length <= 253 && hostnameRe.test(h))
}

export type DiskSelector = { wwid?: string; serial?: string; byIdPath?: string; busPath?: string; devPath?: string }

// diskSelector picks the most stable identifier a disk has, in the design's fallback chain
// (WWID, serial, by-id path, bus path), and only then the kernel device path, which can be
// renumbered between boots. Undefined means the disk reports nothing to select it by.
export function diskSelector(d: Disk): DiskSelector | undefined {
  if (d.wwid) return { wwid: d.wwid }
  if (d.serial) return { serial: d.serial }
  if (d.byIdPath) return { byIdPath: d.byIdPath }
  if (d.busPath) return { busPath: d.busPath }
  if (d.devPath) return { devPath: d.devPath }
  return undefined
}

// diskKey is the form value for a disk: the selector as JSON, so two disks never collide.
export function diskKey(d: Disk): string {
  return JSON.stringify(diskSelector(d) ?? {})
}
