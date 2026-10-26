import { useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert } from '@/components/ErrorAlert'
import { wireKindOf } from '@/components/opkind'
import { OperationEffect, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { confirmMatches, planCounts, planSteps, targetError, type OpKind } from '@/lib/operations'
import { useT } from '@/lib/useT'
import { navigate, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

const startedByDefault: Record<OpKind, boolean> = { talos: true, kubernetes: true, ichor: true, cni: true, shutdown: false }

// StartOperationDialog is the only way the UI starts an upgrade or a shutdown.
// It never writes on the first click: "Review" asks the server for the plan
// (which machines, in which order, what happens to each) and shows it, and
// only the second, separate "Start" writes. A shutdown also needs the cluster's
// name typed. What the server refuses (another operation running, a spec that
// Argo CD tracks, no permission) is shown as the server says it.
export function StartOperationDialog({
  kind,
  cluster,
}: {
  kind: OpKind
  cluster: { namespace: string; name: string }
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [target, setTarget] = useState(kind === 'cni' ? 'replaced' : '')
  const [typed, setTyped] = useState('')
  const [plan, setPlan] = useState<PlanOperationResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  const needsTarget = kind !== 'shutdown'
  const targetProblem = needsTarget ? targetError(kind, target) : null
  const startable =
    plan !== null && plan.blocked === '' && (kind !== 'shutdown' || confirmMatches(typed, cluster.name)) && !busy

  function reset(next: boolean) {
    setOpen(next)
    if (!next) {
      setPlan(null)
      setError(null)
      setTyped('')
    }
  }

  async function review() {
    setBusy(true)
    setError(null)
    try {
      setPlan(await operationClient.planOperation({ cluster, kind: wireKindOf(kind), target: needsTarget ? target.trim() : '' }))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const res = await operationClient.createOperation({
        cluster,
        kind: wireKindOf(kind),
        target: needsTarget ? target.trim() : '',
      })
      reset(false)
      if (res.operation?.ref) navigate(objectRefPath('/operations', res.operation.ref))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={reset}>
      <DialogTrigger asChild>
        <Button size="sm" variant={kind === 'shutdown' ? 'destructive' : 'outline'}>
          {t(`ops.start.${kind}`)}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('ops.dialog.title', { action: t(`ops.start.${kind}`), cluster: cluster.name })}</DialogTitle>
          <DialogDescription>{plan ? '' : t('cluster.operationsHint')}</DialogDescription>
        </DialogHeader>

        {!plan && needsTarget && (
          <div class="space-y-2">
            <Label htmlFor={`op-target-${kind}`}>{t(kind === 'cni' ? 'ops.dialog.targetMode' : 'ops.dialog.targetVersion')}</Label>
            {kind === 'cni' ? (
              <select
                id={`op-target-${kind}`}
                class="h-9 w-full rounded-md border bg-background px-2 text-sm"
                value={target}
                onChange={(e) => setTarget((e.currentTarget as HTMLSelectElement).value)}
              >
                <option value="replaced">{t('ops.dialog.modeReplaced')}</option>
                <option value="enabled">{t('ops.dialog.modeEnabled')}</option>
              </select>
            ) : (
              <Input
                id={`op-target-${kind}`}
                value={target}
                onInput={(e) => setTarget((e.currentTarget as HTMLInputElement).value)}
              />
            )}
            {target !== '' && targetProblem && (
              <p class="text-xs text-destructive">
                {t(targetProblem === 'required' ? 'ops.dialog.errRequired' : 'ops.dialog.errInvalid')}
              </p>
            )}
          </div>
        )}

        {plan && <PlanView kind={kind} plan={plan} />}

        {plan && kind === 'shutdown' && plan.blocked === '' && (
          <div class="space-y-2">
            <Label htmlFor="op-confirm-name">{t('ops.dialog.typeName', { name: cluster.name })}</Label>
            <Input id="op-confirm-name" value={typed} onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)} />
          </div>
        )}

        {error && <ErrorAlert {...error} />}

        <DialogFooter>
          <Button variant="ghost" onClick={() => (plan ? setPlan(null) : reset(false))}>
            {plan ? t('ops.dialog.back') : t('common.cancel')}
          </Button>
          {plan ? (
            <Button variant={startedByDefault[kind] ? 'default' : 'destructive'} disabled={!startable} onClick={start}>
              {busy ? t('ops.dialog.starting') : t('ops.dialog.start')}
            </Button>
          ) : (
            <Button disabled={busy || (needsTarget && targetProblem !== null)} onClick={review}>
              {busy ? t('ops.dialog.reviewing') : t('ops.dialog.review')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// PlanView says what the server's plan will do, in the words of the screen's
// language. The server's own text (a blocking reason) is shown as received.
function PlanView({ kind, plan }: { kind: OpKind; plan: PlanOperationResponse }) {
  const t = useT()
  const counts = planCounts(plan.machines)
  const steps = planSteps(plan.machines)
  const to = plan.target === '' ? plan.current : plan.target

  return (
    <div class="space-y-3 text-sm">
      <p class="font-medium">{t(`ops.plan.${kind}`, { from: plan.current, to })}</p>
      {plan.specChanges && <p class="text-muted-foreground">{t('ops.plan.specChanges', { to })}</p>}
      <p>{effectText(t, plan.effect)}</p>
      {plan.machines.length > 0 ? (
        <div>
          <p>{t('ops.plan.counts', { total: plan.machines.length, ...counts })}</p>
          <p class="text-muted-foreground">
            {kind === 'shutdown' ? t('ops.plan.orderShutdown') : t('ops.plan.orderRollout', { n: Math.max(plan.maxUnavailable, 1) })}
          </p>
          <ol class="mt-1 list-inside list-decimal">
            {steps.map((s, i) => (
              <li key={s.step}>
                {t('ops.plan.step', { n: i + 1, names: s.names.join(', '), role: roleText(t, s.role) })}
              </li>
            ))}
          </ol>
        </div>
      ) : (
        plan.effect !== OperationEffect.NONE && <p>{t('ops.plan.noMachines')}</p>
      )}
      {plan.held.length > 0 && <p>{t('ops.plan.held', { names: plan.held.join(', ') })}</p>}
      {kind !== 'shutdown' && <p class="text-muted-foreground">{t('ops.plan.pauseOnFailure')}</p>}
      {plan.blocked !== '' && (
        <Alert variant="destructive">
          <AlertTitle>{t('ops.plan.blocked')}</AlertTitle>
          <AlertDescription>{plan.blocked}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}

function roleText(t: ReturnType<typeof useT>, role: string): string {
  return role === 'controlplane' ? t('ops.plan.role.controlplane') : t('ops.plan.role.worker')
}

function effectText(t: ReturnType<typeof useT>, effect: number): string {
  switch (effect) {
    case OperationEffect.REBOOT:
      return t('ops.plan.effectReboot')
    case OperationEffect.RESTART_COMPONENTS:
      return t('ops.plan.effectRestart')
    case OperationEffect.STOP:
      return t('ops.plan.effectStop')
    default:
      return t('ops.plan.effectNone')
  }
}
