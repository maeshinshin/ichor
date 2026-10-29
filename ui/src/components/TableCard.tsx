import type { ComponentChildren } from 'preact'
import { Card, CardContent } from '@/components/ui/card'

// TableCard frames a list table as a stock Card.
export function TableCard({ children }: { children: ComponentChildren }) {
  return (
    <Card>
      <CardContent>{children}</CardContent>
    </Card>
  )
}
