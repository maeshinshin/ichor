import { getDocs, llmsFull } from '@/lib/docs'

export const GET = async () => new Response(llmsFull(await getDocs()), { headers: { 'content-type': 'text/plain; charset=utf-8' } })
