import { useEffect, useRef, useState } from 'preact/hooks'
import { Command, CommandDialog, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'

// The slice of Pagefind's JS API this uses (https://pagefind.app/docs/api/). The bundle is
// written next to the pages by `pagefind --site dist` after the Astro build.
type SubResult = { title: string; url: string; excerpt: string }
type Result = { url: string; meta: { title?: string }; excerpt: string; sub_results: SubResult[] }
type Pagefind = { debouncedSearch(q: string): Promise<{ results: { data(): Promise<Result> }[] } | null> }

type Hit = { url: string; title: string; excerpt: string }

let pagefind: Promise<Pagefind> | undefined
const load = (base: string) => (pagefind ??= import(/* @vite-ignore */ `${base}pagefind/pagefind.js`))

// Pagefind reports paths from the root of dist; the site lives under `base`.
const withBase = (base: string, url: string) => (url.startsWith(base) ? url : base + url.replace(/^\//, ''))

export function SearchDialog({ base, open, onOpenChange }: { base: string; open: boolean; onOpenChange: (open: boolean) => void }) {
  const [query, setQuery] = useState('')
  const [hits, setHits] = useState<Hit[]>([])
  const [error, setError] = useState(false)
  const latest = useRef(0)

  useEffect(() => {
    const id = ++latest.current
    if (!query.trim()) {
      setHits([])
      return
    }
    load(base)
      .then((pf) => pf.debouncedSearch(query))
      .then(async (res) => {
        if (!res || id !== latest.current) return
        const data = await Promise.all(res.results.slice(0, 8).map((r) => r.data()))
        if (id !== latest.current) return
        setError(false)
        setHits(
          data.map((d) => {
            const sub = d.sub_results[0]
            return { url: withBase(base, sub?.url ?? d.url), title: d.meta.title ?? sub?.title ?? d.url, excerpt: sub?.excerpt ?? d.excerpt }
          }),
        )
      })
      .catch(() => setError(true))
  }, [query, base])

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title="Search the docs" description="Search pages and headings" className="sm:max-w-xl">
      <Command shouldFilter={false}>
        <CommandInput placeholder="Search the docs..." value={query} onValueChange={setQuery} />
        <CommandList>
          {query.trim() && (
            <CommandEmpty>
              {error ? 'Search needs the built site (make docs-site, then make docs-preview).' : 'No results.'}
            </CommandEmpty>
          )}
          {hits.map((h) => (
            <CommandItem key={h.url} value={h.url} onSelect={() => (window.location.href = h.url)}>
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="truncate font-medium">{h.title}</span>
                {/* Pagefind encodes the excerpt's entities and marks matches with <mark>. */}
                <span className="line-clamp-2 text-xs text-muted-foreground [&_mark]:rounded-sm [&_mark]:bg-primary/20 [&_mark]:text-foreground" dangerouslySetInnerHTML={{ __html: h.excerpt }} />
              </div>
            </CommandItem>
          ))}
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
