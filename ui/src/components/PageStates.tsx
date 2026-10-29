import type { ComponentChildren } from 'preact'
import type { LucideIcon } from 'lucide-preact'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'

// PageSkeleton stands in for a page while it loads. The label stays in the
// DOM for screen readers, so the loading text is still announced.
export function PageSkeleton({ label }: { label: string }) {
  return (
    <div role="status" aria-live="polite" class="space-y-6">
      <span class="sr-only">{label}</span>
      <div class="space-y-2">
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-8 w-64 max-w-full" />
      </div>
      <Skeleton className="h-32 w-full" />
      <Skeleton className="h-48 w-full" />
    </div>
  )
}

// EmptyNote is the "nothing here" state of a list or section.
export function EmptyNote({ children, icon: Icon }: { children: ComponentChildren; icon?: LucideIcon }) {
  return (
    <Empty className="border p-4">
      <EmptyHeader>
        {Icon && (
          <EmptyMedia variant="icon">
            <Icon aria-hidden="true" />
          </EmptyMedia>
        )}
        <EmptyDescription>{children}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}
