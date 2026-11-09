// @ts-check
import { unified } from '@astrojs/markdown-remark'
import preact from '@astrojs/preact'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'astro/config'
import { remarkCallouts, remarkDocsLinks, remarkDropTitle, versionBase } from './plugins/docs.mjs'

// The defaults are the published site, https://ichor.maesh.dev/; DOCS_SITE and DOCS_BASE move it (a fork).
// One build is one version of the docs, at <DOCS_BASE>/<DOCS_VERSION>/ (next, or a tag vX.Y.Z).
const site = process.env.DOCS_SITE ?? 'https://ichor.maesh.dev'
const base = versionBase
const baseSlash = `${base}/`

export default defineConfig({
  site,
  base,
  trailingSlash: 'always',
  integrations: [preact()],
  vite: { plugins: [tailwindcss()] },
  markdown: {
    // Plain typography like GitHub's: smartypants would turn `--flag` into an em dash.
    processor: unified({ smartypants: false, remarkPlugins: [[remarkDocsLinks, { base: baseSlash }], remarkDropTitle, remarkCallouts] }),
    shikiConfig: { themes: { light: 'github-light', dark: 'github-dark' }, defaultColor: false },
  },
})
