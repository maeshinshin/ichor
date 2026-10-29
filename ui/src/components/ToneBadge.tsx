import type { ComponentChildren } from 'preact'
import { CircleAlertIcon, CircleCheckIcon, LoaderCircleIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import type { Tone } from '@/lib/status'

// A state is a Badge: success (green) for good, destructive for bad, secondary for running, outline otherwise.
const variantOf = (tone: Tone): 'success' | 'secondary' | 'destructive' | 'outline' =>
  tone === 'good' ? 'success' : tone === 'progress' ? 'secondary' : tone === 'bad' ? 'destructive' : 'outline'

// ToneBadge is a state as a stock Badge with a leading icon; a neutral state has none.
export function ToneBadge({ tone, title, children }: { tone: Tone; title?: string; children: ComponentChildren }) {
  return (
    <Badge variant={variantOf(tone)} title={title}>
      {tone === 'good' && <CircleCheckIcon data-icon="inline-start" aria-hidden="true" />}
      {tone === 'progress' && <LoaderCircleIcon data-icon="inline-start" aria-hidden="true" />}
      {(tone === 'bad' || tone === 'warn') && <CircleAlertIcon data-icon="inline-start" aria-hidden="true" />}
      {children}
    </Badge>
  )
}
