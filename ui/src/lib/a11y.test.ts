import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { test } from 'node:test'

// Every page and component file under ui/src except the stock components/ui/ copies.
function sources(dir: URL): URL[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const u = new URL(e.name + (e.isDirectory() ? '/' : ''), dir)
    if (e.isDirectory()) return u.pathname.endsWith('/components/ui/') ? [] : sources(u)
    return e.name.endsWith('.tsx') ? [u] : []
  })
}

test('every SelectTrigger has an id (for a Label) or an aria-label', () => {
  const bad: string[] = []
  for (const f of sources(new URL('../', import.meta.url))) {
    for (const m of readFileSync(f, 'utf8').matchAll(/<SelectTrigger\b[^>]*>/g)) {
      if (!/\sid=|\saria-label=/.test(m[0])) bad.push(f.pathname.split('/src/')[1])
    }
  }
  assert.deepEqual(bad, [])
})
