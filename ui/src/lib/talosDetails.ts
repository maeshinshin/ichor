// Pure helpers for the Talos details card; no DOM, no preact,
// so they run under `node --test`.

export type Tone = 'good' | 'bad' | 'neutral'

type ServiceLike = { state: string; healthUnknown: boolean; healthy: boolean }

export function formatSpeed(mbit: number): string {
  if (!Number.isFinite(mbit) || mbit <= 0) return '-'
  if (mbit < 1000) return `${mbit} Mbit/s`
  return `${mbit / 1000} Gbit/s`
}

// A service Talos reports as failed, or running but unhealthy, is bad; one
// running and healthy is ok; anything else (finished, unknown health) is shown
// without colour.
export function serviceTone(s: ServiceLike): Tone {
  if (s.state === 'Failed') return 'bad'
  if (s.state !== 'Running') return 'neutral'
  if (s.healthUnknown) return 'neutral'
  return s.healthy ? 'good' : 'bad'
}

// The Health cell's text: a service Talos has no health check for says so,
// rather than showing a dash that reads as broken.
export function healthKey(s: ServiceLike): 'talos.noHealthCheck' | 'common.yes' | 'common.no' {
  if (s.healthUnknown) return 'talos.noHealthCheck'
  return s.healthy ? 'common.yes' : 'common.no'
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
