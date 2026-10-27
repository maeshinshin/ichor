import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section } from '@/components/SectionNav'
import { StatusText } from '@/components/Status'
import { OperationControls } from '@/components/OperationControls'
import { CARotationCredentials } from '@/components/CARotationCredentials'
import { timestampDate, type Timestamp } from '@bufbuild/protobuf/wkt'
import { NodeAction, OperationKind, type Operation, type OperationEvent } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { credentialsPanel } from '@/lib/credentials'
import { describeError } from '@/lib/errors'
import { restoreStepProgress } from '@/lib/etcdRestore'
import { formatTimestamp } from '@/lib/format'
import { formatDuration, nodePath, spanMs, summaryFallback, visibleEvents } from '@/lib/operationDetail'
import { caStageProgress, kubernetesStageProgress, operationState, pollIntervalMs, progressCounts } from '@/lib/operations'
import { opStateTone } from '@/lib/status'
import { useCluster } from '@/lib/useCluster'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { useT } from '@/lib/useT'
import { Link, navigate } from '@/router'

const toDate = (ts: Timestamp | undefined): Date | undefined => (ts ? timestampDate(ts) : undefined)

type ErrorShape ={ title: string; detail: string }

// Progress view of one operation (architecture.md, "Upgrades": the three
// upgrade kinds share a progress view): the phase and each machine's state
// exactly as the controller recorded them in status. A machine that does not
// appear has not been reached. It polls while the operation can still change.
export function OperationDetail({ name }: { name: string }) {
  const t = useT()
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
  const position = op.caRotationStage === '' ? null : caStageProgress(op.caRotationStage)
  const stage = position ? { name: op.caRotationStage, ...position } : null
  const kubeStage = kubernetesStageProgress(op.kubernetesStage)
  const restoreStep = restoreStepProgress(op.etcdRestoreStep)
  const panel = credentialsPanel(op.kind === OperationKind.CA_ROTATION, op.caRotationStage, state === 'done')

  return (
    <>
      <PageHeader
        title={title}
        kind={`${t('kind.operation')} ${op.ref?.name ?? name}`}
        badges={
          <>
            <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
            {op.machines.length > 0 && <span class="text-sm text-muted-foreground">{t('ops.progress', { done, running })}</span>}
            {stage && <Badge variant="outline">{t('ops.caStage', { stage: stage.name, n: stage.index, total: stage.total })}</Badge>}
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
        meta={[
          { label: t('ops.colKind'), value: enumLabel(OperationKind, op.kind) },
          { label: t('ops.startedBy'), value: op.startedBy || '-' },
          { label: t('ops.startedAt'), value: formatTimestamp(op.createdAt) },
          { label: t('ops.finishedAt'), value: formatTimestamp(op.finishedAt) },
          { label: t('ops.duration'), value: formatDuration(spanMs(toDate(op.createdAt), toDate(op.finishedAt), now, state === 'done' || state === 'shutdown')) },
          ...(op.kind === OperationKind.NODE
            ? [
                { label: t('node.action.actionLabel'), value: enumLabel(NodeAction, op.nodeAction) },
                {
                  label: t('node.action.nodeLabel'),
                  value: op.nodeMachine ? (
                    <Link to={`/nodes/${op.nodeMachine.name}`} class="hover:underline">
                      {op.nodeMachine.name}
                    </Link>
                  ) : undefined,
                },
                { label: t('node.action.progress'), value: op.nodeMessage },
              ]
            : op.kind === OperationKind.ETCD_RESTORE
              ? [
                  { label: t('restore.snapshotLabel'), value: op.etcdRestoreSnapshot },
                  { label: t('restore.snapshotTaken'), value: formatTimestamp(op.etcdRestoreSnapshotTime) },
                  { label: t('restore.backupLabel'), value: op.etcdRestoreBackup?.name },
                  { label: t('restore.progress'), value: op.etcdRestoreMessage },
                ]
              : [{ label: t('ops.maxUnavailable'), value: String(op.maxUnavailable || 1) }]),
        ]}
        actions={<OperationControls op={op} onChanged={setOp} onDeleted={() => navigate('/operations')} />}
      />
      <div class="space-y-8">
        <div class="space-y-4 empty:hidden">
          {panel === 'gate' && (
            <Alert variant="destructive">
              <AlertTitle>{t('ops.awaitingCredentialsTitle')}</AlertTitle>
              <AlertDescription>
                {t('ops.awaitingCredentials', { name: op.cluster?.name ?? '' })}
                <CARotationCredentials op={op} panel={panel} permitted={perms?.clusterCredentials} onChanged={setOp} />
              </AlertDescription>
            </Alert>
          )}
          {panel === 'issued' && (
            <Alert>
              <AlertTitle>{t('creds.issuedTitle')}</AlertTitle>
              <AlertDescription>
                {t('creds.issued')}
                <CARotationCredentials op={op} panel={panel} permitted={perms?.clusterCredentials} onChanged={setOp} />
              </AlertDescription>
            </Alert>
          )}
          {op.kind === OperationKind.CA_ROTATION && state === 'done' && (
            <Alert>
              <AlertTitle>{t('ops.plan.bundleStaleTitle')}</AlertTitle>
              <AlertDescription>{t('ops.plan.bundleStale', { name: op.cluster?.name ?? '' })}</AlertDescription>
            </Alert>
          )}
          {state === 'failed' && (
            <Alert variant="destructive">
              <AlertTitle>{t('ops.failure')}</AlertTitle>
              <AlertDescription>
                {op.failureReason}
                <br />
                {t('ops.failureHint')}
              </AlertDescription>
            </Alert>
          )}
          {error && <ErrorAlert {...error} />}
        </div>

        <Section id="what" title={t('ops.whatItDoes')}>
          <Card>
            <CardHeader>
              <CardTitle>{title}</CardTitle>
            </CardHeader>
            <CardContent>
              <dl class="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-1 text-sm">
                {op.details.map((d) => (
                  <div key={d.key} class="contents">
                    <dt class="text-muted-foreground">{d.key}</dt>
                    <dd class="min-w-0 break-words font-mono text-xs leading-5">{d.value}</dd>
                  </div>
                ))}
              </dl>
            </CardContent>
          </Card>
        </Section>

        <Section id="machines" title={t('ops.machines')}>
          {op.machines.length === 0 ? (
            <EmptyNote>{t(op.kind === OperationKind.ICHOR ? 'ops.ichorJob' : 'ops.noMachines')}</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.name')}</TableHead>
                  <TableHead>{t('ops.colState')}</TableHead>
                  <TableHead>{t('ops.colStep')}</TableHead>
                  <TableHead>{t('ops.startedAt')}</TableHead>
                  <TableHead>{t('ops.finishedAt')}</TableHead>
                  <TableHead>{t('ops.duration')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {op.machines.map((m) => (
                  <TableRow key={m.name}>
                    <TableCell>
                      <Link to={nodePath(m.name)} class="hover:underline">
                        {m.displayName || m.name}
                      </Link>
                    </TableCell>
                    <TableCell>
                      {/* The controller's own word (InProgress, Completed), as received. */}
                      <StatusText tone={m.phase === 'Completed' ? 'good' : m.phase === 'InProgress' ? 'progress' : 'neutral'}>{m.phase}</StatusText>
                    </TableCell>
                    <TableCell class="max-w-[28rem] whitespace-normal break-words font-mono text-xs">{[m.step, m.message].filter(Boolean).join(': ') || '-'}</TableCell>
                    <TableCell>{formatTimestamp(m.startedAt)}</TableCell>
                    <TableCell>{formatTimestamp(m.finishedAt)}</TableCell>
                    <TableCell>{formatDuration(spanMs(toDate(m.startedAt), toDate(m.finishedAt), now, m.phase === 'Completed'))}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Section>

        <Section id="activity" title={t('ops.activity')}>
          <div class="mb-3 flex items-center gap-2">
            <Checkbox id="op-warnings-only" checked={warningsOnly} onCheckedChange={(on: boolean | 'indeterminate') => setWarningsOnly(on === true)} />
            <Label htmlFor="op-warnings-only">{t('ops.warningsOnly')}</Label>
          </div>
          {eventsError && <ErrorAlert {...eventsError} />}
          {events !== null &&
            (shownEvents.length === 0 ? (
              <EmptyNote>{t('ops.noEvents')}</EmptyNote>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('ops.eventTime')}</TableHead>
                    <TableHead>{t('ops.eventType')}</TableHead>
                    <TableHead>{t('ops.eventObject')}</TableHead>
                    <TableHead>{t('ops.eventReason')}</TableHead>
                    <TableHead>{t('ops.eventMessage')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {shownEvents.map((e, i) => (
                    <TableRow key={i}>
                      <TableCell class="whitespace-nowrap">{formatTimestamp(e.time)}</TableCell>
                      <TableCell>
                        <Badge variant={e.type === 'Warning' ? 'destructive' : 'outline'}>{e.type}</Badge>
                      </TableCell>
                      <TableCell class="whitespace-nowrap">{`${e.objectKind}/${e.objectName}`}</TableCell>
                      <TableCell>{e.reason}</TableCell>
                      <TableCell class="whitespace-normal break-words font-mono text-xs">{e.message}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ))}
        </Section>
      </div>
    </>
  )
}
