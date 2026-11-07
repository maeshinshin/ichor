// Pure helpers for the override reclaim dialog (docs/adr/0103, 2.2).
import { en, t, type Key } from './i18n.ts'

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

// usesClusterCredentials says whether a reclaim is asked with the cluster's own credentials: the person
// ticked it (or the caller preset it for an orphan) and may update the cluster. Without that right the
// server would refuse a request that names no talosconfig.
export function usesClusterCredentials(own: boolean, clusterAllowed: boolean): boolean {
  return own && clusterAllowed
}

// reclaimPhaseLabel is the phase in words; a phase this build does not know is shown as received.
export function reclaimPhaseLabel(phase: string): string {
  const key = `reclaim.phase.${phase}`
  return key in en ? t(key as Key) : phase
}
