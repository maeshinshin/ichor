import { ChevronDown } from 'lucide-preact'
import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { ScrollArea } from '@/components/ui/scroll-area'
import { cn } from '@/lib/utils'

export type Heading = { depth: number; slug: string; text: string }

function Links({ headings, active, onPick }: { headings: Heading[]; active?: string; onPick?: () => void }) {
  return (
    <ul className="flex flex-col gap-1 text-sm">
      {headings.map((h) => (
        <li key={h.slug} className={cn(h.depth > 2 && 'pl-3')}>
          <a
            href={`#${h.slug}`}
            onClick={onPick}
            aria-current={h.slug === active ? 'location' : undefined}
            className={cn('block py-0.5 text-muted-foreground hover:text-foreground', h.slug === active && 'font-medium text-foreground')}
          >
            {h.text}
          </a>
        </li>
      ))}
    </ul>
  )
}

/** The heading whose section is being read: the last one scrolled above the top of the window. */
function useActive(headings: Heading[]) {
  const [active, setActive] = useState<string>()
  useEffect(() => {
    const els = headings.map((h) => document.getElementById(h.slug)).filter((e): e is HTMLElement => !!e)
    const update = () => {
      let cur = els[0]?.id
      for (const e of els) if (e.getBoundingClientRect().top <= 120) cur = e.id
      setActive(cur)
    }
    update()
    window.addEventListener('scroll', update, { passive: true })
    return () => window.removeEventListener('scroll', update)
  }, [headings])
  return active
}

/** Desktop: the right-hand "On this page" column. */
export function TocAside({ headings }: { headings: Heading[] }) {
  const active = useActive(headings)
  return (
    <nav aria-label="On this page" className="sticky top-20 hidden max-h-[calc(100svh-6rem)] xl:block">
      <p className="mb-2 text-sm font-medium">On this page</p>
      <ScrollArea className="max-h-[calc(100svh-9rem)]">
        <Links headings={headings} active={active} />
      </ScrollArea>
    </nav>
  )
}

/** Below xl: a collapsible list at the top of the article. */
export function TocCollapsible({ headings }: { headings: Heading[] }) {
  const [open, setOpen] = useState(false)
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="xl:hidden">
      <CollapsibleTrigger asChild>
        <Button variant="outline" className="w-full justify-between">
          On this page
          <ChevronDown className={cn('transition-transform', open && 'rotate-180')} />
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent className="px-3 py-2">
        <Links headings={headings} onPick={() => setOpen(false)} />
      </CollapsibleContent>
    </Collapsible>
  )
}
