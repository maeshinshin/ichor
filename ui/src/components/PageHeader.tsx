import type { ComponentChildren } from 'preact'

// PageHeader is the top of every page: the resource name, its kind, the status
// that says where it stands, the primary actions on the right, and below them
// the key facts as compact label/value pairs, so the most important
// information is visible without scrolling. Everything wraps rather than
// pushing the page wider on a phone (docs/adr/0074).
export type MetaItem = { label: string; value?: ComponentChildren }

export function PageHeader({
  title,
  kind,
  badges,
  actions,
  meta,
}: {
  title: string
  kind?: string
  badges?: ComponentChildren
  actions?: ComponentChildren
  meta?: MetaItem[]
}) {
  return (
    <header class="mb-6 border-b pb-4">
      <div class="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="min-w-0 space-y-1">
          {kind && <p class="text-xs font-medium uppercase tracking-wide text-muted-foreground">{kind}</p>}
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h1 class="break-words text-xl font-semibold tracking-tight">{title}</h1>
            {badges && <div class="flex flex-wrap items-center gap-x-3 gap-y-1">{badges}</div>}
          </div>
        </div>
        {actions && <div class="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {meta && meta.length > 0 && (
        <dl class="mt-3 flex flex-wrap gap-x-8 gap-y-2 text-sm">
          {meta.map((m) => (
            <div key={m.label} class="min-w-0">
              <dt class="text-xs text-muted-foreground">{m.label}</dt>
              <dd class="break-words">{m.value || '-'}</dd>
            </div>
          ))}
        </dl>
      )}
    </header>
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
