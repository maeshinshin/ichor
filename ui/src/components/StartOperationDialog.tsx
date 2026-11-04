import { useEffect, useState } from 'preact/hooks'
import { CircleXIcon, InfoIcon } from 'lucide-preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Item, ItemContent, ItemDescription, ItemGroup, ItemTitle } from '@/components/ui/item'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { VersionPicker } from '@/components/VersionPicker'
import { AlertDialogCancel, AlertDialogFooter } from '@/components/ui/alert-dialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { wireKindOf } from '@/components/opkind'
import { OperationEffect, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import { describeError, isOverridable, type ErrorShape } from '@/lib/errors'
import { versionProblem } from '@/lib/clusterOps'
import { confirmMatches, kindNeedsTypedName, kubernetesStages, planCounts, planSteps, targetError, type OpKind } from '@/lib/operations'
import { t } from '@/lib/i18n'
import { type TargetList } from '@/lib/upgradeTargets'
import { navigate } from '@/router'

const startedByDefault: Record<OpKind, boolean> = { talos: true, kubernetes: true, ichor: true, cni: true, shutdown: false, 'ca-rotation': false, config: false }

// StartOperationDialog is the only way the UI starts an upgrade or a shutdown.
// It never writes on the first click: "Review" asks the server for the plan
// (which machines, in which order, what happens to each) and shows it, and
// only the second, separate "Start" writes. A shutdown also needs the cluster's
// name typed. What the server refuses (another operation running, no
// permission) is shown as the server says it.
export function StartOperationDialog({
  kind,
  cluster,
  current = '',
  disabled = false,
  label,
  size = 'sm',
  open: controlledOpen,
  onOpenChange,
}: {
  kind: OpKind
  cluster: { namespace: string; name: string }
  // current is the version (or kube-proxy mode) the spec says now; the target is checked against it.
  current?: string
  disabled?: boolean
  // label is the trigger's text; the dialog's own title always uses the full action name.
  label?: string
  // size is the trigger's size; an AlertAction takes xs, as the stock Alert example does.
  size?: 'xs' | 'sm'
  // open and onOpenChange make the dialog controlled and without its own button (it is opened from a menu).
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const [ownOpen, setOwnOpen] = useState(false)
  const open = controlledOpen ?? ownOpen
  const setOpen = (v: boolean) => {
    setOwnOpen(v)
    onOpenChange?.(v)
  }
  // A mode starts as the other one. A Talos or Kubernetes version starts empty and is preselected from the
  // candidates ListUpgradeTargets gives (its default); any other version starts as the current one.
  const isVersionPick = kind === 'talos' || kind === 'kubernetes'
  const [target, setTarget] = useState(kind === 'cni' ? (current === 'replaced' ? 'enabled' : 'replaced') : isVersionPick ? '' : current)
  const [touched, setTouched] = useState(false)
  const [typed, setTyped] = useState('')
  const [plan, setPlan] = useState<PlanOperationResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  // list is the candidates for the picker, read when the dialog opens; null while they are read.
  const [list, setList] = useState<TargetList | null>(null)
  // The override lifts a compatibility refusal the plan names; the reason is optional.
  const [overridden, setOverridden] = useState(false)
  // overridable is set when the server refused the plan for an incompatibility the override lifts; no plan exists then.
  const [overridable, setOverridable] = useState(false)
  const [reason, setReason] = useState('')

  const needsName = kindNeedsTypedName(kind)
  const needsTarget = !needsName
  const targetProblem = needsTarget ? checkTarget(kind, current, target) : null
  const startable =
    plan !== null && plan.blocked === '' && (!needsName || confirmMatches(typed, cluster.name)) && !busy

  useEffect(() => {
    if (!open || !isVersionPick) return
    let live = true
    setList(null)
    operationClient.listUpgradeTargets({ cluster, kind: wireKindOf(kind) }).then(
      (res) => {
        if (!live) return
        setList(res)
        // The default is preselected unless the person has already picked a version.
        if (res.default) setTarget((cur) => (cur === '' ? res.default : cur))
      },
      (err) => {
        if (!live) return
        const e = describeError(err)
        setList({ recommended: [], patch: [], nextMinor: [], default: '', available: false, unavailableReason: e.detail || e.title })
      },
    )
    return () => {
      live = false
    }
  }, [open, kind, cluster.namespace, cluster.name])

  function reset(next: boolean) {
    setOpen(next)
    if (!next) {
      setPlan(null)
      setError(null)
      setTyped('')
      setTouched(false)
      setOverridden(false)
      setOverridable(false)
      setReason('')
    }
  }

  async function review(withOverride = overridden) {
    setBusy(true)
    setError(null)
    try {
      setPlan(
        await operationClient.planOperation({
          cluster,
          kind: wireKindOf(kind),
          target: needsTarget ? target.trim() : '',
          override: withOverride ? { reason: reason.trim() } : undefined,
        }),
      )
    } catch (err) {
      setError(describeError(err))
      setOverridable(withOverride || isOverridable(err))
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
        override: overridden ? { reason: reason.trim() } : undefined,
      })
      reset(false)
      notifyDone(t('toast.opStarted', { action: t(`ops.start.${kind}`) }))
      if (res.operation?.ref) navigate(`/operations/${encodeURIComponent(res.operation.ref.name)}`)
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
    <ConfirmDialog
      open={open}
      onOpenChange={reset}
      trigger={
        controlledOpen !== undefined ? undefined : <Button size={size} variant={kind === 'shutdown' ? 'destructive' : 'outline'} disabled={disabled} aria-label={label ? t(`ops.start.${kind}`) : undefined}>
          {label ?? t(`ops.start.${kind}`)}
        </Button>
      }
      title={t('ops.dialog.title', { action: t(`ops.start.${kind}`), cluster: cluster.name })}
      description={plan ? undefined : t('cluster.operationsHint')}
      footer={
        <AlertDialogFooter>
          {plan ? (
            <Button
              variant="ghost"
              onClick={() => {
                setPlan(null)
                setOverridden(false)
              }}
            >
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
            <Button disabled={busy || (needsTarget && targetProblem !== null) || (overridable && !overridden)} onClick={() => review()}>
              {busy ? t('ops.dialog.reviewing') : t('ops.dialog.review')}
            </Button>
          )}
        </AlertDialogFooter>
      }
    >

      {!plan && needsTarget && (
        <Field data-invalid={touched && targetProblem !== null}>
          <FieldLabel htmlFor={`op-target-${kind}`}>{t(kind === 'cni' ? 'ops.dialog.targetMode' : 'ops.dialog.targetVersion')}</FieldLabel>
          {kind === 'cni' ? (
            <Select
              value={target}
              onValueChange={(v) => {
                setTouched(true)
                setTarget(v)
              }}
            >
              <SelectTrigger id={`op-target-${kind}`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="replaced">{t('ops.dialog.modeReplaced')}</SelectItem>
                <SelectItem value="enabled">{t('ops.dialog.modeEnabled')}</SelectItem>
              </SelectContent>
            </Select>
          ) : isVersionPick ? (
            <VersionPicker
              id={`op-target-${kind}`}
              value={target}
              list={list}
              onValueChange={(v) => {
                setTouched(true)
                setTarget(v)
                setError(null)
                setOverridable(false)
                setOverridden(false)
              }}
            />
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
          {current !== '' && <FieldDescription>{t(kind === 'cni' ? 'ops.modeHint' : 'ops.versionHint', { current })}</FieldDescription>}
          {touched && targetProblem && <FieldError>{problemText(kind, targetProblem, current)}</FieldError>}
        </Field>
      )}

      {(!plan || plan.blocked === '') && (
        <Alert variant="destructive">
          <CircleXIcon />
          <AlertTitle>{t('ops.warn.title')}</AlertTitle>
          <AlertDescription>{t(`ops.warn.${kind}`)}</AlertDescription>
        </Alert>
      )}

      {plan && <PlanView kind={kind} plan={plan} clusterName={cluster.name} />}

      {plan && plan.warning !== '' && (
        <Alert>
          <InfoIcon />
          <AlertTitle>{t('ops.plan.warningTitle')}</AlertTitle>
          <AlertDescription>{plan.warning}</AlertDescription>
        </Alert>
      )}

      {(overridable || overridden || (plan && plan.overrideRequired)) && (
        <div class="space-y-3">
          <div class="flex items-center gap-2 text-sm">
            <Checkbox
              id={`op-override-${kind}`}
              checked={overridden}
              onCheckedChange={(on: boolean | 'indeterminate') => {
                const next = on === true
                setOverridden(next)
                void review(next)
              }}
            />
            <Label htmlFor={`op-override-${kind}`}>{t('ops.dialog.override')}</Label>
          </div>
          <div class="space-y-2">
            <Label htmlFor={`op-reason-${kind}`}>{t('ops.dialog.reason')}</Label>
            <Textarea id={`op-reason-${kind}`} value={reason} onInput={(e) => setReason((e.currentTarget as HTMLTextAreaElement).value)} />
            <p class="text-xs text-muted-foreground">{t('ops.dialog.overrideHint')}</p>
          </div>
        </div>
      )}

      {plan && needsName && plan.blocked === '' && (
        <Field>
          <FieldLabel htmlFor="op-confirm-name">{t('ops.dialog.typeName', { name: cluster.name })}</FieldLabel>
          <Input id="op-confirm-name" value={typed} onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)} />
        </Field>
      )}

      {error && <ErrorAlert {...error} />}

    </ConfirmDialog>
    </>
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

function problemText(kind: OpKind, problem: TargetProblem, current: string): string {
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
  const counts = planCounts(plan.machines)
  const steps = planSteps(plan.machines)
  const to = plan.target === '' ? plan.current : plan.target

  return (
    <div class="space-y-3">
      <Card size="sm">
        <CardHeader>
          <CardTitle>
            {kind === 'talos' && plan.targetSchematic !== '' && to === plan.current
              ? t('ops.plan.talosSchematic', { version: plan.current, from: plan.currentSchematic, to: plan.targetSchematic })
              : t(`ops.plan.${kind}`, { from: plan.current, to })}
          </CardTitle>
          {plan.specChanges && <CardDescription>{t('ops.plan.specChanges', { to })}</CardDescription>}
        </CardHeader>
        <CardContent className="space-y-3">
          <p>{effectText(plan.effect, kind)}</p>
          {kind === 'kubernetes' && plan.stages.length > 0 && <KubernetesStages names={plan.stages} />}
          {kind === 'ca-rotation' && plan.stages.length > 0 && <p>{t('ops.plan.stages', { stages: plan.stages.join(', ') })}</p>}
          {plan.machines.length > 0 ? (
            <>
              <p>{t('ops.plan.counts', { total: plan.machines.length, ...counts })}</p>
              <CardDescription>
                {kind === 'shutdown'
                  ? t('ops.plan.orderShutdown')
                  : kind === 'ca-rotation'
                    ? t('ops.plan.orderCARotation')
                    : t('ops.plan.orderRollout', { n: Math.max(plan.maxUnavailable, 1) })}
              </CardDescription>
              <ItemGroup>
                {steps.map((s, i) => (
                  <Item key={s.step} size="xs" variant="outline">
                    <ItemContent>
                      <ItemTitle>{t('ops.plan.step', { n: i + 1, names: s.names.join(', '), role: roleText(s.role) })}</ItemTitle>
                    </ItemContent>
                  </Item>
                ))}
              </ItemGroup>
              {kind === 'config' && (
                <ItemGroup>
                  {plan.machines
                    .filter((m) => m.rebootFields.length > 0)
                    .map((m) => (
                      <Item key={m.name} size="xs">
                        <ItemContent>
                          <ItemDescription>{t('ops.plan.rebootFields', { name: m.name, fields: m.rebootFields.join(', ') })}</ItemDescription>
                        </ItemContent>
                      </Item>
                    ))}
                </ItemGroup>
              )}
              {kind === 'config' && <CardDescription>{t('ops.plan.ownNodeLast')}</CardDescription>}
            </>
          ) : (
            plan.effect !== OperationEffect.NONE && <p>{t('ops.plan.noMachines')}</p>
          )}
          {plan.held.length > 0 && <p>{t('ops.plan.held', { names: plan.held.join(', ') })}</p>}
          {kind !== 'shutdown' && <CardDescription>{t('ops.plan.pauseOnFailure')}</CardDescription>}
        </CardContent>
      </Card>
      {plan.kubernetesApiUnavailable && (
        <Alert>
          <InfoIcon />
          <AlertTitle>{t('ops.plan.apiDownTitle')}</AlertTitle>
          <AlertDescription>{t('ops.plan.apiDown')}</AlertDescription>
        </Alert>
      )}
      {kind === 'ca-rotation' && (
        <Alert variant="destructive">
          <CircleXIcon />
          <AlertTitle>{t('ops.plan.credentialsTitle')}</AlertTitle>
          <AlertDescription>{t('ops.plan.credentials', { name: clusterName })}</AlertDescription>
        </Alert>
      )}
      {plan.replacesSecretsBundle && (
        <Alert>
          <InfoIcon />
          <AlertTitle>{t('ops.plan.bundleStaleTitle')}</AlertTitle>
          <AlertDescription>{t('ops.plan.bundleStale', { name: clusterName })}</AlertDescription>
        </Alert>
      )}
      {plan.blocked !== '' && (
        <Alert variant="destructive">
          <CircleXIcon />
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
  return (
    <div class="space-y-2">
      <p>{t('ops.plan.kubeStages', { n: names.length })}</p>
      <ItemGroup>
        {names.map((name, i) => (
          <Item key={name} size="xs" variant="outline">
            <ItemContent>
              <ItemTitle>{name}</ItemTitle>
              {kubernetesStages[i] !== undefined && <ItemDescription>{t(`ops.plan.kubeStageHint.${kubernetesStages[i]}`)}</ItemDescription>}
            </ItemContent>
          </Item>
        ))}
      </ItemGroup>
    </div>
  )
}

function roleText(role: string): string {
  return role === 'controlplane' ? t('ops.plan.role.controlplane') : t('ops.plan.role.worker')
}

function effectText(effect: number, kind: OpKind): string {
  switch (effect) {
    case OperationEffect.REBOOT:
      return t(kind === 'config' ? 'ops.plan.effectConfigReboot' : 'ops.plan.effectReboot')
    case OperationEffect.RESTART_COMPONENTS:
      return t(kind === 'config' ? 'ops.plan.effectConfigRestart' : 'ops.plan.effectRestart')
    case OperationEffect.STOP:
      return t('ops.plan.effectStop')
    case OperationEffect.CA_ROTATION:
      return t('ops.plan.effectCARotation')
    default:
      return t('ops.plan.effectNone')
  }
}
