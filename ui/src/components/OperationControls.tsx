import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
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
  showDelete = true,
}: {
  op: Operation
  onChanged: (next: Operation) => void
  onDeleted: () => void
  // showDelete is false where only pause and resume belong (the cluster page).
  showDelete?: boolean
}) {
  const t = useT()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [open, setOpen] = useState(false)
  // A restore that has started is half done; the server refuses to pause it, so the button is not offered.
  const action = op.kind === OperationKind.ETCD_RESTORE ? null : pauseAction(op.paused, operationState(op.phase, op.failureReason))

  async function setPaused(paused: boolean) {
    if (!op.ref) return
    setBusy(true)
    setError(null)
    try {
      const res = await operationClient.setOperationPaused({ operation: op.ref, paused })
      if (res.operation) onChanged(res.operation)
      notifyDone(t(paused ? 'toast.opPaused' : 'toast.opResumed'))
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
      notifyDone(t('toast.opDeleted'))
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
        {showDelete && (
        <ConfirmDialog
          open={open}
          onOpenChange={setOpen}
          trigger={
            <Button size="sm" variant="destructive">
              {t('ops.delete')}
            </Button>
          }
          title={t('ops.deleteTitle', { name: op.ref?.name ?? '' })}
          description={t('ops.deleteDescription')}
          confirm={{ label: busy ? t('ops.deleting') : t('ops.delete'), disabled: busy, onClick: remove }}
        >
          {error && <ErrorAlert {...error} />}
        </ConfirmDialog>
        )}
      </div>
      {error && !open && <ErrorAlert {...error} />}
    </div>
  )
}
