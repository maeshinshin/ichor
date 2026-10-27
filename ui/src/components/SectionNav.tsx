import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { pickActive } from '@/lib/sectionNav'
import { cn } from '@/lib/utils'
import { useT } from '@/lib/useT'

export type SectionLink = { id: string; label: string }

// A clicked id is held as active until its smooth scroll ends.
let held: string | null = null
let release = 0
const listeners = new Set<() => void>()

function releaseHold() {
  held = null
  window.removeEventListener('scrollend', releaseHold)
  clearTimeout(release)
  listeners.forEach((f) => f())
}

function jump(id: string) {
  const el = document.getElementById(id)
  if (!el) return
  held = id
  listeners.forEach((f) => f())
  window.addEventListener('scrollend', releaseHold) // same fn: added once
  clearTimeout(release)
  release = window.setTimeout(releaseHold, 1200) // browsers without scrollend
  el.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

// useActiveSection follows the scroll position. It recomputes on scroll and
// resize, once per frame, from the sections' measured tops. The threshold is
// the sections' own scroll-margin-top, the same value jump() stops at.
function useActiveSection(ids: string[]): string {
  const [active, setActive] = useState(ids[0] ?? '')
  const key = ids.join('|')
  useEffect(() => {
    let frame = 0
    const update = () => {
      frame = 0
      const els = ids.flatMap((id) => {
        const el = document.getElementById(id)
        return el ? [{ id, el }] : []
      })
      const tops = els.map(({ id, el }) => ({ id, top: el.getBoundingClientRect().top }))
      const offset = els.length > 0 ? parseFloat(getComputedStyle(els[0].el).scrollMarginTop) || 0 : 0
      const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2
      setActive(pickActive(tops, offset, atBottom, held))
    }
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(update)
    }
    update()
    listeners.add(update)
    window.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    return () => {
      listeners.delete(update)
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
      <div class="min-w-0 space-y-8">{children}</div>
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
    <section id={id} class="scroll-mt-section">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-2 border-b pb-2">
        <h2 class="flex items-center gap-2 text-base font-semibold">{title}</h2>
        {actions}
      </div>
      <div class="space-y-4 text-sm [&_[data-slot=table-container]]:rounded-md [&_[data-slot=table-container]]:border [&_td]:px-3 [&_td]:py-2 [&_th]:h-9 [&_th]:bg-muted/40 [&_th]:px-3 [&_th]:text-xs [&_th]:text-muted-foreground [&_tr:last-child]:border-b-0">{children}</div>
    </section>
  )
}
