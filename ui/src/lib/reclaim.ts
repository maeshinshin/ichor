// Pure helpers for the override reclaim dialog.

// uuidMatches is the typed consent: the text the person typed equals the node's UUID, ignoring case and
// surrounding space. An empty UUID never matches, so a missing inspection cannot be confirmed.
export function uuidMatches(typed: string, uuid: string): boolean {
  const u = uuid.trim().toLowerCase()
  return u !== '' && typed.trim().toLowerCase() === u
}

// reclaimSettled says the phase will not change any more, so polling stops.
export function reclaimSettled(phase: string): boolean {
  return ['Done', 'Failed', 'Expired'].includes(phase)
}
