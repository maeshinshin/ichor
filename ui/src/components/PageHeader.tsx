import { display } from '@/lib/format'
import type { ComponentChildren } from 'preact'

// Fields lays out label/value pairs as a compact description list.
export function Fields({ children }: { children: ComponentChildren }) {
  return <dl class="grid grid-cols-2 gap-x-6 gap-y-4 text-sm sm:grid-cols-3 lg:grid-cols-4">{children}</dl>
}

export function Field({ label, value, children }: { label: string; value?: string; children?: ComponentChildren }) {
  return (
    <div class="min-w-0">
      <dt class="text-xs text-muted-foreground">{label}</dt>
      <dd class="break-words">{children ?? display(value)}</dd>
    </div>
  )
}
