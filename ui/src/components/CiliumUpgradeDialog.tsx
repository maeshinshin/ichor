import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { VersionPicker } from '@/components/VersionPicker'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { cniClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import type { TargetList } from '@/lib/upgradeTargets'
import { useT } from '@/lib/useT'

// CiliumUpgradeDialog starts a Cilium upgrade: to the embedded chart when the picker is left empty, or to the
// verified patch it names. The Cilium page and the Versions card share it.
// The server can still refuse (or answer Unimplemented); the error text is shown as received.
export function CiliumUpgradeDialog({
  cluster,
  chart,
  list,
  size,
  disabled,
}: {
  cluster: ObjectRef
  // chart is the embedded chart version when the cluster runs an older one, else ''.
  chart: string
  list: TargetList | null
  size?: 'sm'
  disabled?: boolean
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [target, setTarget] = useState('')

  async function start() {
    setBusy(true)
    setError(null)
    try {
      await cniClient.startCniUpgrade({ cluster, version: target })
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
        <Button variant="outline" size={size} disabled={disabled}>
          {t('cilium.upgradeStart')}
        </Button>
      }
      title={t('cilium.upgradeConfirmTitle', { version: target || chart || t('cilium.upgradePickTarget') })}
      description={`${chart !== '' ? t('cilium.upgradeAvailable', { version: chart }) : t('cilium.patchesAvailable')} ${t('cilium.upgradeConfirmBody')}`}
      confirm={{
        label: busy ? t('cilium.upgradeStarting') : t('cilium.upgrade'),
        disabled: busy || (target === '' && chart === ''),
        onClick: start,
        destructive: false,
      }}
    >
      <VersionPicker
        id="cilium-version"
        value={target}
        onValueChange={(v) => {
          setTarget(v)
          setError(null)
        }}
        list={list}
      />
      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
