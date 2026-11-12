// Builds every version of the docs into one tree, ready to be published:
//
//   <out>/next/         docs/ of the working tree (main), built with this site's code
//   <out>/vX.Y/         docs/ of each minor release line: the branch release-X.Y when it exists, else the
//                       newest tag vX.Y.* (`git tag -l 'v*'`), built with this same site's code
//   <out>/vX.Y.Z/       docs/ of each tag, frozen, built the same way; a tag whose docs do not build gets a redirect
//                       to the same page under vX.Y/ instead, so per-tag links keep working
//   <out>/latest/...    redirects to the newest minor (or to next when there is none)
//   <out>/index.html    redirect to latest; llms.txt and llms-full.txt of latest at the root
//   <out>/versions.json the list the version switcher fetches at runtime
//   <out>/NOTICE.txt, LICENSE.txt, THIRD-PARTY-NOTICES.txt   the repository's notice files, which every page footer links to
//
// A line's docs/ is exported with `git archive` of that ref into a temporary directory and read through DOCS_REPO, so
// links out of docs/ are checked against that ref too. The ref's own site/ (older tags have none) is not
// used: every version gets the current layout, search and switcher, and only the content is frozen.
//
//   node scripts/build-all.mjs [outDir]     (make docs-site-all)
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readdirSync, renameSync, rmSync, writeFileSync, copyFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { rootBase } from '../plugins/docs.mjs'

const out = resolve(process.argv[2] ?? 'dist-all')
const run = (cmd, args, opts = {}) => execFileSync(cmd, args, { stdio: 'inherit', ...opts })
const tags = execFileSync('git', ['tag', '-l', 'v*'], { encoding: 'utf8' })
  .split('\n')
  .filter((t) => /^v\d+\.\d+\.\d+$/.test(t))
  .sort((a, b) => {
    const [x, y] = [a, b].map((t) => t.slice(1).split('.').map(Number))
    return y[0] - x[0] || y[1] - x[1] || y[2] - x[2]
  })

// Minor lines, newest first; each documents its release-X.Y branch (remote first, as CI has only that) or else its newest tag.
const refExists = (r) => { try { execFileSync('git', ['rev-parse', '--verify', '-q', r], { stdio: 'ignore' }); return true } catch { return false } }
const lines = [...new Set(tags.map((t) => t.replace(/\.\d+$/, '')))].map((name) => {
  const patch = tags.find((t) => t.startsWith(`${name}.`)) // tags are sorted newest first
  const branch = `release-${name.slice(1)}`
  const ref = [`origin/${branch}`, branch].find(refExists)
  return { name, patch, ref: ref ?? patch, gitRef: ref ? branch : patch }
})

rmSync(out, { recursive: true, force: true })
mkdirSync(out, { recursive: true })

function build(name, repoDir, gitRef, patch) {
  rmSync('dist', { recursive: true, force: true })
  rmSync('.astro', { recursive: true, force: true }) // the content cache must not carry pages of another version
  run('npm', ['run', 'build'], { env: { ...process.env, DOCS_VERSION: name, DOCS_RELEASE: patch, ...(repoDir && { DOCS_REPO: repoDir, DOCS_GITREF: gitRef }) } })
  renameSync('dist', join(out, name))
}

build('next')
// A release's docs are frozen, so one that cannot be built (say a broken link in an old tag) is left out with
// a warning instead of blocking every later publish; next must always build.
const buildRef = (name, ref, gitRef, patch) => {
  const dir = mkdtempSync(join(tmpdir(), `ichor-docs-${name}-`))
  try {
    run('sh', ['-c', 'git -C .. archive "$1" | tar -x -C "$2"', 'sh', ref, dir])
    build(name, dir, gitRef, patch)
    return true
  } catch (e) {
    console.error(`::warning::the docs of ${name} (${ref}) did not build and are left out: ${e.message}`)
    return false
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}
const releases = lines.filter((l) => buildRef(l.name, l.ref, l.gitRef, l.patch))
const built = new Set() // tags with a frozen copy of their own
for (const l of releases) l.patches = tags.filter((t) => t.startsWith(`${l.name}.`)).map((t) => ({ name: t, path: `${t}/` }))
for (const l of releases) for (const t of l.patches) if (buildRef(t.name, t.name, t.name, t.name)) built.add(t.name)
const latest = releases[0]?.name

writeFileSync(
  join(out, 'versions.json'),
  JSON.stringify([{ name: 'next', path: 'next/', isLatest: false, isNext: true }, ...releases.map((l) => ({ name: l.name, path: `${l.name}/`, patch: l.patch, patches: l.patches, isLatest: l.name === latest, isNext: false }))], null, 2) + '\n',
)

// The licence notices travel with the site: the footer of every page links to these, from main's tree (the build's own).
for (const f of ['NOTICE', 'LICENSE', 'THIRD-PARTY-NOTICES']) copyFileSync(join('..', f), join(out, `${f}.txt`)) // .txt, so a browser shows it as text

const redirect = (to) =>
  `<!doctype html><html lang="en"><meta charset="utf-8"><title>Redirecting</title><link rel="canonical" href="${to}"><meta http-equiv="refresh" content="0; url=${to}"><script>location.replace(${JSON.stringify(to)} + location.hash)</script><a href="${to}">${to}</a></html>\n`
const write = (file, html) => {
  mkdirSync(dirname(file), { recursive: true })
  writeFileSync(file, html)
}

// latest/<page>/ -> <newest release>/<page>/, for every page, so a deep link to "latest" keeps working.
const target = latest ?? 'next'
const pages = (dir, rel = '') => readdirSync(join(dir, rel), { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? pages(dir, join(rel, e.name)) : e.name === 'index.html' ? [rel] : []))
for (const rel of pages(join(out, target))) write(join(out, 'latest', rel, 'index.html'), redirect(`${rootBase}/${target}/${rel && `${rel}/`}`))
// vX.Y.Z/<page>/ -> vX.Y/<page>/ for every tag of a built line that has no copy of its own (pages the line has dropped since stay 404; the overview always redirects).
for (const l of releases) for (const t of tags.filter((t) => t.startsWith(`${l.name}.`) && !built.has(t))) for (const rel of pages(join(out, l.name))) write(join(out, t, rel, 'index.html'), redirect(`${rootBase}/${l.name}/${rel && `${rel}/`}`))
write(join(out, 'index.html'), redirect(`${rootBase}/${target}/`))
for (const f of ['llms.txt', 'llms-full.txt']) if (existsSync(join(out, target, f))) copyFileSync(join(out, target, f), join(out, f))
console.log(`build-all: ${['next', ...releases.map((l) => l.name)].join(', ')} -> ${out} (latest: ${target})`)
