import { getDocs, llmsIndex } from '@/lib/docs'

export const GET = async () => new Response(llmsIndex(await getDocs()), { headers: { 'content-type': 'text/plain; charset=utf-8' } })
