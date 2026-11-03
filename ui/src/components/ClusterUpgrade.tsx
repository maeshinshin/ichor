import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { PlanClusterUpgradeResponse, ClusterUpgradeStep } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyDone } from '@/lib/notify'
import { useT } from '@/lib/useT'
import { stepLabel, upgradeLabel } from '@/lib/versions'

// ClusterUpgrade is the one button that brings the cluster to the release set of this Ichor.
// The rollout runs one step at a time: the confirmation lists every step, and starting creates only the
// first one, so each following step needs another click. The button is hidden when the cluster already
// runs the set, and disabled while an operation runs.
export function ClusterUpgrade({
  cluster,
  version,
  busy,
  onStarted,
}: {
  cluster: { namespace: string; name: string }
  version: string | null
  busy: boolean
  onStarted: () => void
}) {
  const t = useT()
  const [steps, setSteps] = useState<ClusterUpgradeStep[] | null>(null)
  const [open, setOpen] = useState(false)
  const [plan, setPlan] = useState<PlanClusterUpgradeResponse | null>(null)
  const [starting, setStarting] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  // Whether anything is left to do is read again whenever an operation starts or ends.
  useEffect(() => {
    operationClient.planClusterUpgrade({ cluster }).then(
      (p) => setSteps(p.steps),
      () => setSteps(null),
    )
  }, [cluster.namespace, cluster.name, busy])

  // Opening the dialog reads the plan again, so the steps shown are the ones the server would start now.
  async function openDialog(next: boolean) {
    setOpen(next)
    if (!next) return
    setPlan(null)
    setError(null)
    try {
      setPlan(await operationClient.planClusterUpgrade({ cluster }))
    } catch (err) {
      setError(describeError(err))
    }
  }

  async function start() {
    setStarting(true)
    setError(null)
    try {
      await operationClient.startClusterUpgrade({ cluster })
      setOpen(false)
      notifyDone(t('versions.upgradeStarted'))
      onStarted()
    } catch (err) {
      setError(describeError(err))
    } finally {
      setStarting(false)
    }
  }

  if (steps?.length === 0) return null

  const canStart = plan !== null && plan.blocked === '' && plan.steps.length > 0 && !starting && !busy
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(o) => void openDialog(o)}
      trigger={
        <Button className="h-auto whitespace-normal" disabled={busy}>
          {upgradeLabel(version)}
        </Button>
      }
      title={upgradeLabel(version)}
      description={t('versions.upgradeIntro')}
      confirm={{ label: t('versions.upgradeStart'), onClick: start, disabled: !canStart, destructive: false }}
    >
      {plan === null && error === null && <p>{t('versions.checking')}</p>}
      {plan?.blocked ? (
        <Alert variant="destructive">
          <AlertTitle>{t('ops.plan.blocked')}</AlertTitle>
          <AlertDescription className="break-words">{plan.blocked}</AlertDescription>
        </Alert>
      ) : plan && plan.steps.length === 0 ? (
        <p>{t('versions.state.up-to-date')}</p>
      ) : (
        plan && (
          <ol className="list-decimal space-y-2 pl-5 break-words">
            {plan.steps.map((s) => (
              <li key={s.step}>{stepLabel(s, plan.releaseSet)}</li>
            ))}
          </ol>
        )
      )}
      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
