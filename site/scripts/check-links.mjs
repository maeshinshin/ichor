// Fails the build when a page links to a page, file or #heading of this site that does not exist.
// Runs on dist/ after `astro build`, so a Markdown-only edit that breaks a link cannot ship.
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { versionBase } from '../plugins/docs.mjs'

const dist = 'dist'
const base = `${versionBase}/`

const pages = (dir) =>
  readdirSync(dir, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? pages(join(dir, e.name)) : e.name.endsWith('.html') ? [join(dir, e.name)] : [],
  )

const ids = new Map()
const idsOf = (file) => {
  if (!ids.has(file)) ids.set(file, new Set([...readFileSync(file, 'utf8').matchAll(/\sid="([^"]+)"/g)].map((m) => m[1])))
  return ids.get(file)
}

const broken = new Set()
for (const page of pages(dist)) {
  const html = readFileSync(page, 'utf8')
  for (const [, href] of html.matchAll(/\s(?:href|src)="([^"]+)"/g)) {
    if (!href.startsWith(base)) continue
    const [path, hash] = href.slice(base.length).split('#')
    const target = join(dist, decodeURI(path))
    const file = path === '' || path.endsWith('/') ? join(target, 'index.html') : target
    if (!existsSync(file)) broken.add(`${page}: ${href} (no such page)`)
    else if (hash && file.endsWith('.html') && !idsOf(file).has(decodeURI(hash))) broken.add(`${page}: ${href} (no such heading)`)
  }
}

if (broken.size) {
  console.error(`${broken.size} broken internal link(s):\n${[...broken].join('\n')}`)
  process.exit(1)
}
console.log('check-links: all internal links resolve')
