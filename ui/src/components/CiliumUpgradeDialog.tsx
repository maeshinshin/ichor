import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { cniClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import { t } from '@/lib/i18n'

// CiliumUpgradeDialog starts the Cilium upgrade to the chart this Ichor embeds. There is nothing to pick: the
// title names the version, because Cilium moves only to the embedded chart (docs/adr/0115, amendment 2026-11-04 (3)).
// The Cilium page and the Versions card share it. The server can still refuse; the error text is shown as received.
export function CiliumUpgradeDialog({ cluster, chart, size }: { cluster: ObjectRef; chart: string; size?: 'sm' }) {
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  async function start() {
    setBusy(true)
    setError(null)
    try {
      await cniClient.startCniUpgrade({ cluster })
      setOpen(false)
      notifyDone(t('cilium.upgradeStarted'))
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
        <Button variant="outline" size={size}>
          {t('cilium.upgradeStart')}
        </Button>
      }
      title={t('cilium.upgradeConfirmTitle', { version: chart })}
      description={`${t('cilium.upgradeAvailable', { version: chart })} ${t('cilium.upgradeConfirmBody')}`}
      confirm={{
        label: busy ? t('cilium.upgradeStarting') : t('cilium.upgrade'),
        disabled: busy,
        onClick: start,
        destructive: false,
      }}
    >
      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
