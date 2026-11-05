// @ts-check
import { unified } from '@astrojs/markdown-remark'
import preact from '@astrojs/preact'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'astro/config'
import { remarkCallouts, remarkDocsLinks, remarkDropTitle } from './plugins/docs.mjs'

// GitHub Pages project page: https://maeshinshin.github.io/ichor/. Override
// both for a fork or a custom domain (DOCS_SITE=https://docs.example.org DOCS_BASE=/).
const site = process.env.DOCS_SITE ?? 'https://maeshinshin.github.io'
const base = process.env.DOCS_BASE ?? '/ichor'
const baseSlash = base.endsWith('/') ? base : `${base}/`

export default defineConfig({
  site,
  base,
  trailingSlash: 'always',
  integrations: [preact()],
  vite: { plugins: [tailwindcss()] },
  markdown: {
    processor: unified({ remarkPlugins: [[remarkDocsLinks, { base: baseSlash }], remarkDropTitle, remarkCallouts] }),
    shikiConfig: { themes: { light: 'github-light', dark: 'github-dark' }, defaultColor: false },
  },
})
