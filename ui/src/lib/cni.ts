// Pure helpers for a cluster whose CNI Ichor leaves alone (docs/adr/0103). No import from "@/gen"
// or preact, so they run under `node --test`.

// isCniUnmanaged: spec.cni.preset is "unmanaged", so Ichor neither installs, upgrades nor
// configures the CNI and the Cilium screens do not apply.
export function isCniUnmanaged(preset: string): boolean {
  return preset === 'unmanaged'
}

// detectedCni is what the cluster is observed to run, "unknown" until the controller has looked.
export function detectedCni(detected: string): string {
  return detected === '' ? 'unknown' : detected
}

// caRotationAdvised: the CARotationAdvised condition is True (an adopted cluster; docs/adr/0103
// section 1.4). The advice is only advice; dismissing it writes an annotation.
export function caRotationAdvised(conds: readonly { type: string; status: string }[]): boolean {
  return conds.some((c) => c.type === 'CARotationAdvised' && c.status === 'True')
}
