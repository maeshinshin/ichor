import { toast } from '@/lib/shims/sonner'
import { outcomeMessage } from '@/lib/outcome'
import { t, type Key } from '@/lib/i18n'

// The one way the UI tells a person that a change worked (preactcn sonner).
// Errors stay inline where the person is looking; notifyError is for the few
// places that have no room for one.
export function notifyDone(message: string): void {
  toast.success(message)
}

export function notifyError(title: string, detail?: string): void {
  toast.error(title, detail ? { description: detail } : undefined)
}

// notifyWrite is for a write's response: "done" for a direct write, a pull
// request note when the change was proposed instead.
export function notifyWrite(res: { pullRequestUrl?: string }, done: Key, params?: Record<string, string | number>): void {
  toast.success(outcomeMessage(res, t(done, params), t('toast.pullRequest')))
}
