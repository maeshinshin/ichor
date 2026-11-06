// Fails the build when a fenced code block under docs/ has no language, so every block is highlighted
// (docs/contributing-docs.md, "Code"). Runs before `astro build`. Only the docs on main are checked: a
// release's docs are frozen by its tag and must keep building.
import { readdirSync, readFileSync } from 'node:fs'
import { join, relative } from 'node:path'
import { docsRoot, isNext, repoRoot } from '../plugins/docs.mjs'

if (!isNext) {
  console.log('check-fences: skipped for a release (its docs are frozen)')
  process.exit(0)
}

const files = (dir) =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? files(join(dir, e.name)) : e.name.endsWith('.md') ? [join(dir, e.name)] : [],
  )

const bare = []
for (const file of files(docsRoot)) {
  let fence = null // the opening fence while inside a block
  readFileSync(file, 'utf8')
    .split('\n')
    .forEach((line, i) => {
      const m = line.match(/^\s*(`{3,}|~{3,})\s*(\S*)/)
      if (!fence) {
        if (!m) return
        fence = m[1]
        if (!m[2]) bare.push(`${relative(repoRoot, file)}:${i + 1}`)
      } else if (m && !m[2] && m[1][0] === fence[0] && m[1].length >= fence.length) fence = null
    })
}

if (bare.length) {
  console.error(`${bare.length} code fence(s) without a language (use sh, console, yaml, go, json or text):\n${bare.join('\n')}`)
  process.exit(1)
}
console.log('check-fences: every code fence has a language')
