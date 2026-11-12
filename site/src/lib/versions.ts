/** One entry of versions.json, which the CI writes at the root of the site from the minor release lines. */
export type Version = { name: string; path: string; patch?: string; patches?: { name: string; path: string }[]; isLatest: boolean; isNext: boolean }

let cache: Promise<Version[]> | undefined
/** versions.json is fetched at runtime so an old build lists releases made after it; none found is an empty list. */
export const loadVersions = (root: string) =>
  (cache ??= fetch(`${root}versions.json`)
    .then((r) => (r.ok ? (r.json() as Promise<Version[]>) : []))
    .catch(() => []))

/** `v0.2 (latest)`: the line, or the patch, and whether it is the newest line. */
export const label = (v: Pick<Version, 'name' | 'isNext' | 'isLatest'>) =>
  v.isNext ? 'next (main)' : `${v.name}${v.isLatest ? ' (latest)' : ''}`

/** The same page in another version if it exists there, else that version's overview. */
export async function pageIn(root: string, current: string, to: Pick<Version, 'path'>): Promise<string> {
  const rest = location.pathname.slice(`${root}${current}/`.length)
  const same = `${root}${to.path}${rest}`
  const ok = rest !== '' && (await fetch(same, { method: 'HEAD' }).then((r) => r.ok, () => false))
  return ok ? same + location.hash : `${root}${to.path}`
}
