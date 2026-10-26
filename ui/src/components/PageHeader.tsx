import type { ComponentChildren } from 'preact'

// PageHeader is the top of every page: the resource name, its kind, the badges
// that say where it stands, and the primary actions. Badges and actions wrap
// rather than push the page wider on a phone.
export function PageHeader({
  title,
  kind,
  badges,
  actions,
}: {
  title: string
  kind?: string
  badges?: ComponentChildren
  actions?: ComponentChildren
}) {
  return (
    <div class="mb-6 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
      <div class="min-w-0 space-y-1">
        {kind && <p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">{kind}</p>}
        <h1 class="break-words text-2xl font-semibold tracking-tight">{title}</h1>
        {badges && <div class="flex flex-wrap items-center gap-2 pt-1">{badges}</div>}
      </div>
      {actions && <div class="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}

// Fields lays out label/value pairs as a compact description list.
export function Fields({ children }: { children: ComponentChildren }) {
  return <dl class="grid grid-cols-2 gap-x-6 gap-y-4 text-sm sm:grid-cols-3 lg:grid-cols-4">{children}</dl>
}

export function Field({ label, value, children }: { label: string; value?: string; children?: ComponentChildren }) {
  return (
    <div class="min-w-0">
      <dt class="text-xs text-muted-foreground">{label}</dt>
      <dd class="break-words">{children ?? (value || '-')}</dd>
    </div>
  )
}
