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
