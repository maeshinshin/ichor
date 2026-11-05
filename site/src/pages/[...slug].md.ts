// The Markdown of every page, next to its HTML: /guides/x.md for /guides/x/, /index.md for the home page.
import type { APIRoute } from 'astro'
import { getDocs, pageMarkdown } from '@/lib/docs'

export async function getStaticPaths() {
  return (await getDocs()).map((d) => ({ params: { slug: d.route || 'index' }, props: { id: d.entry.id } }))
}

export const GET: APIRoute = async ({ props }) => {
  const doc = (await getDocs()).find((d) => d.entry.id === props.id)!
  return new Response(pageMarkdown(doc), { headers: { 'content-type': 'text/markdown; charset=utf-8' } })
}
