import type { ComponentChildren } from 'preact'
import { CircleCheckIcon, CircleIcon, CircleXIcon, TriangleAlertIcon } from 'lucide-preact'
import { Spinner } from '@/components/ui/spinner'
import type { Tone } from '@/lib/status'

const toneClass: Record<Tone, string> = {
  good: 'text-emerald-600 dark:text-emerald-400',
  bad: 'text-red-600 dark:text-red-400',
  warn: 'text-amber-600 dark:text-amber-400',
  progress: 'text-sky-600 dark:text-sky-400',
  neutral: 'text-muted-foreground',
}

// StatusText is how a state is shown everywhere, as in Argo CD and the cloud
// consoles: a coloured icon and the word, never a filled pill. The icon is
// decorative; the word carries the meaning, so colour is never the only cue.
export function StatusText({ tone, children }: { tone: Tone; children: ComponentChildren }) {
  return (
    <span class={`inline-flex items-center gap-1.5 whitespace-nowrap text-sm ${toneClass[tone]}`}>
      {tone === 'good' && <CircleCheckIcon className="size-4" aria-hidden="true" />}
      {tone === 'bad' && <CircleXIcon className="size-4" aria-hidden="true" />}
      {tone === 'warn' && <TriangleAlertIcon className="size-4" aria-hidden="true" />}
      {tone === 'progress' && <Spinner className="size-4" role="presentation" aria-label="" aria-hidden="true" />}
      {tone === 'neutral' && <CircleIcon className="size-4" aria-hidden="true" />}
      <span>{children}</span>
    </span>
  )
}
