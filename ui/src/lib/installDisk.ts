// The RFC 1123 subdomain TalosMachine.spec.hostname is validated against; the same
// rule runs in the CRD and in ApproveMachine, repeated here so the form says so before submit.
const hostnameRe = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/

export function isValidHostname(h: string): boolean {
  return h === '' || (h.length <= 253 && hostnameRe.test(h))
}

export type DiskSelector = { wwid?: string; serial?: string; byIdPath?: string; busPath?: string; devPath?: string }

// DiskOption is what the pickers read from a disk: its identifiers and what a person needs to tell it
// apart. A disk the approval recorded at discovery (Machine.discoveredDisks) and one the node reports
// now (NodeDetail.disks) both fit.
export type DiskOption = {
  wwid: string
  serial: string
  byIdPath: string
  busPath: string
  devPath: string
  sizeBytes: bigint | number
  model?: string
  transport?: string
}

// diskSelector picks the most stable identifier a disk has, in the design's fallback chain
// (WWID, serial, by-id path, bus path), and only then the kernel device path, which can be
// renumbered between boots. Undefined means the disk reports nothing to select it by.
export function diskSelector(d: DiskOption): DiskSelector | undefined {
  if (d.wwid) return { wwid: d.wwid }
  if (d.serial) return { serial: d.serial }
  if (d.byIdPath) return { byIdPath: d.byIdPath }
  if (d.busPath) return { busPath: d.busPath }
  if (d.devPath) return { devPath: d.devPath }
  return undefined
}

// diskKey is the form value for a disk: the selector as JSON, so two disks never collide.
export function diskKey(d: DiskOption): string {
  return JSON.stringify(diskSelector(d) ?? {})
}

// The smallest install target Talos takes: its system requirements ask for 10 GiB of disk.
const minInstallBytes = 10 * 1024 ** 3

// defaultInstallDisk is the disk the install picker starts on: the smallest disk that can take Talos
// and that has an identifier to select it by. Talos itself has no default; a smaller disk leaves the
// bigger ones for data. Undefined when no disk is that large, and the person chooses.
export function defaultInstallDisk(disks: DiskOption[]): DiskOption | undefined {
  let best: DiskOption | undefined
  for (const d of disks) {
    if (Number(d.sizeBytes) < minInstallBytes || diskSelector(d) === undefined) continue
    if (best === undefined || Number(d.sizeBytes) < Number(best.sizeBytes)) best = d
  }
  return best
}

export type InstallChoice = { disk: DiskSelector; image: string }

// installChoice turns the picker's state into the install target to send. A disk chosen from the
// node's own list wins; the typed device path is only used while the node reports no disks. ok is
// false for a typed path that is not a device path.
export function installChoice(s: { disks: DiskOption[]; key: string; typed: string; image: string }): {
  install: InstallChoice | undefined
  typedDisk: boolean
  ok: boolean
} {
  const typedDisk = s.disks.length === 0 && s.typed.trim() !== ''
  const image = s.image.trim()
  const install = typedDisk
    ? { disk: { devPath: s.typed.trim() }, image }
    : s.key
      ? { disk: JSON.parse(s.key) as DiskSelector, image }
      : undefined
  return { install, typedDisk, ok: !typedDisk || s.typed.trim().startsWith('/dev/') }
}

// needsInstallDisk: an approved machine with no install target whose Ready reason says the
// controller is waiting for one, so the machine page offers to set it.
export function needsInstallDisk(m: {
  approved: boolean
  install?: unknown
  conditions: { type: string; reason: string }[]
}): boolean {
  return m.approved && !m.install && m.conditions.some((c) => c.type === 'Ready' && c.reason === 'InstallDiskUnknown')
}
