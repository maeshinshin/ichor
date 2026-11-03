import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { VersionPicker } from '@/components/VersionPicker'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { cniClient } from '@/lib/connect'
import { describeError, isOverridable, type ErrorShape } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import type { TargetList } from '@/lib/upgradeTargets'
import { useT } from '@/lib/useT'

// CiliumUpgradeDialog starts a Cilium upgrade: to the embedded chart when the picker is left empty, or to the
// verified patch it names (docs/adr/0115, amendment 2026-11-03). The Cilium page and the Versions card share it.
// The server can still refuse (or answer Unimplemented, ADR 0099 unit 7); the error text is shown as received.
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

  // The override (ADR 0110) is offered only after the server refused a patch for compatibility and marked the
  // refusal overridable; the reason is optional.
  const [overridable, setOverridable] = useState(false)
  const [overridden, setOverridden] = useState(false)
  const [reason, setReason] = useState('')

  async function start() {
    setBusy(true)
    setError(null)
    try {
      await cniClient.startCniUpgrade({ cluster, version: target, override: overridden ? { reason: reason.trim() } : undefined })
      setOpen(false)
      notifyDone(t('cilium.upgradeStarted'))
    } catch (err) {
      setError(describeError(err))
      setOverridable(overridden || isOverridable(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (!next) {
          setError(null)
          setOverridable(false)
          setOverridden(false)
          setReason('')
        }
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
        disabled: busy || (target === '' && chart === '') || (overridable && !overridden),
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
          setOverridable(false)
          setOverridden(false)
        }}
        list={list}
      />
      {error && <ErrorAlert {...error} />}
      {overridable && (
        <div class="space-y-3">
          <div class="flex items-center gap-2 text-sm">
            <Checkbox id="cilium-override" checked={overridden} onCheckedChange={(on: boolean | 'indeterminate') => setOverridden(on === true)} />
            <Label htmlFor="cilium-override">{t('ops.dialog.override')}</Label>
          </div>
          <div class="space-y-2">
            <Label htmlFor="cilium-override-reason">{t('ops.dialog.reason')}</Label>
            <Textarea
              id="cilium-override-reason"
              value={reason}
              onInput={(e) => setReason((e.currentTarget as HTMLTextAreaElement).value)}
            />
            <p class="text-xs text-muted-foreground">{t('ops.dialog.overrideHint')}</p>
          </div>
        </div>
      )}
    </ConfirmDialog>
  )
}
