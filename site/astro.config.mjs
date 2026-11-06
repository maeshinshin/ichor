// @ts-check
import { unified } from '@astrojs/markdown-remark'
import preact from '@astrojs/preact'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'astro/config'
import { remarkCallouts, remarkDocsLinks, remarkDropTitle, versionBase } from './plugins/docs.mjs'

// GitHub Pages project page: https://maeshinshin.github.io/ichor/. Override
// both for a fork or a custom domain (DOCS_SITE=https://docs.example.org DOCS_BASE=/).
// One build is one version of the docs, at <DOCS_BASE>/<DOCS_VERSION>/ (next, or a tag vX.Y.Z).
const site = process.env.DOCS_SITE ?? 'https://maeshinshin.github.io'
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
