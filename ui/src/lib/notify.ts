import { toast } from '@/lib/shims/sonner'
import { t, type Key } from '@/lib/i18n'

// The one way the UI tells a person that a change worked (preactcn sonner).
// Errors stay inline where the person is looking.
export function notifyDone(message: string): void {
  toast.success(message)
}

// notifyFailed is for a failure the person cannot see inline, such as a file download that ended
// after the dialog closed. The detail is the server's own message.
export function notifyFailed(title: string, detail?: string): void {
  toast.error(title, { description: detail, duration: 15000 })
}

// notifyWarning is for a write that went through with a remark worth a look, such as a pick the
// node did not report. The message is the server's own text.
export function notifyWarning(message: string): void {
  toast.warning(message)
}

// notifyWrite is for a write that went through: its message, from the catalogue.
export function notifyWrite(done: Key, params?: Record<string, string | number>): void {
  toast.success(t(done, params))
}
