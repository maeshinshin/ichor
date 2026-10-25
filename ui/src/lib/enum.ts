// protoc-gen-es's erasable_syntax enums (ui/src/gen) are plain
// NAME -> number objects (ADR 0021), so there is no built-in reverse
// lookup for display. This is that lookup, title-cased for the screen.
export function enumLabel(values: Record<string, number>, value: number): string {
  const key = Object.keys(values).find((k) => values[k] === value)
  if (!key) return `Unknown (${value})`
  if (key === 'UNSPECIFIED') return 'Unspecified'
  return key.toLowerCase().replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase())
}
