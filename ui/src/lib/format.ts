import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { timestampDate } from '@bufbuild/protobuf/wkt'

export function formatBytes(bytes: bigint | number | undefined): string {
  const n = typeof bytes === 'bigint' ? Number(bytes) : bytes ?? 0
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = n
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

export function formatTimestamp(ts: Timestamp | undefined): string {
  return ts ? timestampDate(ts).toLocaleString() : '-'
}
