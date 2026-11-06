import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { CircleAlertIcon, CircleXIcon, InfoIcon, ServerIcon, WorkflowIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Field as FormField, FieldLabel } from '@/components/ui/field'
import { Item, ItemContent, ItemDescription, ItemGroup, ItemSeparator, ItemTitle } from '@/components/ui/item'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { PageTitle } from '@/components/PageTitle'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ToneBadge } from '@/components/ToneBadge'
import { OperationControls } from '@/components/OperationControls'
import { CARotationCredentials } from '@/components/CARotationCredentials'
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { NodeAction, OperationKind, type Operation, type OperationEvent } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { credentialsPanel } from '@/lib/credentials'
import { describeError, type ErrorShape } from '@/lib/errors'
import { restoreStepProgress } from '@/lib/etcdRestore'
import { formatTimestamp } from '@/lib/format'
import { formatDuration, nodePath, spanMs, summaryFallback, visibleEvents } from '@/lib/operationDetail'
import { caStageProgress, keyStageProgress, kubernetesStageProgress, operationState, pollIntervalMs, progressCounts } from '@/lib/operations'
import { opStateTone } from '@/lib/status'
import { useCluster } from '@/lib/useCluster'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { t } from '@/lib/i18n'
import { Link, navigate } from '@/router'

const toDate = (ts: Timestamp | undefined): Date | undefined => (ts ? timestampDate(ts) : undefined)

