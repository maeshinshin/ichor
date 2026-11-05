import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { useAction } from '@/hooks/use-action'
import { cniClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import { t } from '@/lib/i18n'

// CiliumUpgradeDialog starts the Cilium upgrade to the chart this Ichor embeds. There is nothing to pick: the
// title names the version, because Cilium moves only to the embedded chart.
// The Cilium page and the Versions card share it. The server can still refuse; the error text is shown as received.
export function CiliumUpgradeDialog({ cluster, chart, size }: { cluster: ObjectRef; chart: string; size?: 'sm' }) {
  const [open, setOpen] = useState(false)
  const { busy, error, setError, run } = useAction()

  const start = () =>
    run(async () => {
      await cniClient.startCniUpgrade({ cluster })
      setOpen(false)
      notifyDone(t('cilium.upgradeStarted'))
    })

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
