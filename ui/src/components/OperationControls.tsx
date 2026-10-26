import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { operationState, pauseAction } from '@/lib/operations'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }

// OperationControls: pause or resume, and delete. Deleting asks first; pausing
// changes nothing already done and can be undone, so it does not.
export function OperationControls({
  op,
  onChanged,
  onDeleted,
}: {
  op: Operation
  onChanged: (next: Operation) => void
  onDeleted: () => void
}) {
  const t = useT()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [open, setOpen] = useState(false)
  const action = pauseAction(op.paused, operationState(op.phase, op.failureReason))

  async function setPaused(paused: boolean) {
    if (!op.ref) return
    setBusy(true)
    setError(null)
    try {
      const res = await operationClient.setOperationPaused({ operation: op.ref, paused })
      if (res.operation) onChanged(res.operation)
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!op.ref) return
    setBusy(true)
    setError(null)
    try {
      await operationClient.deleteOperation({ operation: op.ref })
      setOpen(false)
      onDeleted()
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div class="flex flex-col gap-2">
      <div class="flex gap-2">
        {action && (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => setPaused(action === 'pause')}>
            {busy ? t('ops.pausing') : t(action === 'pause' ? 'ops.pause' : 'ops.resume')}
          </Button>
        )}
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button size="sm" variant="destructive">
              {t('ops.delete')}
            </Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t('ops.deleteTitle', { name: op.ref?.name ?? '' })}</DialogTitle>
              <DialogDescription>{t('ops.deleteDescription')}</DialogDescription>
            </DialogHeader>
            {error && <ErrorAlert {...error} />}
            <DialogFooter>
              <Button variant="ghost" onClick={() => setOpen(false)}>
                {t('common.cancel')}
              </Button>
              <Button variant="destructive" disabled={busy} onClick={remove}>
                {busy ? t('ops.deleting') : t('ops.delete')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      </div>
      {error && !open && <ErrorAlert {...error} />}
    </div>
  )
}