// Progress view of one operation (architecture.md, "Upgrades": the three
// upgrade kinds share a progress view): the phase and each machine's state
// exactly as the controller recorded them in status. A machine that does not
// appear has not been reached. It polls while the operation can still change.
export function OperationDetail({ name }: { name: string }) {
  const { cluster, error: clusterError } = useCluster()
  const namespace = cluster?.ref?.namespace
  const [op, setOp] = useState<Operation | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [events, setEvents] = useState<OperationEvent[] | null>(null)
  const [eventsError, setEventsError] = useState<ErrorShape | null>(null)
  const [warningsOnly, setWarningsOnly] = useState(false)
  const [now, setNow] = useState(() => new Date())
  const perms = useMyPermissions(undefined, op?.kind === OperationKind.CA_ROTATION ? op.cluster : undefined)

  useEffect(() => {
    if (!namespace) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    async function load() {
      try {
        const res = await operationClient.getOperation({ operation: { namespace, name } })
        if (cancelled) return
        setOp(res.operation ?? null)
        setError(null)
        const next = res.operation ? pollIntervalMs(operationState(res.operation.phase, res.operation.failureReason)) : null
        if (next !== null) timer = setTimeout(load, next)
      } catch (err) {
        if (cancelled) return
        setError(describeError(err))
        timer = setTimeout(load, 10_000)
      }
    }

    setOp(null)
    load()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [namespace, name])

  // The activity log refreshes every 5 s while the operation can still change, and once more when it stops.
  const active = op !== null && pollIntervalMs(operationState(op.phase, op.failureReason)) !== null
  const loaded = op !== null
  useEffect(() => {
    if (!namespace || !loaded) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    async function load() {
      try {
        const res = await operationClient.listOperationEvents({ operation: { namespace, name } })
        if (cancelled) return
        setEvents(res.events)
        setEventsError(null)
      } catch (err) {
        if (cancelled) return
        setEventsError(describeError(err))
      }
      if (cancelled) return
      setNow(new Date())
      if (active) timer = setTimeout(load, 5_000)
    }

    load()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [namespace, name, loaded, active])

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (error && !op) return <ErrorAlert {...error} />
  if (!op) return <PageSkeleton label={t('ops.loading')} />

  const state = operationState(op.phase, op.failureReason)
  const shownEvents = visibleEvents(events ?? [], warningsOnly)
  const title = op.summary || summaryFallback(op.ref?.name ?? name, enumLabel(OperationKind, op.kind), op.nodeMachine?.name)
  const { done, running } = progressCounts(op.machines)
  const stage = caStageProgress(op.caRotationStage)
  const keyStage = keyStageProgress(op.keyRotationStage)
  const kubeStage = kubernetesStageProgress(op.kubernetesStage)
  const restoreStep = restoreStepProgress(op.etcdRestoreStep)
  const panel = credentialsPanel(op.kind === OperationKind.CA_ROTATION, op.caRotationStage, state === 'done', op.caTargets)

  const nodeLink = op.nodeMachine ? (
    <Button asChild variant="link" size="sm" className="h-auto max-w-full whitespace-normal break-all py-1 text-left">
      <Link to={`/nodes/${op.nodeMachine.name}`}>{op.nodeMachine.name}</Link>
    </Button>
  ) : undefined

  return (
    <>
      <PageTitle
        title={title}
        description={`${t('kind.operation')} ${op.ref?.name ?? name}`}
        badges={
          <>
            <ToneBadge tone={opStateTone(state)}>{t(`ops.state.${state}`)}</ToneBadge>
            {op.machines.length > 0 && <Badge variant="outline">{t('ops.progress', { done, running })}</Badge>}
            {stage && <Badge variant="outline">{t('ops.caStage', { stage: stage.name, n: stage.index, total: stage.total })}</Badge>}
            {keyStage && <Badge variant="outline">{t('ops.keyStage', { stage: keyStage.name, n: keyStage.index, total: keyStage.total })}</Badge>}
            {restoreStep && (
              <Badge variant="outline">
                {t('restore.stepName', { n: restoreStep.index, total: restoreStep.total, name: t(`restore.stepName.${restoreStep.name}`) })}
              </Badge>
            )}
            {kubeStage && (
              <Badge variant="outline">
                {t('ops.kubeStage', { stage: t(`ops.kubeStageName.${kubeStage.name}`), n: kubeStage.index, total: kubeStage.total })}
              </Badge>
            )}
          </>
        }
        actions={<OperationControls op={op} onChanged={setOp} onDeleted={() => navigate('/operations')} />}
      />
      <div class="space-y-4">
        <Card size="sm">
          <CardContent>
            <Fields>
              <Field label={t('ops.colKind')} value={enumLabel(OperationKind, op.kind)} />
              <Field label={t('ops.startedBy')} value={op.startedBy === 'ichor-auto-patch' ? t('ops.startedByAutoPatch') : op.startedBy} />
              <Field label={t('ops.startedAt')} value={formatTimestamp(op.createdAt)} />
              <Field label={t('ops.finishedAt')} value={formatTimestamp(op.finishedAt)} />
              <Field
                label={t('ops.duration')}
                value={formatDuration(spanMs(toDate(op.createdAt), toDate(op.finishedAt), now, state === 'done' || state === 'shutdown'))}
              />
              {op.kind === OperationKind.NODE ? (
                <>
                  <Field label={t('node.action.actionLabel')} value={enumLabel(NodeAction, op.nodeAction)} />
                  <Field label={t('node.action.nodeLabel')}>{nodeLink ?? '-'}</Field>
                  <Field label={t('node.action.progress')} value={op.nodeMessage} />
                </>
              ) : op.kind === OperationKind.ETCD_RESTORE ? (
                <>
                  <Field label={t('restore.snapshotLabel')} value={op.etcdRestoreSnapshot} />
                  <Field label={t('restore.snapshotTaken')} value={formatTimestamp(op.etcdRestoreSnapshotTime)} />
                  <Field label={t('restore.backupLabel')} value={op.etcdRestoreBackup?.name} />
                  <Field label={t('restore.progress')} value={op.etcdRestoreMessage} />
                </>
              ) : (
                <Field label={t('ops.maxUnavailable')} value={String(op.maxUnavailable || 1)} />
              )}
              {op.message && <Field label={t('ops.currently')} value={op.message} />}
            </Fields>
          </CardContent>
        </Card>

        {panel !== 'none' && (
          <Alert variant={panel === 'gate' ? 'destructive' : 'default'}>
            {panel === 'gate' ? <CircleXIcon /> : <InfoIcon />}
            <AlertTitle>{panel === 'gate' ? t('ops.awaitingCredentialsTitle') : t('creds.issuedTitle')}</AlertTitle>
            <AlertDescription>
              {panel === 'gate' ? t('ops.awaitingCredentials', { name: op.cluster?.name ?? '' }) : t('creds.issued')}
              <CARotationCredentials op={op} panel={panel} permitted={perms?.clusterCredentials} onChanged={setOp} />
            </AlertDescription>
          </Alert>
        )}
        {(op.kind === OperationKind.CA_ROTATION || op.kind === OperationKind.KEY_ROTATION) && state === 'done' && (
          <Alert>
            <InfoIcon />
            <AlertTitle>{t('ops.plan.bundleStaleTitle')}</AlertTitle>
            <AlertDescription>{t('ops.plan.bundleStale', { name: op.cluster?.name ?? '' })}</AlertDescription>
          </Alert>
        )}
        {state === 'failed' && (
          <Alert variant="destructive">
            <CircleXIcon />
            <AlertTitle>{t('ops.failure')}</AlertTitle>
            <AlertDescription>
              {op.failureReason}
              <br />
              {t('ops.failureHint')}
            </AlertDescription>
          </Alert>
        )}
        {error && <ErrorAlert {...error} />}

        <Card id="what" className="scroll-mt-section">
          <CardHeader>
            <CardTitle>{t('ops.whatItDoes')}</CardTitle>
            <CardDescription>{title}</CardDescription>
          </CardHeader>
          <CardContent>
            <ItemGroup>
              {op.details.map((d, i) => (
                <div key={d.key}>
                  {i > 0 && <ItemSeparator />}
                  <Item>
                    <ItemContent>
                      <ItemTitle>{d.key}</ItemTitle>
                      <ItemDescription className="break-words">{d.value}</ItemDescription>
                    </ItemContent>
                  </Item>
                </div>
              ))}
            </ItemGroup>
          </CardContent>
        </Card>

        <Card id="machines" className="scroll-mt-section">
          <CardHeader>
            <CardTitle>{t('ops.machines')}</CardTitle>
          </CardHeader>
          <CardContent>
            {op.machines.length === 0 ? (
              <EmptyNote icon={ServerIcon}>{t(op.kind === OperationKind.ICHOR ? 'ops.ichorJob' : 'ops.noMachines')}</EmptyNote>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('col.name')}</TableHead>
                    <TableHead>{t('ops.colState')}</TableHead>
                    <TableHead className="max-md:hidden">{t('ops.colStep')}</TableHead>
                    <TableHead className="max-md:hidden">{t('ops.startedAt')}</TableHead>
                    <TableHead className="max-md:hidden">{t('ops.finishedAt')}</TableHead>
                    <TableHead className="max-md:hidden">{t('ops.duration')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {op.machines.map((m) => (
                    <TableRow key={m.name}>
                      <TableCell>
                        <Button asChild variant="link" size="sm">
                          <Link to={nodePath(m.name)}>{m.displayName || m.name}</Link>
                        </Button>
                        <div class="whitespace-normal wrap-anywhere text-muted-foreground md:hidden">{[m.step, m.message].filter(Boolean).join(': ')}</div>
                        <div class="text-muted-foreground md:hidden">{formatTimestamp(m.startedAt)} · {formatDuration(spanMs(toDate(m.startedAt), toDate(m.finishedAt), now, m.phase === 'Completed'))}</div>
                      </TableCell>
                      <TableCell>
                        {/* The controller's own word (InProgress, Completed), as received. */}
                        <ToneBadge tone={m.phase === 'Completed' ? 'good' : m.phase === 'InProgress' ? 'progress' : 'neutral'}>{m.phase}</ToneBadge>
                      </TableCell>
                      <TableCell className="min-w-48 whitespace-normal break-words max-md:hidden">{[m.step, m.message].filter(Boolean).join(': ') || '-'}</TableCell>
                      <TableCell className="max-md:hidden">{formatTimestamp(m.startedAt)}</TableCell>
                      <TableCell className="max-md:hidden">{formatTimestamp(m.finishedAt)}</TableCell>
                      <TableCell className="max-md:hidden">{formatDuration(spanMs(toDate(m.startedAt), toDate(m.finishedAt), now, m.phase === 'Completed'))}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <Card id="activity" className="scroll-mt-section">
          <CardHeader>
            <CardTitle>{t('ops.activity')}</CardTitle>
            <CardAction>
              <FormField orientation="horizontal">
                <Checkbox id="op-warnings-only" checked={warningsOnly} onCheckedChange={(on: boolean | 'indeterminate') => setWarningsOnly(on === true)} />
                <FieldLabel htmlFor="op-warnings-only">{t('ops.warningsOnly')}</FieldLabel>
              </FormField>
            </CardAction>
          </CardHeader>
          <CardContent className="space-y-4">
            {eventsError && <ErrorAlert {...eventsError} />}
            {events !== null &&
              (shownEvents.length === 0 ? (
                <EmptyNote icon={WorkflowIcon}>{t('ops.noEvents')}</EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('ops.eventTime')}</TableHead>
                      <TableHead>{t('ops.eventType')}</TableHead>
                      <TableHead className="max-md:hidden">{t('ops.eventObject')}</TableHead>
                      <TableHead className="max-md:hidden">{t('ops.eventReason')}</TableHead>
                      <TableHead className="max-md:hidden">{t('ops.eventMessage')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {shownEvents.map((e, i) => (
                      <TableRow key={i}>
                        <TableCell className="max-md:whitespace-normal">
                          {formatTimestamp(e.time)}
                          <div class="text-muted-foreground wrap-anywhere md:hidden">{`${e.objectKind}/${e.objectName}`}</div>
                          <div class="text-muted-foreground wrap-anywhere md:hidden">{e.reason}</div>
                          <div class="text-muted-foreground wrap-anywhere md:hidden">{e.message}</div>
                        </TableCell>
                        <TableCell>
                          <Badge variant={e.type === 'Warning' ? 'destructive' : 'outline'}>
                            {e.type === 'Warning' && <CircleAlertIcon data-icon="inline-start" aria-hidden="true" />}
                            {e.type}
                          </Badge>
                        </TableCell>
                        <TableCell className="max-md:hidden">{`${e.objectKind}/${e.objectName}`}</TableCell>
                        <TableCell className="max-md:hidden">{e.reason}</TableCell>
                        <TableCell className="min-w-64 whitespace-normal break-words max-md:hidden">{e.message}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              ))}
          </CardContent>
        </Card>
      </div>
    </>
  )
}
