import type { ComponentChildren } from 'preact'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

// SectionCard is one stable-id block of a detail page as a stock Card: the title, the optional
// description and action in the header, the body in the content. The id is the anchor that #links
// point at; scroll-mt-section keeps a jumped-to card clear of the sticky top bar.
export function SectionCard({
  id,
  size,
  title,
  description,
  actions,
  children,
}: {
  id?: string
  size?: 'default' | 'sm'
  title: ComponentChildren
  description?: ComponentChildren
  actions?: ComponentChildren
  children: ComponentChildren
}) {
  return (
    <Card id={id} size={size} className="scroll-mt-section">
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description && <CardDescription>{description}</CardDescription>}
        {actions && <CardAction>{actions}</CardAction>}
      </CardHeader>
      <CardContent className="space-y-4">{children}</CardContent>
    </Card>
  )
}

// SectionSkeleton stands in for a section while it loads; the label stays for screen readers.
export function SectionSkeleton({ label }: { label: string }) {
  return (
    <div role="status" aria-live="polite">
      <span class="sr-only">{label}</span>
      <Skeleton className="h-32 w-full" />
    </div>
  )
}
