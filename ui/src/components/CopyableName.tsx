import { useEffect, useState } from 'preact/hooks'
import { CheckIcon, CopyIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'

// CopyableName shows a name a typed confirmation asks for, as text the user can select (one tap
// selects all of it on a phone) with a copy button. It stays out of the field label: the label is
// select-none, and tapping a label focuses its input instead.
function CopyableName({ value, ariaLabel }: { value: string; ariaLabel: string }) {
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) {
      return
    }
    const timeout = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(timeout)
  }, [copied])

  return (
    <div class="flex items-center gap-2">
      <code class="min-w-0 flex-1 font-mono text-sm break-all select-all">{value}</code>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        aria-label={ariaLabel}
        onClick={() => {
          navigator.clipboard?.writeText(value).then(() => setCopied(true), () => {})
        }}
      >
        {copied ? <CheckIcon /> : <CopyIcon />}
      </Button>
    </div>
  )
}

export { CopyableName }
