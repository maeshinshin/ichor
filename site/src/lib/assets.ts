import type { APIRoute } from 'astro'
import { readdirSync, readFileSync } from 'node:fs'
import { join, sep } from 'node:path'
import { docsRoot } from '../../plugins/docs.mjs'

// The images of docs/ (the draw.io diagrams in docs/images, say) are served at their own path, next to the pages:
// docs/images/x.svg is <base>/images/x.svg. One endpoint per type, src/pages/[...slug].svg.ts and .png.ts.
const types = { svg: 'image/svg+xml', png: 'image/png' } as const

export const assetPaths = (ext: keyof typeof types) =>
  readdirSync(docsRoot, { recursive: true, encoding: 'utf8' })
    .filter((f) => f.endsWith(`.${ext}`))
    .map((f) => ({ params: { slug: f.split(sep).join('/').slice(0, -ext.length - 1) } }))

export const assetResponse =
  (ext: keyof typeof types): APIRoute =>
  ({ params }) =>
    new Response(readFileSync(join(docsRoot, `${params.slug}.${ext}`)), { headers: { 'content-type': types[ext] } })
