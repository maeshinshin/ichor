import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { activeSection } from '@/lib/sectionNav'
import { cn } from '@/lib/utils'
import { useT } from '@/lib/useT'

export type SectionLink = { id: string; label: string }

// The sticky top bar is h-14; sections stop a little below it.
const SCROLL_OFFSET = 88

function jump(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// useActiveSection follows the scroll position. It recomputes on scroll and
// resize, once per frame, from the sections' measured tops.
function useActiveSection(ids: string[]): string {
  const [active, setActive] = useState(ids[0] ?? '')
  const key = ids.join('|')
  useEffect(() => {
    let frame = 0
    const update = () => {
      frame = 0
      const tops = ids.flatMap((id) => {
        const el = document.getElementById(id)
        return el ? [{ id, top: el.getBoundingClientRect().top }] : []
      })
      // At the very bottom the last section may never reach the offset.
      const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2
      setActive(atBottom && tops.length > 0 ? tops[tops.length - 1].id : activeSection(tops, SCROLL_OFFSET))
    }
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(update)
    }
    update()
    window.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    return () => {
      window.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
      if (frame) cancelAnimationFrame(frame)
    }
  }, [key])
  return active
}

// SectionLayout puts the page content next to an "On this page" list that
// jumps to each section and highlights the current one. From xl up the list is
// a sticky right-hand column; below that it is a row of jump links that scrolls
// inside itself, so it never widens the page.
export function SectionLayout({ sections, children }: { sections: SectionLink[]; children: ComponentChildren }) {
  const t = useT()
  const active = useActiveSection(sections.map((s) => s.id))
  return (
    <div class="xl:grid xl:grid-cols-[minmax(0,1fr)_13rem] xl:gap-8">
      <nav
        aria-label={t('nav.jumpTo')}
        class="sticky top-14 z-10 -mx-4 mb-4 flex gap-1 overflow-x-auto border-b bg-background/95 px-4 py-2 backdrop-blur sm:-mx-6 sm:px-6 xl:hidden"
      >
        {sections.map((s) => (
          <a
            key={s.id}
            href={`#${s.id}`}
            onClick={(e) => {
              e.preventDefault()
              jump(s.id)
            }}
            aria-current={s.id === active ? 'location' : undefined}
            class={cn(
              'shrink-0 rounded-md px-2.5 py-1 text-sm text-muted-foreground hover:text-foreground',
              s.id === active && 'bg-muted font-medium text-foreground',
            )}
          >
            {s.label}
          </a>
        ))}
      </nav>
      <div class="min-w-0 space-y-6">{children}</div>
      <nav aria-label={t('nav.onThisPage')} class="hidden xl:block">
        <div class="sticky top-20">
          <p class="mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">{t('nav.onThisPage')}</p>
          <ul class="space-y-1 border-l">
            {sections.map((s) => (
              <li key={s.id}>
                <a
                  href={`#${s.id}`}
                  onClick={(e) => {
                    e.preventDefault()
                    jump(s.id)
                  }}
                  aria-current={s.id === active ? 'location' : undefined}
                  class={cn(
                    '-ml-px block border-l-2 border-transparent py-1 pl-3 text-sm text-muted-foreground hover:text-foreground',
                    s.id === active && 'border-primary font-medium text-foreground',
                  )}
                >
                  {s.label}
                </a>
              </li>
            ))}
          </ul>
        </div>
      </nav>
    </div>
  )
}

// Section is one stable-id block of a detail page; the id is the jump target.
export function Section({
  id,
  title,
  actions,
  children,
}: {
  id: string
  title: ComponentChildren
  actions?: ComponentChildren
  children: ComponentChildren
}) {
  return (
    <Card id={id} className="scroll-mt-24">
      <CardHeader className="flex flex-wrap items-center justify-between gap-2">
        <CardTitle className="flex items-center gap-2">{title}</CardTitle>
        {actions}
      </CardHeader>
      <CardContent className="space-y-4 text-sm">{children}</CardContent>
    </Card>
  )
}
