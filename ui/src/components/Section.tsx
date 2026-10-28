import type { ComponentChildren } from 'preact'

// Section is one stable-id block of a detail page; the id is the anchor that
// #links point at. Its scroll-mt-section keeps a jumped-to heading clear of the
// sticky top bar.
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
