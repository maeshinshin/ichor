// What the version picker of a Talos or Kubernetes upgrade offers, and the patch hints of the Versions
// card (docs/adr/0115). Pure: no import from "@/gen" or preact, so these run under `node --test` (docs/adr/0045).

// TargetList is the answer of ListUpgradeTargets, as far as the UI reads it.
export type TargetList = {
  recommended: readonly string[]
  patch: readonly string[]
  nextMinor: readonly string[]
  default: string
  available: boolean
  unavailableReason: string
}

// PickerGroup is one group of the picker. "typed" is the "Use vX.Y.Z" entry for a version the list does not hold.
export type PickerGroup = { key: 'typed' | 'recommended' | 'patch' | 'nextMinor'; items: string[] }

const finalVersion = /^v?(\d+)\.(\d+)\.(\d+)$/

// normalize makes "1.14.3" and "v1.14.3" the same version, for comparing and filtering.
function normalize(v: string): string {
  return v.trim().toLowerCase().replace(/^v/, '')
}

// typedVersion is the "vX.Y.Z" a typed text stands for, or null when the text is not a final version
// (the server refuses anything else, so the picker does not offer it).
export function typedVersion(typed: string): string | null {
  const m = finalVersion.exec(typed.trim())
  return m ? `v${m[1]}.${m[2]}.${m[3]}` : null
}

// pickerOptions is what the picker lists for the text typed and the version selected. The typed text
// filters the candidates (case-insensitive, a leading v ignored), except when it is the selected version
// itself, so an opened picker shows every candidate. A typed final version the list does not hold is
// offered first as "Use vX.Y.Z". The candidates keep the order of the list: Recommended, Patch, Next minor.
// With no list (still loading, or unavailable) the typed entry still works.
export function pickerOptions(list: TargetList | null, typed: string, selected: string): PickerGroup[] {
  const query = typed.trim() === '' || normalize(typed) === normalize(selected) ? '' : normalize(typed)
  const candidates = list ? [...list.recommended, ...list.patch, ...list.nextMinor] : []
  const groups: PickerGroup[] = []

  const entry = query === '' ? null : typedVersion(typed)
  if (entry !== null && !candidates.some((v) => normalize(v) === normalize(entry))) groups.push({ key: 'typed', items: [entry] })

  const sections: [PickerGroup['key'], readonly string[]][] = [
    ['recommended', list?.recommended ?? []],
    ['patch', list?.patch ?? []],
    ['nextMinor', list?.nextMinor ?? []],
  ]
  for (const [key, items] of sections) {
    const kept = items.filter((v) => normalize(v).includes(query))
    if (kept.length > 0) groups.push({ key, items: kept })
  }
  return groups
}

// hasPatch: the list was available and offers a newer patch of the running minor. Nothing when the fetch failed.
export function hasPatch(list: TargetList | null): boolean {
  return list !== null && list.available && list.patch.length > 0
}

// unavailableNotes says why the Versions card has no patch hint for a component: the lists that could not be
// given, grouped by reason so one cause (the check is off) is one note, in the order Talos, Kubernetes, Cilium.
export function unavailableNotes(i: {
  talos: TargetList | null
  kubernetes: TargetList | null
  cilium: TargetList | null
}): { components: string[]; reason: string }[] {
  const out: { components: string[]; reason: string }[] = []
  for (const [name, list] of [['Talos', i.talos], ['Kubernetes', i.kubernetes], ['Cilium', i.cilium]] as const) {
    if (list === null || list.available || list.unavailableReason === '') continue
    const note = out.find((n) => n.reason === list.unavailableReason)
    if (note) note.components.push(name)
    else out.push({ components: [name], reason: list.unavailableReason })
  }
  return out
}

// patchComponents names the components that have a patch, in the order Talos, Kubernetes, Cilium. The attention
// list announces them as one item. A Cilium patch is one whose images verified (docs/adr/0115, amendment 2026-11-03).
export function patchComponents(i: {
  talos: TargetList | null
  kubernetes: TargetList | null
  cilium: TargetList | null
}): string[] {
  const out: string[] = []
  if (hasPatch(i.talos)) out.push('Talos')
  if (hasPatch(i.kubernetes)) out.push('Kubernetes')
  if (hasPatch(i.cilium)) out.push('Cilium')
  return out
}
