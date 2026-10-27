import { useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { AlertDialogCancel, AlertDialogFooter } from '@/components/ui/alert-dialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { wireKindOf } from '@/components/opkind'
import { OperationEffect, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import { describeError } from '@/lib/errors'
import { versionProblem } from '@/lib/clusterOps'
import { confirmMatches, kindNeedsTarget, kindNeedsTypedName, kubernetesStages, planCounts, planSteps, targetError, type OpKind } from '@/lib/operations'
import { useT } from '@/lib/useT'
import { navigate, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

const startedByDefault: Record<OpKind, boolean> = { talos: true, kubernetes: true, ichor: true, cni: true, shutdown: false, 'ca-rotation': false, config: false }

// StartOperationDialog is the only way the UI starts an upgrade or a shutdown.
// It never writes on the first click: "Review" asks the server for the plan
// (which machines, in which order, what happens to each) and shows it, and
// only the second, separate "Start" writes. A shutdown also needs the cluster's
// name typed. What the server refuses (another operation running, a spec that
// Argo CD tracks, no permission) is shown as the server says it.
export function StartOperationDialog({
  kind,
  cluster,
  current = '',
  disabled = false,
  label,
}: {
  kind: OpKind
  cluster: { namespace: string; name: string }
  // current is the version (or kube-proxy mode) the spec says now; the target is checked against it.
  current?: string
  disabled?: boolean
  // label is the trigger's text; the dialog's own title always uses the full action name.
  label?: string
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  // A version starts as the current one, so a person edits the last digits; a mode starts as the other one.
  const [target, setTarget] = useState(kind === 'cni' ? (current === 'replaced' ? 'enabled' : 'replaced') : current)
  const [touched, setTouched] = useState(false)
  const [typed, setTyped] = useState('')
  const [plan, setPlan] = useState<PlanOperationResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  const needsTarget = kindNeedsTarget(kind)
  const needsName = kindNeedsTypedName(kind)
  const targetProblem = needsTarget ? checkTarget(kind, current, target) : null
  const startable =
    plan !== null && plan.blocked === '' && (!needsName || confirmMatches(typed, cluster.name)) && !busy

  function reset(next: boolean) {
    setOpen(next)
    if (!next) {
      setPlan(null)
      setError(null)
      setTyped('')
      setTouched(false)
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
      notifyDone(t('toast.opStarted', { action: t(`ops.start.${kind}`) }))
      if (res.operation?.ref) navigate(objectRefPath('/operations', res.operation.ref))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={reset}
      trigger={
        <Button size="sm" variant={kind === 'shutdown' ? 'destructive' : 'outline'} disabled={disabled} aria-label={label ? t(`ops.start.${kind}`) : undefined}>
          {label ?? t(`ops.start.${kind}`)}
        </Button>
      }
      title={t('ops.dialog.title', { action: t(`ops.start.${kind}`), cluster: cluster.name })}
      description={plan ? undefined : t('cluster.operationsHint')}
      footer={
        <AlertDialogFooter>
          {plan ? (
            <Button variant="ghost" onClick={() => setPlan(null)}>
              {t('ops.dialog.back')}
            </Button>
          ) : (
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
          )}
          {plan ? (
            <Button variant={startedByDefault[kind] ? 'default' : 'destructive'} disabled={!startable} onClick={start}>
              {busy ? t('ops.dialog.starting') : t('ops.dialog.start')}
            </Button>
          ) : (
            <Button disabled={busy || (needsTarget && targetProblem !== null)} onClick={review}>
              {busy ? t('ops.dialog.reviewing') : t('ops.dialog.review')}
            </Button>
          )}
        </AlertDialogFooter>
      }
    >

      {!plan && needsTarget && (
        <div class="space-y-2">
          <Label htmlFor={`op-target-${kind}`}>{t(kind === 'cni' ? 'ops.dialog.targetMode' : 'ops.dialog.targetVersion')}</Label>
        {kind === 'cni' ? (
            <select
              id={`op-target-${kind}`}
              class="h-9 w-full rounded-md border bg-background px-2 text-sm"
              value={target}
              onChange={(e) => {
                setTouched(true)
                setTarget((e.currentTarget as HTMLSelectElement).value)
              }}
            >
              <option value="replaced">{t('ops.dialog.modeReplaced')}</option>
              <option value="enabled">{t('ops.dialog.modeEnabled')}</option>
            </select>
          ) : (
            <Input
              id={`op-target-${kind}`}
              value={target}
              onInput={(e) => {
                setTouched(true)
                setTarget((e.currentTarget as HTMLInputElement).value)
              }}
            />
          )}
          {current !== '' && <p class="text-xs text-muted-foreground">{t(kind === 'cni' ? 'ops.modeHint' : 'ops.versionHint', { current })}</p>}
          {touched && targetProblem && <p class="text-xs text-destructive">{problemText(t, kind, targetProblem, current)}</p>}
        </div>
      )}

      {(!plan || plan.blocked === '') && (
        <Alert variant="destructive">
          <AlertTitle>{t('ops.warn.title')}</AlertTitle>
          <AlertDescription>{t(`ops.warn.${kind}`)}</AlertDescription>
        </Alert>
      )}

      {plan && <PlanView kind={kind} plan={plan} clusterName={cluster.name} />}

      {plan && plan.warning !== '' && (
        <Alert>
          <AlertTitle>{t('ops.plan.warningTitle')}</AlertTitle>
          <AlertDescription>{plan.warning}</AlertDescription>
        </Alert>
      )}

      {plan && needsName && plan.blocked === '' && (
        <div class="space-y-2">
          <Label htmlFor="op-confirm-name">{t('ops.dialog.typeName', { name: cluster.name })}</Label>
          <Input id="op-confirm-name" value={typed} onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)} />
        </div>
      )}

      {error && <ErrorAlert {...error} />}

    </ConfirmDialog>
  )
}

type TargetProblem = 'required' | 'invalid' | 'same' | 'older'

// checkTarget: the shape rules of the kind, and, with a current value known, that
// the target is a change (a mode) or newer (a version).
function checkTarget(kind: OpKind, current: string, target: string): TargetProblem | null {
  const shape = targetError(kind, target)
  if (shape) return shape
  if (kind === 'cni') return current !== '' && target.trim() === current ? 'same' : null
  return versionProblem(current, target)
}

function problemText(t: ReturnType<typeof useT>, kind: OpKind, problem: TargetProblem, current: string): string {
  switch (problem) {
    case 'required':
      return t('ops.dialog.errRequired')
    case 'same':
      return t(kind === 'cni' ? 'ops.dialog.errSameMode' : 'ops.dialog.errSame')
    case 'older':
      return t('ops.dialog.errOlder', { current })
    default:
      return t('ops.dialog.errInvalid')
  }
}

// PlanView says what the server's plan will do, in the words of the screen's
// language. The server's own text (a blocking reason) is shown as received.
function PlanView({ kind, plan, clusterName }: { kind: OpKind; plan: PlanOperationResponse; clusterName: string }) {
  const t = useT()
  const counts = planCounts(plan.machines)
  const steps = planSteps(plan.machines)
  const to = plan.target === '' ? plan.current : plan.target

  return (
    <div class="space-y-3 text-sm">
      <p class="font-medium">{t(`ops.plan.${kind}`, { from: plan.current, to })}</p>
      {plan.specChanges && <p class="text-muted-foreground">{t('ops.plan.specChanges', { to })}</p>}
      <p>{effectText(t, plan.effect, kind)}</p>
      {kind === 'kubernetes' && plan.stages.length > 0 && <KubernetesStages names={plan.stages} />}
      {kind === 'ca-rotation' && plan.stages.length > 0 && <p>{t('ops.plan.stages', { stages: plan.stages.join(', ') })}</p>}
      {plan.machines.length > 0 ? (
        <div>
          <p>{t('ops.plan.counts', { total: plan.machines.length, ...counts })}</p>
          <p class="text-muted-foreground">
            {kind === 'shutdown'
              ? t('ops.plan.orderShutdown')
              : kind === 'ca-rotation'
                ? t('ops.plan.orderCARotation')
                : t('ops.plan.orderRollout', { n: Math.max(plan.maxUnavailable, 1) })}
          </p>
          <ol class="mt-1 list-inside list-decimal">
            {steps.map((s, i) => (
              <li key={s.step}>
                {t('ops.plan.step', { n: i + 1, names: s.names.join(', '), role: roleText(t, s.role) })}
              </li>
            ))}
          </ol>
          {kind === 'config' && (
            <ul class="mt-2 space-y-1">
              {plan.machines.map((m) => (
                <li key={m.name}>{t('ops.plan.rebootFields', { name: m.name, fields: m.rebootFields.join(', ') })}</li>
              ))}
            </ul>
          )}
          {kind === 'config' && <p class="mt-2 text-muted-foreground">{t('ops.plan.ownNodeLast')}</p>}
        </div>
      ) : (
        plan.effect !== OperationEffect.NONE && <p>{t('ops.plan.noMachines')}</p>
      )}
      {plan.kubernetesApiUnavailable && (
        <Alert>
          <AlertTitle>{t('ops.plan.apiDownTitle')}</AlertTitle>
          <AlertDescription>{t('ops.plan.apiDown')}</AlertDescription>
        </Alert>
      )}
      {plan.replacesSecretsBundle && (
        <Alert>
          <AlertTitle>{t('ops.plan.bundleStaleTitle')}</AlertTitle>
          <AlertDescription>{t('ops.plan.bundleStale', { name: clusterName })}</AlertDescription>
        </Alert>
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

// KubernetesStages lists the plan's stages in order, each with what it does. The
// explanation is looked up by the stage's position: the plan names them in the order of kubernetesStages.
function KubernetesStages({ names }: { names: string[] }) {
  const t = useT()
  return (
    <div>
      <p>{t('ops.plan.kubeStages', { n: names.length })}</p>
      <ol class="mt-1 list-inside list-decimal">
        {names.map((name, i) => (
          <li key={name}>
            <span class="font-medium">{name}</span>
            {kubernetesStages[i] !== undefined && <>: {t(`ops.plan.kubeStageHint.${kubernetesStages[i]}`)}</>}
          </li>
        ))}
      </ol>
    </div>
  )
}

function roleText(t: ReturnType<typeof useT>, role: string): string {
  return role === 'controlplane' ? t('ops.plan.role.controlplane') : t('ops.plan.role.worker')
}

function effectText(t: ReturnType<typeof useT>, effect: number, kind: OpKind): string {
  switch (effect) {
    case OperationEffect.REBOOT:
      return t(kind === 'config' ? 'ops.plan.effectConfigReboot' : 'ops.plan.effectReboot')
    case OperationEffect.RESTART_COMPONENTS:
      return t('ops.plan.effectRestart')
    case OperationEffect.STOP:
      return t('ops.plan.effectStop')
    case OperationEffect.CA_ROTATION:
      return t('ops.plan.effectCARotation')
    default:
      return t('ops.plan.effectNone')
  }
}
