// Markdown plumbing for rendering ../docs as written for GitHub (no copies, no
// front matter required). Shared by astro.config.mjs and src/content.config.ts.
import { execFileSync } from 'node:child_process'
import { existsSync, statSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import { visit } from 'unist-util-visit'

/**
 * The version being built: `next` (the docs on main, the default) or a minor release line `vX.Y`
 * (DOCS_VERSION). Its pages live under `<DOCS_BASE>/<version>/`; DOCS_BASE (default `/`,
 * `/` for a domain root) is where all versions sit together.
 */
export const version = process.env.DOCS_VERSION ?? 'next'
export const isNext = version === 'next'
/** Where all versions sit, without a trailing slash (empty at a domain root). */
export const rootBase = (process.env.DOCS_BASE ?? '/').replace(/\/+$/, '')
export const versionBase = `${rootBase}/${version}`
/** The git ref the docs of this version were written at: GitHub links out of docs/ point there. */
export const gitRef = process.env.DOCS_GITREF ?? (isNext ? 'main' : version)

/**
 * The release the docs document, without the leading `v`: DOCS_RELEASE (build-all sets it to the patch of
 * the line), else the newest `vX.Y.Z` tag, so `next` shows the newest release, never the unreleased one.
 * Pages write it as `{{version}}`.
 */
const releaseTags = execFileSync('git', ['tag', '-l', 'v*'], { encoding: 'utf8' })
  .split('\n')
  .filter((t) => /^v\d+\.\d+\.\d+$/.test(t))
  .sort((a, b) => a.localeCompare(b, undefined, { numeric: true }))
export const release = (process.env.DOCS_RELEASE ?? releaseTags.at(-1)?.slice(1) ?? 'X.Y.Z').replace(/^v/, '')
/**
 * The newest release tag and its minor line, as versions.json will list them (build-all lists the same tags). The version
 * banner is rendered with them at build time, so it is complete in the HTML and the page does not move when the browser
 * has fetched versions.json.
 */
export const latestPatch = releaseTags.at(-1)
export const latestLine = latestPatch?.replace(/\.\d+$/, '')
export const withVersion = (s) => s.replaceAll('{{version}}', release)

/** `{{version}}` in text, inline code and code blocks becomes the release being documented. */
export function remarkVersion() {
  return (tree) => visit(tree, ['text', 'inlineCode', 'code'], (n) => { n.value = withVersion(n.value) })
}

/**
 * The docs/ directory and the repository root it sits in (Astro runs with site/ as the working
 * directory). DOCS_REPO is a checkout of a release tag to build its docs/ with this site's code.
 */
export const repoRoot = process.env.DOCS_REPO ? resolve(process.env.DOCS_REPO) : resolve(process.cwd(), '..')
export const docsRoot = resolve(repoRoot, 'docs')
export const repoUrl = 'https://github.com/maeshinshin/ichor'

/** `guides/README.md` -> `guides`, `index.md` -> ``, `guides/x.md` -> `guides/x`. */
export function routeOf(relPath) {
  return relPath
    .split(sep)
    .join('/')
    .replace(/\.md$/, '')
    .replace(/(^|\/)(index|README)$/, '')
}

/** Site URL (with `base`, which ends in `/`) of a route. */
export function urlOf(base, route) {
  return route ? `${base}${route}/` : base
}

const external = /^([a-z][a-z0-9+.-]*:|\/\/|\/|#)/i

/**
 * Relative links between docs become site URLs; relative links that leave
 * docs/ (source, manifests) become GitHub links. A link to a file that does
 * not exist fails the build.
 */
export function remarkDocsLinks({ base }) {
  return (tree, file) => {
    const from = dirname(file.path)
    visit(tree, ['link', 'definition', 'image'], (node, index, parent) => {
      if (external.test(node.url)) return
      const source = node.url
      try {
        node.url = resolveLink(source, from, base)
        // A diagram is a pair, NAME.light.drawio.svg and NAME.dark.drawio.svg: the Markdown
        // names the light one, which is what GitHub shows, and the site shows the dark one in the dark theme.
        // Each image links to its SVG, which opens at full size (the page column is narrower than the diagram).
        if (node.type === 'image' && source.endsWith('.light.drawio.svg')) {
          // The diagram keeps a readable size (640 px) on a narrow screen and scrolls inside its link, not the page.
          node.data = { hProperties: { className: ['min-w-[640px]'] } }
          const dark = { ...node, url: resolveLink(source.replace('.light.', '.dark.'), from, base) }
          const open = (image, className) => ({ type: 'link', url: image.url, children: [image], data: { hProperties: { className: ['overflow-x-auto', ...className], target: '_blank', rel: ['noopener'], title: 'Open the diagram at full size' } } })
          parent.children.splice(index, 1, open(node, ['block', 'dark:hidden']), open(dark, ['hidden', 'dark:block']))
          return index + 2
        }
      } catch (e) {
        file.fail(e.message, node)
      }
    })
  }
}

/**
 * Where a relative link in a docs file points: the site path of another docs page, or a
 * GitHub URL for anything else in the repository. Throws if the target does not exist.
 */
export function resolveLink(url, fromDir, base) {
  const [path, hash = ''] = url.split('#')
  const target = resolve(fromDir, decodeURI(path))
  if (!existsSync(target)) throw new Error(`broken link "${url}" (no such file)`)
  // docs/adr is not published, so a link into it goes to GitHub like any other repository file.
  if (!relative(docsRoot, target).startsWith('..') && target.endsWith('.md') && !relative(docsRoot, target).startsWith(`adr${sep}`)) {
    return urlOf(base, routeOf(relative(docsRoot, target))) + (hash && `#${hash}`)
  }
  // Images in docs/ are served by the site itself (src/pages/[...slug].svg.ts and .png.ts), at the same path.
  if (!relative(docsRoot, target).startsWith('..') && /\.(svg|png)$/.test(target)) {
    return base + relative(docsRoot, target).split(sep).join('/') + (hash && `#${hash}`)
  }
  const kind = statSync(target).isDirectory() ? 'tree' : 'blob'
  return `${repoUrl}/${kind}/${gitRef}/${relative(repoRoot, target).split(sep).join('/')}${hash && `#${hash}`}`
}

/**
 * A docs file's Markdown with every link made absolute, so a copy of it works outside the
 * site: links to other pages are site URLs, links out of docs/ are GitHub URLs, `#anchors`
 * point at `pageUrl`. Fenced code is left alone.
 */
export function absoluteMarkdown(markdown, filePath, base, origin, pageUrl) {
  const from = dirname(filePath)
  const fix = (u) => (u.startsWith('#') ? pageUrl + u : external.test(u) ? u : new URL(resolveLink(u, from, base), origin).href)
  let fence = ''
  return markdown
    .split('\n')
    .map((line) => {
      const m = /^\s*(`{3,}|~{3,})/.exec(line)
      if (m) {
        if (!fence) fence = m[1]
        else if (m[1][0] === fence[0] && m[1].length >= fence.length) fence = ''
        return line
      }
      if (fence) return line
      return line.replace(/(`+).*?\1|(\]\()([^)\s]+)/g, (m, tick, a, u) => (tick ? m : a + fix(u))).replace(/^(\s*\[[^\]]+\]:\s*)(\S+)/, (_, a, u) => a + fix(u))
    })
    .join('\n')
}

