import { defineCollection } from 'astro:content'
import { glob } from 'astro/loaders'
import { z } from 'astro/zod'
import { docsRoot, routeOf } from '../plugins/docs.mjs'

// The repo's own Markdown, read in place. Front matter is optional: the title
// is the first `# ` heading, the order is alphabetical.
const docs = defineCollection({
  loader: glob({
    pattern: '**/*.md',
    base: docsRoot,
    generateId: ({ entry }) => routeOf(entry) || 'index',
  }),
  schema: z.object({
    title: z.string().optional(),
    sidebar_label: z.string().optional(),
    sidebar_order: z.number().optional(),
  }),
})

export const collections = { docs }
