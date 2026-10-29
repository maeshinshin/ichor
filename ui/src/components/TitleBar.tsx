import type { ComponentChildren } from 'preact'

// TitleBar is the top of a page, laid out like the Overview header: the title with a muted line
// under it, optional badges below, and the page's actions on the right.
export function TitleBar({
  title,
  description,
  badges,
  actions,
}: {
  title: string
  description?: string
  badges?: ComponentChildren
  actions?: ComponentChildren
}) {
  return (
    <header class="mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
      <div class="min-w-0">
        <h1 class="break-words text-xl font-semibold tracking-tight">{title}</h1>
        {description && <p class="break-words text-sm text-muted-foreground">{description}</p>}
        {badges && <div class="mt-2 flex flex-wrap items-center gap-2">{badges}</div>}
      </div>
      {actions && <div class="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}
