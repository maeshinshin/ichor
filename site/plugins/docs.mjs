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
      const [path, hash = ''] = node.url.split('#')
      const target = resolve(from, decodeURI(path))
      if (!existsSync(target)) {
        file.fail(`broken link "${node.url}" (no such file)`, node)
      }
      const inDocs = !relative(docsRoot, target).startsWith('..')
      if (inDocs && target.endsWith('.md')) {
        node.url = urlOf(base, routeOf(relative(docsRoot, target))) + (hash && `#${hash}`)
        return
      }
      const repoPath = relative(repoRoot, target).split(sep).join('/')
      const kind = statSync(target).isDirectory() ? 'tree' : 'blob'
      node.url = `${repoUrl}/${kind}/main/${repoPath}${hash && `#${hash}`}`
    })
  }
}

/** The page layout prints the title as an h1, so the Markdown's own is dropped. */
export function remarkDropTitle() {
  return (tree) => {
    const i = tree.children.findIndex((n) => n.type === 'heading' && n.depth === 1)
    if (i >= 0) tree.children.splice(i, 1)
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
