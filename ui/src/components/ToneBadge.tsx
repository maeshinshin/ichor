import type { ComponentChildren } from 'preact'
import { CircleAlertIcon, CircleCheckIcon, LoaderCircleIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import type { Tone } from '@/lib/status'

// ToneName also takes the talos views' 'ok' for 'good'.
export type ToneName = Tone | 'ok'

// A state is a Badge: destructive for bad, secondary for good and running, outline otherwise.
const variantOf = (tone: ToneName): 'secondary' | 'destructive' | 'outline' =>
  tone === 'good' || tone === 'ok' || tone === 'progress' ? 'secondary' : tone === 'bad' ? 'destructive' : 'outline'

// ToneBadge is a state as a stock Badge with a leading icon; a neutral state has none.
export function ToneBadge({ tone, title, children }: { tone: ToneName; title?: string; children: ComponentChildren }) {
  return (
    <Badge variant={variantOf(tone)} title={title}>
      {(tone === 'good' || tone === 'ok') && <CircleCheckIcon data-icon="inline-start" aria-hidden="true" />}
      {tone === 'progress' && <LoaderCircleIcon data-icon="inline-start" aria-hidden="true" />}
      {(tone === 'bad' || tone === 'warn') && <CircleAlertIcon data-icon="inline-start" aria-hidden="true" />}
      {children}
    </Badge>
  )
}
