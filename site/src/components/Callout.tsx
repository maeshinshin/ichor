import { CircleAlert, Info, Lightbulb, OctagonAlert, TriangleAlert } from 'lucide-preact'
import type { ComponentChildren } from 'preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'

// GitHub's alert kinds on the preactcn Alert; its destructive variant is for the two that warn of harm.
const kinds = {
  note: { title: 'Note', Icon: Info, destructive: false },
  tip: { title: 'Tip', Icon: Lightbulb, destructive: false },
  important: { title: 'Important', Icon: CircleAlert, destructive: false },
  warning: { title: 'Warning', Icon: TriangleAlert, destructive: true },
  caution: { title: 'Caution', Icon: OctagonAlert, destructive: true },
} as const

export type CalloutKind = keyof typeof kinds

export function Callout({ kind, children }: { kind: CalloutKind; children?: ComponentChildren }) {
  const { title, Icon, destructive } = kinds[kind]
  return (
    <Alert variant={destructive ? 'destructive' : 'default'}>
      <Icon />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>{children}</AlertDescription>
    </Alert>
  )
}
