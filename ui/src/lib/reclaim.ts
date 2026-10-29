// Pure helpers for the break-glass reclaim dialog.

// uuidMatches is the typed consent: the text the person typed equals the node's UUID, ignoring case and
// surrounding space. An empty UUID never matches, so a missing inspection cannot be confirmed.
export function uuidMatches(typed: string, uuid: string): boolean {
  const u = uuid.trim().toLowerCase()
  return u !== '' && typed.trim().toLowerCase() === u
}

// canOfferReclaim: the entry is shown only when the installation enabled reclaim and Kubernetes RBAC
// grants the caller the verb. Undefined (not answered, or the probe failed) offers nothing.
export function canOfferReclaim(p: { enabled: boolean; allowed: boolean } | undefined): boolean {
  return !!p && p.enabled && p.allowed
}

const finished = ['Done', 'Failed', 'Expired']

// reclaimSettled says the phase will not change any more, so polling stops.
export function reclaimSettled(phase: string): boolean {
  return finished.includes(phase)
}
