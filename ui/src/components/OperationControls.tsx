import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { useAction } from '@/hooks/use-action'
import { operationClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import { operationState, pauseAction } from '@/lib/operations'
import { t } from '@/lib/i18n'

// OperationControls: pause or resume (resuming a failed operation runs the failed step again), and delete. Deleting asks first; pausing
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
  const { busy, error, run } = useAction()
  const [open, setOpen] = useState(false)
  const [resumeOpen, setResumeOpen] = useState(false)
  const state = operationState(op.phase, op.failureReason)
  // A restore that has started is half done; the server refuses to pause it, so only a failed one gets a button (Resume).
  const action = op.kind === OperationKind.ETCD_RESTORE && state !== 'failed' ? null : pauseAction(op.paused, state)

  function setPaused(paused: boolean) {
    const operation = op.ref
    if (!operation) return
    return run(async () => {
      const res = await operationClient.setOperationPaused({ operation, paused })
      if (res.operation) onChanged(res.operation)
      setResumeOpen(false)
      notifyDone(t(paused ? 'toast.opPaused' : 'toast.opResumed'))
    })
  }

  function remove() {
    const operation = op.ref
    if (!operation) return
    return run(async () => {
      await operationClient.deleteOperation({ operation })
      setOpen(false)
      notifyDone(t('toast.opDeleted'))
      onDeleted()
    })
  }

  return (
    <div class="flex flex-col gap-2">
      <div class="flex gap-2">
        {action && state === 'failed' && (
          <ConfirmDialog
            open={resumeOpen}
            onOpenChange={setResumeOpen}
            trigger={
              <Button size="sm" variant="outline" disabled={busy}>
                {t('ops.resume')}
              </Button>
            }
            title={t('ops.resumeTitle', { name: op.ref?.name ?? '' })}
            description={t('ops.resumeFailedDescription')}
            confirm={{ label: busy ? t('ops.pausing') : t('ops.resume'), disabled: busy, onClick: () => setPaused(false), destructive: false }}
          >
            {error && <ErrorAlert {...error} />}
          </ConfirmDialog>
        )}
        {action && state !== 'failed' && (
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
