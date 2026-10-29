import type { ComponentChildren } from 'preact'
import { CircleAlertIcon, CircleCheckIcon, LoaderCircleIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import type { Tone } from '@/lib/status'

// A state is a Badge: destructive for bad, secondary for good and running, outline otherwise.
const toneVariant: Record<Tone, 'secondary' | 'destructive' | 'outline'> = {
  good: 'secondary',
  progress: 'secondary',
  bad: 'destructive',
  warn: 'outline',
  neutral: 'outline',
}

// StateBadge shows a state as a stock Badge with a leading icon through the documented slot;
// a neutral state has no icon.
export function StateBadge({ tone, children }: { tone: Tone; children: ComponentChildren }) {
  return (
    <Badge variant={toneVariant[tone]}>
      {tone === 'good' && <CircleCheckIcon data-icon="inline-start" aria-hidden="true" />}
      {tone === 'progress' && <LoaderCircleIcon data-icon="inline-start" aria-hidden="true" />}
      {(tone === 'bad' || tone === 'warn') && <CircleAlertIcon data-icon="inline-start" aria-hidden="true" />}
      {children}
    </Badge>
  )
}
