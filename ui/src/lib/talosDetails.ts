// Pure helpers for the Talos details card; no DOM, no preact,
// so they run under `node --test`.

export type Tone = 'good' | 'bad' | 'neutral'

type ServiceLike = { state: string; healthUnknown: boolean; healthy: boolean; healthMessage?: string }

export function formatSpeed(mbit: number): string {
  if (!Number.isFinite(mbit) || mbit <= 0) return '-'
  if (mbit < 1000) return `${mbit} Mbit/s`
  return `${mbit / 1000} Gbit/s`
}

// A service Talos reports as failed, or running but unhealthy, is bad; a running
// one that is healthy, or has no health check, is ok; anything else (finished,
// skipped ...) is shown without colour.
export function serviceTone(s: ServiceLike): Tone {
  if (s.state === 'Failed') return 'bad'
  if (s.state !== 'Running') return 'neutral'
  if (s.healthUnknown) return 'good'
  return s.healthy ? 'good' : 'bad'
}

export type ServiceStatus = {
  // Shown as-is, or translated through labelKey when set.
  label: string
  labelKey?: 'talos.healthy' | 'talos.unhealthy'
  tone: Tone
  // Muted detail after the label; only an unhealthy service carries one.
  message: string
}

// The single status cell of a service: a running service reads Healthy or
// Unhealthy when Talos has a health check for it, and Running when it has none
// (dashboard). Any other state is shown as Talos reports it.
export function serviceStatus(s: ServiceLike): ServiceStatus {
  const tone = serviceTone(s)
  if (s.state !== 'Running' || s.healthUnknown) return { label: s.state, tone, message: '' }
  if (s.healthy) return { label: 'Healthy', labelKey: 'talos.healthy', tone, message: '' }
  return { label: 'Unhealthy', labelKey: 'talos.unhealthy', tone, message: s.healthMessage ?? '' }
}

export function sortServices<T extends ServiceLike & { id: string }>(services: readonly T[]): T[] {
  const rank = (s: T) => (serviceTone(s) === 'bad' ? 0 : 1)
  return [...services].sort((a, b) => rank(a) - rank(b) || a.id.localeCompare(b.id))
}

// Volume types that are backed by a disk partition. Directory, overlay,
// symlink and tmpfs volumes are Talos's own mounts, always present and
// size-less, so they are hidden until the person asks for all of them.
const DISK_BACKED_VOLUME_TYPES = new Set(['partition', 'disk'])

export function visibleVolumes<T extends { type: string }>(volumes: readonly T[], showAll: boolean): T[] {
  return showAll ? [...volumes] : volumes.filter((v) => DISK_BACKED_VOLUME_TYPES.has(v.type))
}
