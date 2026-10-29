import type { ComponentChildren } from 'preact'

// PageTitle is the top of a page, laid out as the Overview's: title and a line under it on the
// left, the page's actions on the right.
export function PageTitle({
  title,
  description,
  badges,
  actions,
}: {
  title: string
  description?: ComponentChildren
  badges?: ComponentChildren
  actions?: ComponentChildren
}) {
  return (
    <header class="mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <h1 class="break-words text-xl font-semibold tracking-tight">{title}</h1>
          {badges}
        </div>
        {description && <p class="break-words text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div class="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}
