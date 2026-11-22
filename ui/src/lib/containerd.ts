// Pure helpers for the containerd device-ownership switch (ADR 0172). No import from "@/gen".

// deviceOwnershipNeedsContract is the lowest version contract that renders the setting (the first with the
// CRICustomizationConfig document).
export const DEVICE_OWNERSHIP_MIN_CONTRACT = 'v1.14'

// contractAtLeast compares vMAJOR.MINOR contracts; an unreadable one is not at least anything.
export function contractAtLeast(contract: string, min: string): boolean {
  const parse = (v: string) => /^v(\d+)\.(\d+)$/.exec(v)?.slice(1).map(Number)
  const a = parse(contract)
  const b = parse(min)
  if (!a || !b) return false
  return a[0] !== b[0] ? a[0] > b[0] : a[1] >= b[1]
}

// deviceOwnershipCanTurnOn is false when the cluster's contract is below the floor and the setting is
// off; turning it off is always possible. The backend refuses the same way.
export function deviceOwnershipCanTurnOn(contract: string, enabled: boolean): boolean {
  return enabled || contractAtLeast(contract, DEVICE_OWNERSHIP_MIN_CONTRACT)
}
