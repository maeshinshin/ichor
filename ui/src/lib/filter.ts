// matchesQuery is the list toolbars' search box: every whitespace-separated
// term must appear (case-insensitively) in at least one of the fields.
export function matchesQuery(query: string, ...fields: (string | undefined)[]): boolean {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean)
  const hay = fields.map((f) => (f ?? '').toLowerCase())
  return terms.every((t) => hay.some((f) => f.includes(t)))
}
