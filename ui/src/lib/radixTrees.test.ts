import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'

// ui/src/lib/radix and site/src/lib/radix are the same vendored Radix port, written by separate `shadcn add` runs.
// A fix made in one that the other lacks is a behaviour (dismiss, focus, scroll lock) that is right in one app and wrong in the other.
const ui = new URL('./radix/', import.meta.url)
const site = new URL('../../../site/src/lib/radix/', import.meta.url)
const files = (root: URL) => readdirSync(root, { recursive: true, encoding: 'utf8' }).filter((f) => /\.tsx?$/.test(f))
const read = (root: URL, f: string) => readFileSync(new URL(f, root), 'utf8')

test('a file in both radix trees is identical', () => {
  const inSite = new Set(files(site))
  const shared = files(ui).filter((f) => inSite.has(f))
  assert.ok(shared.length > 15, `only ${shared.length} shared files: the trees moved`)
  const drifted = shared.filter((f) => read(ui, f) !== read(site, f))
  assert.deepEqual(drifted, [], 'copy the one with the fix over the other (a plain `shadcn add` offers the registry file, which has none of the fixes)')
})

// `shadcn add --overwrite` in both trees would keep them identical and lose these fixes in both.
test('both radix trees keep the local fixes to the registry files', () => {
  for (const root of [ui, site]) {
    const dismiss = read(root, 'internal/dismissable-layer.ts')
    assert.match(dismiss, /const isTop = /, 'only the top layer reacts to an outside press')
    assert.match(dismiss, /TAP_SLOP/, 'a touch tap outside dismisses on pointerup, not on a click iOS never sends')
    assert.doesNotMatch(read(root, 'internal/modal-layer.ts'), /paddingRight/, 'scroll lock relies on `html { scrollbar-gutter: stable }`')
  }
})

// The portal reads Preact's minified internals, so a Preact that moves on its own would break it where nothing renders to notice.
test('both apps pin Preact to an exact version', () => {
  for (const app of ['../../package.json', '../../../site/package.json']) {
    const { preact } = JSON.parse(readFileSync(new URL(app, import.meta.url), 'utf8')).dependencies
    assert.match(preact, /^\d+\.\d+\.\d+$/, `${app} has preact ${preact}`)
  }
})
