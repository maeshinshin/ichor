import type { Timestamp } from '@bufbuild/protobuf/wkt'
import { timestampDate } from '@bufbuild/protobuf/wkt'
import type { Lang } from './i18n.ts'
import { getLang } from './lang.ts'

// Sizes are binary (1024) and labelled B/KB/MB/GB/TB in every language; only
// the digits (decimal mark, grouping) follow the language.
export function formatBytes(bytes: bigint | number | undefined, lang: Lang = getLang()): string {
  const n = typeof bytes === 'bigint' ? Number(bytes) : bytes ?? 0
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  if (!Number.isFinite(n) || n <= 0) return `0 ${units[0]}`
  let value = n
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  const digits = unit === 0 ? 0 : 1
  const num = new Intl.NumberFormat(lang, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value)
  return `${num} ${units[unit]}`
}

export function formatDate(date: Date, lang: Lang = getLang()): string {
  return new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'medium' }).format(date)
}

export function formatTimestamp(ts: Timestamp | undefined, lang: Lang = getLang()): string {
  return ts ? formatDate(timestampDate(ts), lang) : '-'
}
