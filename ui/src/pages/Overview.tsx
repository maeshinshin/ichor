import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { InfoIcon, PlusIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConditionList } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { Field, Fields, PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StartOperationDialog } from '@/components/StartOperationDialog'
import { StatusText } from '@/components/Status'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { blockingOperation, currentOperation, recentOperations, rebootPendingCount, type OpRow } from '@/lib/clusterOps'
import { machineClient, operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { formatTimestamp } from '@/lib/format'
import { kubernetesStageProgress, operationState, progressCounts } from '@/lib/operations'
import { clusterHealth, type Health } from '@/lib/clusterHealth'
import { attentionItems, machineCounts } from '@/lib/overview'
import { opStateTone, type Tone } from '@/lib/status'
import { useCluster } from '@/lib/useCluster'
import { useT } from '@/lib/useT'
import { Link } from '@/router'

type Row = OpRow & { op: Operation }
type ErrorShape = { title: string; detail: string }

const toRow = (op: Operation): Row => ({
  op,
  name: op.ref?.name ?? '',
  phase: op.phase,
  failureReason: op.failureReason,
  isNode: op.kind === OperationKind.NODE,
  created: Number(op.createdAt?.seconds ?? 0n),
})

const statusTone: Record<Health, Tone> = { ready: 'good', degraded: 'bad', upgrading: 'progress' }

const readyReason = (m: Machine) => m.conditions.find((c) => c.type === 'Ready')?.reason ?? ''

// Overview is the home of the one cluster Ichor manages: where it stands, what
// needs a person, the versions with their upgrades, quick actions, the latest
// operations, the details, and the dangerous actions last. It only reads; every
// change goes through the dialogs, which ask the server for a plan first.
export function Overview() {
  const t = useT()
  const { cluster, operations, opRunning, opsError, loading, error, reload } = useCluster()
  if (error) return <ErrorAlert {...error} />
  if (loading) return <PageSkeleton label={t('cluster.loading')} />
  if (!cluster?.ref) return <EmptyNote>{t('home.noCluster')}</EmptyNote>
  return <Home cluster={cluster} operations={operations} opRunning={opRunning} opsError={opsError} reloadCluster={reload} />
}

function Home({
  cluster,
  operations,
  opRunning,
  opsError,
  reloadCluster,
}: {
  cluster: Cluster
  operations: Operation[]
  opRunning: boolean
  opsError: ErrorShape | null
  reloadCluster: () => void
}) {
  const t = useT()
  const ref = cluster.ref!
  const ops = operations.map(toRow)
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const [ichorVersion, setIchorVersion] = useState<string | null>(null)
  const wasBusy = useRef(false)

  async function reloadMachines() {
    try {
      setMachines((await machineClient.listMachines({ cluster: ref })).machines)
    } catch {
      setMachines([])
    }
  }

  async function loadIchor() {
    try {
      setIchorVersion((await operationClient.planOperation({ cluster: ref, kind: OperationKind.ICHOR, target: '' })).current)
    } catch {
      setIchorVersion(null)
    }
  }

  // The operations come from the shared cluster poll; a visit refreshes them at once.
  useEffect(() => {
    reloadCluster()
    reloadMachines()
    loadIchor()
  }, [ref.namespace, ref.name])

  const current = currentOperation(ops)
  const busy = blockingOperation(ops)
  // When an operation of this cluster finishes, read the versions and nodes again.
  useEffect(() => {
    if (busy) {
      wasBusy.current = true
    } else if (wasBusy.current) {
      wasBusy.current = false
      reloadCluster()
      reloadMachines()
      loadIchor()
    }
  }, [busy !== null])

  const approved = (machines ?? []).filter((m) => m.approvalState === MachineApprovalState.APPROVED)
  const pending = (machines ?? []).filter((m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED)
  const counts = machineCounts(approved.map((m) => ({ approval: 'approved' as const, ready: m.conditions.find((c) => c.type === 'Ready')?.status ?? '' })))
  const waiting = approved.filter((m) => rebootPendingCount([{ rebootPaths: m.rebootPaths, readyReason: readyReason(m) }]) > 0)
  const currentState = current ? operationState(current.op.phase, current.op.failureReason) : null
  const status = clusterHealth(cluster.conditions, !!cluster.rollout, opRunning)
  const busyReason = busy ? t('ops.disabledBusy') : undefined
  const kubeProxy = cluster.kubeProxyMode || 'enabled'
  const items = attentionItems({ conditions: cluster.conditions, pending: pending.length, configWaiting: waiting.length, opState: currentState })
  const recent = recentOperations(ops, 5)

  return (
    <>
      <PageHeader
        title={ref.name}
        kind={t('kind.cluster')}
        badges={<StatusText tone={statusTone[status]}>{t(`home.status.${status}`)}</StatusText>}
        meta={[
          { label: t('home.endpoint'), value: cluster.endpoint },
          {
            label: t('cluster.cpCount'),
            value: `${cluster.controlPlaneCount}${cluster.controlPlaneSchedulable ? ` ${t('clusters.schedulable')}` : ''}`,
          },
        ]}
      />
      {opsError && <ErrorAlert {...opsError} />}
      <div class="space-y-6">
        <div class="grid gap-4 md:grid-cols-3">
          <Card>
            <CardHeader>
              <CardDescription>{t('home.statusCard')}</CardDescription>
              <CardTitle>
                <StatusText tone={statusTone[status]}>{t(`home.status.${status}`)}</StatusText>
                {cluster.rollout && (
                  <p class="text-sm font-normal text-muted-foreground">
                    {enumLabel(RolloutKind, cluster.rollout.kind)} {cluster.rollout.nodesDone}/{cluster.rollout.nodesTotal}
                  </p>
                )}
              </CardTitle>
            </CardHeader>
          </Card>
          <Card>
            <CardHeader>
              <CardDescription>{t('home.nodesCard')}</CardDescription>
              <CardTitle>
                <Link to="/nodes" class="hover:underline">
                  {machines ? t('home.nodesReady', { ready: counts.ready, total: approved.length }) : '-'}
                </Link>
              </CardTitle>
            </CardHeader>
          </Card>
          <Card>
            <CardHeader>
              <CardDescription>{t('home.activeCard')}</CardDescription>
              <CardTitle>
                {current ? (
                  <ActiveOperation row={current} total={approved.length} />
                ) : (
                  <span class="text-base font-normal text-muted-foreground">{t('home.noActive')}</span>
                )}
              </CardTitle>
            </CardHeader>
          </Card>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>{t('overview.attention')}</CardTitle>
          </CardHeader>
          <CardContent>
            {items.length === 0 ? (
              <p class="text-muted-foreground">{t('overview.attentionEmpty')}</p>
            ) : (
              <ul class="divide-y">
                {items.map((a) => (
                  <li key={a.kind} class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 py-2 first:pt-0 last:pb-0">
                    {a.kind === 'certs' && (
                      <>
                        <StatusText tone="warn">{t('cluster.certsExpiringTitle')}</StatusText>
                        <span class="min-w-0 break-words text-muted-foreground">{a.message}</span>
                      </>
                    )}
                    {a.kind === 'backupStale' && (
                      <>
                        <StatusText tone="warn">{t('home.attnBackupStale')}</StatusText>
                        <span class="min-w-0 break-words text-muted-foreground">{a.message}</span>
                      </>
                    )}
                    {a.kind === 'pending' && (
                      <>
                        <StatusText tone="warn">{t('home.attnPending', { n: a.count })}</StatusText>
                        <Link to="/nodes" class="font-medium hover:underline">
                          {t('home.review')}
                        </Link>
                      </>
                    )}
                    {a.kind === 'config' && (
                      <>
                        <StatusText tone="warn">{t('home.attnConfig', { n: a.count })}</StatusText>
                        <StartOperationDialog kind="config" cluster={ref} label={t('ops.action.apply')} disabled={!!busy} />
                      </>
                    )}
                    {(a.kind === 'opFailed' || a.kind === 'opPaused') && current && (
                      <>
                        <StatusText tone={a.kind === 'opFailed' ? 'bad' : 'warn'}>{t(a.kind === 'opFailed' ? 'home.attnOpFailed' : 'home.attnOpPaused')}</StatusText>
                        <div class="flex flex-wrap items-center gap-3">
                          {current.op.ref && (
                            <Link to={`/operations/${current.op.ref.name}`} class="font-medium hover:underline">
                              {t('home.open')}
                            </Link>
                          )}
                          <OperationControls op={current.op} onChanged={reloadCluster} onDeleted={reloadCluster} showDelete={false} />
                        </div>
                      </>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('home.versions')}</CardTitle>
            <CardDescription>{t('home.versionsHint')}</CardDescription>
          </CardHeader>
          <CardContent className="divide-y px-0 [&>*]:px-(--card-spacing)">
            <Action title={t('ops.row.talos')} detail={t('ops.installed', { version: cluster.talosVersion })} reason={busyReason}>
              <StartOperationDialog kind="talos" cluster={ref} current={cluster.talosVersion} label={t('ops.action.upgrade')} disabled={!!busy} />
            </Action>
            <Action title={t('ops.row.kubernetes')} detail={t('ops.installed', { version: cluster.kubernetesVersion })} reason={busyReason}>
              <StartOperationDialog kind="kubernetes" cluster={ref} current={cluster.kubernetesVersion} label={t('ops.action.upgrade')} disabled={!!busy} />
            </Action>
            <Action
              title={t('ops.row.ichor')}
              detail={ichorVersion ? t('ops.installed', { version: ichorVersion }) : t('ops.installedUnknown')}
              reason={busyReason}
            >
              <StartOperationDialog kind="ichor" cluster={ref} current={ichorVersion ?? ''} label={t('ops.action.upgrade')} disabled={!!busy} />
            </Action>
            <Action title={t('ops.row.cni')} detail={t(kubeProxy === 'replaced' ? 'ops.modeReplaced' : 'ops.modeEnabled')} reason={busyReason}>
              <StartOperationDialog kind="cni" cluster={ref} current={kubeProxy} label={t('ops.action.switch')} disabled={!!busy} />
            </Action>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('home.quickActions')}</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-wrap items-center gap-2">
            <Button asChild size="sm">
              <Link to="/nodes/add">
                <PlusIcon />
                {t('cluster.addNode')}
              </Link>
            </Button>
            <StartOperationDialog
              kind="config"
              cluster={ref}
              label={t('home.applyConfig')}
              disabled={!!busy || machines === null || waiting.length === 0}
            />
            {machines !== null && waiting.length === 0 && <span class="text-muted-foreground">{t('ops.disabledNothing')}</span>}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>{t('home.recent')}</CardTitle>
            <CardDescription>
              <Link to="/operations" class="hover:underline">
                {t('overview.viewAll')}
              </Link>
            </CardDescription>
          </CardHeader>
          <CardContent>
            {recent.length === 0 ? (
              <p class="text-muted-foreground">{t('ops.recentNone')}</p>
            ) : (
              <ul class="divide-y rounded-md border">
                {recent.map(({ op }) => {
                  const state = operationState(op.phase, op.failureReason)
                  return (
                    <li key={op.ref?.name} class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-3 py-2">
                      <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                        <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
                        {op.ref ? (
                          <Link to={`/operations/${op.ref.name}`} class="font-medium hover:underline">
                            {enumLabel(OperationKind, op.kind)}
                          </Link>
                        ) : (
                          enumLabel(OperationKind, op.kind)
                        )}
                      </div>
                      <span class="text-muted-foreground">{formatTimestamp(op.createdAt)}</span>
                    </li>
                  )
                })}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardContent>
            <details>
              <summary class="cursor-pointer font-medium">
                {t('home.details')} <span class="font-normal text-muted-foreground">{t('home.detailsHint')}</span>
              </summary>
              <div class="mt-4 space-y-6">
                <section class="space-y-2">
                  <h3 class="font-medium">{t('cluster.conditions')}</h3>
                  <ConditionList conditions={cluster.conditions} />
                </section>
                <section class="space-y-2">
                  <h3 class="font-medium">{t('cluster.rollout')}</h3>
                  {cluster.rollout ? (
                    <Fields>
                      <Field label={t('cluster.rolloutKind')} value={enumLabel(RolloutKind, cluster.rollout.kind)} />
                      <Field label={t('cluster.rolloutTarget')} value={cluster.rollout.targetVersion} />
                      <Field label={t('cluster.rolloutPaused')} value={cluster.rollout.paused ? t('common.yes') : t('common.no')} />
                      <Field label={t('cluster.rolloutProgress')} value={`${cluster.rollout.nodesDone}/${cluster.rollout.nodesTotal}`} />
                      <Field label={t('cluster.rolloutPhase')} value={cluster.rollout.currentPhase} />
                    </Fields>
                  ) : (
                    <EmptyNote>{t('cluster.noRollout')}</EmptyNote>
                  )}
                </section>
                <section class="space-y-2">
                  <h3 class="font-medium">{t('cluster.handover')}</h3>
                  {cluster.handovers.length === 0 ? (
                    <EmptyNote>{t('cluster.noHandover')}</EmptyNote>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>{t('cluster.colComponent')}</TableHead>
                          <TableHead>{t('cluster.colPhase')}</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {cluster.handovers.map((h) => (
                          <TableRow key={h.component}>
                            <TableCell className="font-medium">{h.component}</TableCell>
                            <TableCell>
                              <Badge variant="outline">{enumLabel(HandoverPhase, h.phase)}</Badge>
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </section>
              </div>
            </details>
          </CardContent>
        </Card>

        <Card className="ring-destructive/40">
          <CardHeader>
            <CardTitle className="text-destructive">{t('ops.group.danger')}</CardTitle>
            <CardDescription>{t('ops.group.dangerHint')}</CardDescription>
          </CardHeader>
          <CardContent className="divide-y px-0 [&>*]:px-(--card-spacing)">
            <Action title={t('ops.row.caRotation')} hint={t('ops.row.caRotationHint')} detail={<CertificateLines cluster={cluster} />} reason={busyReason}>
              <StartOperationDialog kind="ca-rotation" cluster={ref} label={t('ops.action.rotate')} disabled={!!busy} />
            </Action>
            <Action title={t('ops.row.shutdown')} hint={t('ops.row.shutdownHint')} reason={busyReason}>
              <StartOperationDialog kind="shutdown" cluster={ref} label={t('ops.action.shutdown')} disabled={!!busy} />
            </Action>
          </CardContent>
        </Card>
      </div>
    </>
  )
}

// ActiveOperation is the running operation's kind, its progress and a link to it.
function ActiveOperation({ row, total }: { row: Row; total: number }) {
  const t = useT()
  const op = row.op
  const { done } = progressCounts(op.machines)
  const kubeStage = kubernetesStageProgress(op.kubernetesStage)
  const tracksNodes =
    op.kind !== OperationKind.ICHOR && op.kind !== OperationKind.CONFIG && op.kind !== OperationKind.NODE && op.kind !== OperationKind.ETCD_RESTORE && total > 0
  const label = enumLabel(OperationKind, op.kind)
  return (
    <div class="space-y-1 text-base">
      {op.ref ? (
        <Link to={`/operations/${op.ref.name}`} class="font-medium hover:underline">
          {label}
        </Link>
      ) : (
        label
      )}
      <p class="text-sm font-normal text-muted-foreground">
        {tracksNodes ? t('ops.currentNodes', { done, total }) : t(`ops.state.${operationState(op.phase, op.failureReason)}`)}
        {kubeStage && ` · ${t('ops.kubeStage', { stage: t(`ops.kubeStageName.${kubeStage.name}`), n: kubeStage.index, total: kubeStage.total })}`}
      </p>
    </div>
  )
}

// Action is one row: what it is, what it is like now, and its button. When the
// button is off, the reason is written under it, not left to a server refusal.
function Action({
  title,
  hint,
  detail,
  reason,
  children,
}: {
  title: string
  hint?: string
  detail?: ComponentChildren
  reason?: string
  children: ComponentChildren
}) {
  return (
    <div class="flex flex-col gap-3 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
      <div class="min-w-0 space-y-1">
        <p class="font-medium">{title}</p>
        {hint && <p class="text-muted-foreground">{hint}</p>}
        {detail && <div class="text-foreground">{detail}</div>}
        {reason && (
          <p class="flex items-center gap-1.5 text-muted-foreground">
            <InfoIcon class="size-4 shrink-0" aria-hidden="true" />
            {reason}
          </p>
        )}
      </div>
      <div class="shrink-0">{children}</div>
    </div>
  )
}

// CertificateLines lists each CA with its expiry date.
function CertificateLines({ cluster }: { cluster: Cluster }) {
  const t = useT()
  if (cluster.certificates.length === 0) return <span class="text-muted-foreground">{t('ops.caNone')}</span>
  return (
    <ul class="space-y-0.5">
      {cluster.certificates.map((c) => {
        const at = Number(c.notAfter?.seconds ?? 0n)
        const date = new Intl.DateTimeFormat('en', { dateStyle: 'medium' }).format(new Date(at * 1000))
        return <li key={c.name}>{t('ops.caLine', { name: c.name, date })}</li>
      })}
    </ul>
  )
}
