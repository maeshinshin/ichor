import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { EtcdBackup } from '@/gen/ichor/v1alpha1/operation_pb'
import { backNowBlock } from '@/lib/backups'
import { operationClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import { t } from '@/lib/i18n'

// BackupNow asks for one snapshot outside the schedule (docs/adr/0047). The UI only sets the request;
// the controller takes the snapshot. onChanged gets the backup as it now stands (request pending).
export function BackupNow({ backup, onChanged }: { backup: EtcdBackup; onChanged: (next: EtcdBackup) => void }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const block = backNowBlock(backup)
  const name = backup.ref?.name ?? ''

  async function request() {
    if (!backup.ref) return
    setBusy(true)
    setError(null)
    try {
      const res = await operationClient.requestEtcdBackup({ backup: backup.ref })
      if (res.backup) onChanged(res.backup)
      setOpen(false)
      notifyDone(t('toast.backupRequested'))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) setError(null)
      }}
      trigger={
        <Button size="sm" variant="outline" disabled={!!block} title={block ? t(block) : undefined}>
          {t('backups.now')}
        </Button>
      }
      title={t('backups.nowTitle', { name })}
      description={t('backups.nowDescription')}
      confirm={{ label: busy ? t('backups.nowBusy') : t('backups.now'), disabled: busy, onClick: request, destructive: false }}
    >
      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