/**
 * The page layout prints the title as an h1, so the Markdown's own is dropped:
 * a `# ` heading, or a first heading that repeats the front matter title
 * (the generated CLI pages start with `## ichor adopt`).
 */
export function remarkDropTitle() {
  return (tree, file) => {
    const title = file.data.astro?.frontmatter?.title
    const text = (n) => (n.children ?? []).map((c) => c.value ?? text(c)).join('')
    const i = tree.children.findIndex((n) => n.type === 'heading')
    const h = tree.children[i]
    if (h && (h.depth === 1 || text(h) === title)) tree.children.splice(i, 1)
  }
}

const calloutMarker = /^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n?/i

/**
 * GitHub alerts (`> [!NOTE]`) become <div data-callout="note">; the Prose
 * component swaps those for the preactcn Alert.
 */
export function remarkCallouts() {
  return (tree) => {
    visit(tree, 'blockquote', (node) => {
      const text = node.children[0]?.type === 'paragraph' ? node.children[0].children[0] : undefined
      const m = text?.type === 'text' && calloutMarker.exec(text.value)
      if (!m) return
      text.value = text.value.slice(m[0].length)
      if (!text.value) node.children[0].children.shift()
      if (!node.children[0].children.length) node.children.shift()
      node.data = { hName: 'div', hProperties: { 'data-callout': m[1].toLowerCase() } }
    })
  }
}
