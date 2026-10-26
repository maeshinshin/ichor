import { enumName } from './i18n.ts'

// protoc-gen-es's erasable_syntax enums (ui/src/gen) are plain
// NAME -> number objects, so there is no built-in reverse
// lookup for display. This is that lookup. A name with a dictionary entry is
// shown in English; any other is title-cased from the proto name,
// so a value added later is still readable.
export function enumLabel(values: Record<string, number>, value: number): string {
  const key = Object.keys(values).find((k) => values[k] === value)
  if (!key) return `Unknown (${value})`
  const translated = enumName(key)
  if (translated !== undefined) return translated
  return key.toLowerCase().replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}
