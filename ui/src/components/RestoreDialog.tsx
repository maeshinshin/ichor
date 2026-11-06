import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { AlertDialogCancel, AlertDialogFooter } from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { CircleXIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import { CopyableName } from '@/components/CopyableName'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Item, ItemContent, ItemGroup, ItemMedia, ItemTitle } from '@/components/ui/item'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationKind, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { useAction } from '@/hooks/use-action'
import { operationClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { canRestore, canReview, snapshotChoices, type SnapshotChoice } from '@/lib/etcdRestore'
import { formatBytes, formatDate } from '@/lib/format'
import { notifyDone } from '@/lib/notify'
import { t } from '@/lib/i18n'
import { navigate } from '@/router'

// RestoreDialog is "Restore from backup" in the cluster page's Danger zone (docs/adr/0089). It never
// writes on the first click: the user picks a snapshot from the cluster's backups, "Review" asks the
// server for the plan (what happens, in order, and what is lost) and shows it, and only the second,
// separate button writes. That one also needs the cluster's name typed and the override ticked (the
// server asks Kubernetes whether this user holds the override permission, and refuses if not). The UI only
// writes the operation; the controller wipes and recovers etcd.
export function RestoreDialog({ cluster, disabled = false }: { cluster: { namespace: string; name: string }; disabled?: boolean }) {
  const [open, setOpen] = useState(false)
  const [choices, setChoices] = useState<SnapshotChoice[] | null>(null)
  const [chosen, setChosen] = useState('')
  const [plan, setPlan] = useState<PlanOperationResponse | null>(null)
  const [typed, setTyped] = useState('')
  const [overridden, setOverridden] = useState(false)
  const { busy, error, setError, run } = useAction()

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
      setOverridden(false)
    }
  }

  const request = (confirm: string) => ({
    cluster,
    kind: OperationKind.ETCD_RESTORE,
    etcdRestore: {
      backup: choice?.backup,
      snapshot: choice?.name ?? '',
      confirm,
      override: overridden ? {} : undefined,
    },
  })

  const review = () => run(async () => setPlan(await operationClient.planOperation(request(''))))

  const start = () =>
    run(async () => {
      const res = await operationClient.createOperation(request(typed.trim()))
      reset(false)
      notifyDone(t('restore.started', { cluster: cluster.name }))
      if (res.operation?.ref) navigate(`/operations/${encodeURIComponent(res.operation.ref.name)}`)
    })

  const startable = canRestore({
    planned: plan !== null,
    blocked: plan?.blocked ?? '',
    clusterName: plan?.confirmName ?? '',
    typed,
    overridden,
    busy,
  })

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={(o) => !busy && reset(o)}
      trigger={
        <Button size="sm" variant="outline" disabled={disabled}>
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
        <Field>
          <FieldLabel htmlFor="restore-snapshot">{t('restore.snapshot')}</FieldLabel>
          {choices === null ? (
            <>
              <Skeleton className="h-9 w-full" />
              <span class="sr-only">{t('restore.loading')}</span>
            </>
          ) : choices.length === 0 ? (
            <Alert>
              <AlertTitle>{t('restore.noneTitle')}</AlertTitle>
              <AlertDescription>{t('restore.none')}</AlertDescription>
            </Alert>
          ) : (
            <Select value={chosen} onValueChange={setChosen}>
              <SelectTrigger id="restore-snapshot" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {choices.map((c) => (
                  <SelectItem key={c.key} value={c.key}>
                    {t('restore.option', { when: formatDate(new Date(c.seconds * 1000)), backup: c.backup.name, size: formatBytes(c.size) })}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
        </Field>
      )}

      {!plan && (
        <Alert variant="destructive">
          <CircleXIcon />
          <AlertTitle>{t('restore.warnTitle')}</AlertTitle>
          <AlertDescription>{t('restore.warn')}</AlertDescription>
        </Alert>
      )}

      {plan && plan.blocked !== '' && (
        <Alert variant="destructive">
          <CircleXIcon />
          <AlertTitle>{t('ops.plan.blocked')}</AlertTitle>
          <AlertDescription>{plan.blocked}</AlertDescription>
        </Alert>
      )}

      {plan && plan.blocked === '' && (
        <div class="space-y-3">
          <Card size="sm">
            <CardHeader>
              <CardTitle>{t('node.action.stepsTitle')}</CardTitle>
              <CardDescription>{t('ops.plan.pauseOnFailure')}</CardDescription>
            </CardHeader>
            <CardContent>
              <ItemGroup>
                {plan.steps.map((s, i) => (
                  <Item key={s} size="xs">
                    <ItemMedia>
                      <Badge variant="secondary">{i + 1}</Badge>
                    </ItemMedia>
                    <ItemContent>
                      <ItemTitle>{s}</ItemTitle>
                    </ItemContent>
                  </Item>
                ))}
              </ItemGroup>
            </CardContent>
          </Card>
          {plan.kubernetesApiUnavailable && (
            <Alert variant="destructive">
              <CircleXIcon />
              <AlertTitle>{t('node.action.apiDown')}</AlertTitle>
            </Alert>
          )}
          {plan.warning !== '' && (
            <Alert variant="destructive">
              <CircleXIcon />
              <AlertTitle>{t('node.action.warning')}</AlertTitle>
              <AlertDescription>{plan.warning}</AlertDescription>
            </Alert>
          )}
          <Field>
            <CopyableName value={cluster.name} ariaLabel={t('ops.dialog.copyName')} />
            <FieldLabel htmlFor="restore-name">{t('ops.dialog.typeName')}</FieldLabel>
            <Input id="restore-name" autoComplete="off" value={typed} onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)} />
          </Field>
          <div class="flex items-center gap-2 text-sm">
            <Checkbox id="restore-override" checked={overridden} onCheckedChange={(on: boolean | 'indeterminate') => setOverridden(on === true)} />
            <Label htmlFor="restore-override">{t('restore.override')}</Label>
          </div>
        </div>
      )}

      {error && <ErrorAlert {...error} />}
    </ConfirmDialog>
  )
}
