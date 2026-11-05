import type { ComponentChildren } from 'preact'
import type { LucideIcon } from 'lucide-preact'
import { CircleXIcon, InboxIcon } from 'lucide-preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { t, type Key } from '@/lib/i18n'

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

// Failed shows a section's own error instead of its body.
export function Failed({ error, children }: { error: string; children: ComponentChildren }) {
  if (!error) return <>{children}</>
  return (
    <Alert variant="destructive">
      <CircleXIcon />
      <AlertTitle>{t('talos.sectionFailed')}</AlertTitle>
      <AlertDescription className="break-words">{error}</AlertDescription>
    </Alert>
  )
}

// Grid is a table with a header row, or the empty note when there is nothing to list.
export function Grid({ heads, empty, none = 'talos.none', children }: { heads: string[]; empty?: boolean; none?: Key; children: ComponentChildren }) {
  if (empty) return <EmptyNote icon={InboxIcon}>{t(none)}</EmptyNote>
  return (
    <Table>
      <TableHeader>
        <TableRow>
          {heads.map((h, i) => (
            <TableHead key={i}>{h}</TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>{children}</TableBody>
    </Table>
  )
}
