import { Check, Copy } from 'lucide-preact'
import { render } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { CodeBlockCopyButton } from '@/components/ui/code-block'

function CopyButton({ pre }: { pre: HTMLElement }) {
  const [done, setDone] = useState(false)
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      className="absolute top-2 right-2"
      aria-label="Copy code"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(pre.querySelector('code')?.textContent ?? pre.textContent ?? '')
          setDone(true)
          setTimeout(() => setDone(false), 1500)
        } catch {
          // Clipboard blocked (insecure origin): nothing to copy to.
        }
      }}
    >
      {done ? <Check /> : <Copy />}
    </Button>
  )
}

/** Adds a copy button to every code block of the article; authors write plain fenced code. */
export function CodeCopy() {
  useEffect(() => {
    for (const pre of document.querySelectorAll<HTMLElement>('article pre')) {
      // A fence with options is a preactcn CodeBlock, with its own copy button below.
      if (pre.dataset.copy || pre.closest('[data-slot=code-block]')) continue
      pre.dataset.copy = 'true'
      const wrap = document.createElement('div')
      wrap.className = 'relative'
      pre.replaceWith(wrap)
      wrap.append(pre)
      const mount = document.createElement('div')
      wrap.append(mount)
      render(<CopyButton pre={pre} />, mount)
    }
    for (const actions of document.querySelectorAll<HTMLElement>('article [data-slot=code-block-actions]')) {
      if (actions.dataset.copy) continue
      actions.dataset.copy = 'true'
      const lines = actions.closest('[data-slot=code-block]')?.querySelectorAll('[data-slot=code-block-line]') ?? []
      render(<CodeBlockCopyButton value={Array.from(lines, (line) => line.textContent).join('\n')} />, actions)
    }
  }, [])
  return null
}
