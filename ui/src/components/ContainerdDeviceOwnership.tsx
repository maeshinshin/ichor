import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Switch } from '@/components/ui/switch'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { useAction } from '@/hooks/use-action'
import { clusterClient } from '@/lib/connect'
import { deviceOwnershipCanTurnOn } from '@/lib/containerd'
import { notifyDone } from '@/lib/notify'
import { t } from '@/lib/i18n'

// ContainerdDeviceOwnership is spec.containerd.deviceOwnershipFromSecurityContext. The UI only writes
// the field, as the caller; the controller renders it into the nodes. canEdit is the caller's update
// verb on the cluster.
export function ContainerdDeviceOwnership({
  cluster,
  contract,
  canEdit,
  onChanged,
}: {
  cluster: Cluster
  contract: string
  canEdit: boolean
  onChanged: (next: Cluster) => void
}) {
  const { busy, error, run } = useAction()
  const enabled = cluster.containerdDeviceOwnership
  const blocked = !deviceOwnershipCanTurnOn(contract, enabled)

  function set(on: boolean) {
    const ref = cluster.ref
    if (!ref) return
    return run(async () => {
      const res = await clusterClient.setContainerdDeviceOwnership({ cluster: ref, enabled: on })
      if (res.cluster) onChanged(res.cluster)
      notifyDone(t('containerd.devOwn.done'))
    })
  }

  return (
    <div className="space-y-2">
      <Field orientation="horizontal">
        <Switch id="containerd-device-ownership" checked={enabled} disabled={!canEdit || busy || blocked} onCheckedChange={set} />
        <FieldLabel htmlFor="containerd-device-ownership">{t('containerd.devOwn.label')}</FieldLabel>
      </Field>
      <FieldDescription>{t('containerd.devOwn.help')}</FieldDescription>
      {blocked && <FieldDescription>{t('containerd.devOwn.needsContract')}</FieldDescription>}
      {error && <ErrorAlert {...error} />}
    </div>
  )
}
