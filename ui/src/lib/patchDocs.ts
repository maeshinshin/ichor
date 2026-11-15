// Pure helpers for the patch screens. No DOM, no preact and no ui/src/gen import,
// so it runs under node --test.

// previewKey identifies what a preview was run for, so the screen can tell
// when the form has changed since (the preview is stale).
export function previewKey(f: { cluster: string; layer: number; priority: number; target: string; config: string }): string {
  return JSON.stringify([f.cluster, f.layer, f.priority, f.target, f.config])
}

// configSections are the top-level sections of a patch's config (machine, cluster),
// sorted; none when the text is not a JSON object.
export function configSections(config: string): string[] {
  try {
    const v: unknown = JSON.parse(config)
    return typeof v === 'object' && v !== null && !Array.isArray(v) ? Object.keys(v).sort() : []
  } catch {
    return []
  }
}
