// activeSection picks the section the reader is in: the last one whose top edge
// has reached `offset` (the sticky header's height), or the first one before
// any has. Pure; SectionNav feeds it measured tops on scroll.
export function activeSection(sections: { id: string; top: number }[], offset: number): string {
  let active = sections[0]?.id ?? ''
  for (const s of sections) {
    if (s.top <= offset) active = s.id
  }
  return active
}

// SPY_EPSILON absorbs sub-pixel rounding between where a jump stops a section
// (exactly its scroll margin) and where the spy counts it as reached.
export const SPY_EPSILON = 2

// pickActive decides what the nav highlights. A clicked section (`held`) wins
// while its smooth scroll runs; otherwise the scroll position decides, and the
// at-the-bottom rule (the last section may never reach the offset) applies
// only to passive scrolling. Pure.
export function pickActive(
  sections: { id: string; top: number }[],
  offset: number,
  atBottom: boolean,
  held: string | null,
): string {
  if (held && sections.some((s) => s.id === held)) return held
  if (atBottom && sections.length > 0) return sections[sections.length - 1].id
  return activeSection(sections, offset + SPY_EPSILON)
}
