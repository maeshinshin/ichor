import type { FieldRow } from '@/lib/fieldSearch'

// The index is a separate chunk: the bundle only pays for it when someone
// opens the search.
let index: Promise<FieldRow[]> | undefined
export const loadFieldIndex = () => (index ??= import('@/gen/fieldindex.json').then((m) => m.default as FieldRow[]))
