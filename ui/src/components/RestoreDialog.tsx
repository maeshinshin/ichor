import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { AlertDialogCancel, AlertDialogFooter } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationKind, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { canRestore, canReview, snapshotChoices, type SnapshotChoice } from '@/lib/etcdRestore'
import { formatBytes, formatDate } from '@/lib/format'
import { notifyDone } from '@/lib/notify'
import { useT } from '@/lib/useT'
import { navigate, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

// RestoreDialog is "Restore from backup…" in the cluster page's Danger zone. It never
// writes on the first click: the user picks a snapshot from the cluster's backups, "Review" asks the
// server for the plan (what happens, in order, and what is lost) and shows it, and only the second,
// separate button writes. That one also needs the cluster's name typed and a reason for the break-glass
// permission (the server asks Kubernetes whether this user holds it, and refuses if not). The UI only
// writes the operation; the controller wipes and recovers etcd.
export function RestoreDialog({ cluster, disabled = false }: { cluster: { namespace: string; name: string }; disabled?: boolean }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [choices, setChoices] = useState<SnapshotChoice[] | null>(null)
  const [chosen, setChosen] = useState('')
  const [plan, setPlan] = useState<PlanOperationResponse | null>(null)
  const [typed, setTyped] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  const choice = choices?.find((c) => c.key === chosen)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setChoices(null)
    operationClient.listEtcdBackups({ cluster }).then(
      (res) => {
        if (cancelled) return
        const list = snapshotChoices(res.backups)
        setChoices(list)
        setChosen((cur) => (list.some((c) => c.key === cur) ? cur : (list[0]?.key ?? '')))
      },
      (err) => {
        if (cancelled) return
        setChoices([])
        setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [open, cluster.namespace, cluster.name])

  function reset(next: boolean) {
    setOpen(next)
    if (!next) {
      setPlan(null)
      setError(null)
      setTyped('')
      setReason('')
    }
  }

  const request = (confirm: string) => ({
    cluster,
    kind: OperationKind.ETCD_RESTORE,
    etcdRestore: {
      backup: choice?.backup,
      snapshot: choice?.name ?? '',
      confirm,
      breakGlass: reason.trim() === '' ? undefined : { reason: reason.trim() },
    },
  })

  async function review() {
    setBusy(true)
    setError(null)
    try {
      setPlan(await operationClient.planOperation(request('')))
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
      const res = await operationClient.createOperation(request(typed.trim()))
      reset(false)
      notifyDone(t('restore.started', { cluster: cluster.name }))
      if (res.operation?.ref) navigate(objectRefPath('/operations', res.operation.ref))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  const startable = canRestore({
    planned: plan !== null,
    blocked: plan?.blocked ?? '',
    clusterName: plan?.confirmName ?? '',
    typed,
    reason,
    busy,
  })

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(o) => !busy && reset(o)}
      trigger={
        <Button size="sm" variant="destructive" disabled={disabled}>
          {t('restore.action')}
        </Button>
      }
      title={t('restore.title', { cluster: cluster.name })}
      description={plan ? undefined : t('restore.description')}
      footer={
        <AlertDialogFooter>
          {plan ? (
            <Button variant="ghost" disabled={busy} onClick={() => setPlan(null)}>
              {t('ops.dialog.back')}
            </Button>
          ) : (
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
          )}
          {plan ? (
            <Button variant="destructive" disabled={!startable} onClick={start}>
              {busy ? t('restore.starting') : t('restore.go')}
            </Button>
          ) : (
            <Button disabled={!canReview({ choice, busy })} onClick={review}>
              {busy ? t('ops.dialog.reviewing') : t('ops.dialog.review')}
            </Button>
          )}
        </AlertDialogFooter>
      }
    >
      {!plan && (
        <div class="space-y-2">
          <Label htmlFor="restore-snapshot">{t('restore.snapshot')}</Label>
          {choices === null ? (
            <p class="text-sm text-muted-foreground">{t('restore.loading')}</p>
          ) : choices.length === 0 ? (
            <Alert>
              <AlertTitle>{t('restore.noneTitle')}</AlertTitle>
              <AlertDescription>{t('restore.none')}</AlertDescription>
            </Alert>
          ) : (
            <select
              id="restore-snapshot"
              class="h-9 w-full rounded-md border bg-background px-2 text-sm"
              value={chosen}
              onChange={(e) => setChosen((e.currentTarget as HTMLSelectElement).value)}
            >
              {choices.map((c) => (
                <option key={c.key} value={c.key}>
                  {t('restore.option', { when: formatDate(new Date(c.seconds * 1000)), backup: c.backup.name, size: formatBytes(c.size) })}
                </option>
              ))}
            </select>
          )}
        </div>
      )}

      {!plan && (
        <Alert variant="destructive">
          <AlertTitle>{t('restore.warnTitle')}</AlertTitle>
          <AlertDescription>{t('restore.warn')}</AlertDescription>
        </Alert>
      )}

      {plan && plan.blocked !== '' && (
        <Alert variant="destructive">
          <AlertTitle>{t('ops.plan.blocked')}</AlertTitle>
          <AlertDescription>{plan.blocked}</AlertDescription>
        </Alert>
      )}

      {plan && plan.blocked === '' && (
        <div class="space-y-3 text-sm">
          <div>
            <p class="mb-1 font-medium">{t('node.action.stepsTitle')}</p>
            <ol class="list-inside list-decimal space-y-0.5">
              {plan.steps.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ol>
          </div>
          {plan.kubernetesApiUnavailable && (
            <Alert variant="destructive">
              <AlertTitle>{t('node.action.apiDown')}</AlertTitle>
            </Alert>
          )}
          {plan.warning !== '' && (
            <Alert variant="destructive">
              <AlertTitle>{t('node.action.warning')}</AlertTitle>
              <AlertDescription>{plan.warning}</AlertDescription>
            </Alert>
          )}
          <p class="text-muted-foreground">{t('ops.plan.pauseOnFailure')}</p>
          <div class="space-y-2">
            <Label htmlFor="restore-name">{t('ops.dialog.typeName', { name: cluster.name })}</Label>
            <Input id="restore-name" autoComplete="off" value={typed} onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)} />
          </div>
          <div class="space-y-2">
            <Label htmlFor="restore-reason">{t('node.action.reason')}</Label>
            <Textarea id="restore-reason" value={reason} onInput={(e) => setReason((e.currentTarget as HTMLTextAreaElement).value)} />
            <p class="text-xs text-muted-foreground">{t('restore.reasonHint')}</p>
          </div>
        </div>
      )}

      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
