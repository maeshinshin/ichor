// Markdown plumbing for rendering ../docs as written for GitHub (no copies, no
// front matter required). Shared by astro.config.mjs and src/content.config.ts.
import { existsSync, statSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import { visit } from 'unist-util-visit'

/** The docs/ directory and the repository root it sits in (Astro runs with site/ as the working directory). */
export const repoRoot = resolve(process.cwd(), '..')
export const docsRoot = resolve(repoRoot, 'docs')
export const repoUrl = 'https://github.com/maeshinshin/ichor'

/** `adr/README.md` -> `adr`, `index.md` -> ``, `guides/x.md` -> `guides/x`. */
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
    visit(tree, ['link', 'definition'], (node) => {
      if (external.test(node.url)) return
      try {
        node.url = resolveLink(node.url, from, base)
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
  if (!relative(docsRoot, target).startsWith('..') && target.endsWith('.md')) {
    return urlOf(base, routeOf(relative(docsRoot, target))) + (hash && `#${hash}`)
  }
  const kind = statSync(target).isDirectory() ? 'tree' : 'blob'
  return `${repoUrl}/${kind}/main/${relative(repoRoot, target).split(sep).join('/')}${hash && `#${hash}`}`
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
