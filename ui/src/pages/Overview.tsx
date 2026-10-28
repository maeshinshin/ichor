import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { CircleXIcon, EllipsisIcon, PlusIcon, TriangleAlertIcon } from 'lucide-preact'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { BackupNow } from '@/components/BackupNow'
import { ConditionList } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { Field, Fields } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StartOperationDialog } from '@/components/StartOperationDialog'
import { StatusText } from '@/components/Status'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { OperationKind, type EtcdBackup, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { blockingOperation, currentOperation, recentOperations, rebootPendingCount, type OpRow } from '@/lib/clusterOps'
import { machineClient, operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { machineDisplayName } from '@/lib/machineName'
import { formatTimestamp } from '@/lib/format'
import { kubernetesStageProgress, operationState, progressCounts } from '@/lib/operations'
import { attentionItems, backupsValue, machineCounts, nodesValue, tileTargets, versionsValue, type TileTarget } from '@/lib/overview'
import { conditionStatusTone, opStateTone, type Tone } from '@/lib/status'
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

const readyOf = (m: Machine) => m.conditions.find((c) => c.type === 'Ready')

// Overview is the home of the one cluster Ichor manages: what needs a person, four numbers that
// say where it stands, the latest operations, the nodes, the versions with their upgrades, and the
// details last. The top bar's badge is the one place for health. It only reads; every change goes
// through the dialogs, which ask the server for a plan first.
export function Overview() {
  const t = useT()
  const { cluster, operations, opsError, loading, error, reload } = useCluster()
  if (error) return <ErrorAlert {...error} />
  if (loading) return <PageSkeleton label={t('cluster.loading')} />
  if (!cluster?.ref) return <EmptyNote>{t('home.noCluster')}</EmptyNote>
  return <Home cluster={cluster} operations={operations} opsError={opsError} reloadCluster={reload} />
}

function Home({
  cluster,
  operations,
  opsError,
  reloadCluster,
}: {
  cluster: Cluster
  operations: Operation[]
  opsError: ErrorShape | null
  reloadCluster: () => void
}) {
  const t = useT()
  const ref = cluster.ref!
  const ops = operations.map(toRow)
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const [backups, setBackups] = useState<EtcdBackup[] | null>(null)
  const [ichorVersion, setIchorVersion] = useState<string | null>(null)
  const [dialog, setDialog] = useState<'ca-rotation' | 'shutdown' | null>(null)
  const wasBusy = useRef(false)

  async function reloadMachines() {
    try {
      setMachines((await machineClient.listMachines({ cluster: ref })).machines)
    } catch {
      setMachines([])
    }
  }

  async function loadBackups() {
    try {
      setBackups((await operationClient.listEtcdBackups({ cluster: ref })).backups)
    } catch {
      setBackups(null)
    }
  }

  async function loadIchor() {
    try {
      setIchorVersion((await operationClient.planOperation({ cluster: ref, kind: OperationKind.ICHOR, target: '' })).current)
    } catch {
      setIchorVersion(null)
    }
  }

  function reloadAll() {
    reloadCluster()
    reloadMachines()
    loadBackups()
    loadIchor()
  }

  // The operations come from the shared cluster poll; a visit refreshes them at once.
  useEffect(reloadAll, [ref.namespace, ref.name])

  const current = currentOperation(ops)
  const busy = blockingOperation(ops)
  // When an operation of this cluster finishes, read the versions, nodes and backups again.
  useEffect(() => {
    if (busy) {
      wasBusy.current = true
    } else if (wasBusy.current) {
      wasBusy.current = false
      reloadAll()
    }
  }, [busy !== null])

  const approved = (machines ?? []).filter((m) => m.approvalState === MachineApprovalState.APPROVED)
  const pending = (machines ?? []).filter((m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED)
  const counts = machineCounts(approved.map((m) => ({ approval: 'approved' as const, ready: readyOf(m)?.status ?? '' })))
  const waiting = approved.filter((m) => rebootPendingCount([{ rebootPaths: m.rebootPaths, readyReason: readyOf(m)?.reason ?? '' }]) > 0)
  const currentState = current ? operationState(current.op.phase, current.op.failureReason) : null
  const busyReason = busy ? t('ops.disabledBusy') : undefined
  const kubeProxy = cluster.kubeProxyMode || 'enabled'
  const items = attentionItems({ conditions: cluster.conditions, pending: pending.length, configWaiting: waiting.length, opState: currentState })
  const recent = recentOperations(ops, 5)
  const targets = tileTargets(current?.op.ref?.name ?? null)
  const nothingWaits = machines !== null && waiting.length === 0
  const applyDisabledReason = busyReason ?? (nothingWaits ? t('ops.disabledNothing') : undefined)

  return (
    <TooltipProvider>
      <header class="mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="min-w-0">
          <h1 class="text-xl font-semibold tracking-tight">{t('overview.title')}</h1>
          <p class="break-words text-sm text-muted-foreground">{ref.name}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Button asChild size="sm">
            <Link to="/nodes/add">
              <PlusIcon />
              {t('cluster.addNode')}
            </Link>
          </Button>
          {backups && backups.length > 0 && (
            <BackupNow backup={backups[0]} onChanged={(next) => setBackups(backups.map((x) => (x.ref?.name === next.ref?.name ? next : x)))} />
          )}
          {applyDisabledReason || machines === null ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <span tabIndex={0} class="inline-flex">
                  <StartOperationDialog kind="config" cluster={ref} label={t('home.applyConfig')} disabled />
                </span>
              </TooltipTrigger>
              {applyDisabledReason && <TooltipContent>{applyDisabledReason}</TooltipContent>}
            </Tooltip>
          ) : (
            <StartOperationDialog kind="config" cluster={ref} label={t('home.applyConfig')} />
          )}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button size="icon-sm" variant="outline" aria-label={t('overview.more')}>
                <EllipsisIcon />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem disabled={!!busy} onSelect={() => setDialog('ca-rotation')}>
                {t('overview.rotateCAs')}
              </DropdownMenuItem>
              <DropdownMenuItem variant="destructive" disabled={!!busy} onSelect={() => setDialog('shutdown')}>
                {t('overview.shutdownCluster')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <StartOperationDialog kind="ca-rotation" cluster={ref} open={dialog === 'ca-rotation'} onOpenChange={(o) => !o && setDialog(null)} />
          <StartOperationDialog kind="shutdown" cluster={ref} open={dialog === 'shutdown'} onOpenChange={(o) => !o && setDialog(null)} />
        </div>
      </header>
      {opsError && <ErrorAlert {...opsError} />}
      <div class="space-y-4">
        {items.length > 0 && (
          <ul class="space-y-2">
            {items.map((a) => {
              const bad = a.kind === 'opFailed'
              return (
                <li key={a.kind}>
                  <Alert variant={bad ? 'destructive' : 'default'} className={bad ? undefined : 'border-amber-500/50'}>
                    {bad ? <CircleXIcon /> : <TriangleAlertIcon class="text-amber-600 dark:text-amber-400" />}
                    <div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
                      {(a.kind === 'certs' || a.kind === 'backupStale') && (
                        <>
                          <span class="min-w-0 break-words font-medium">{t(a.kind === 'certs' ? 'cluster.certsExpiringTitle' : 'home.attnBackupStale')}</span>
                          <span class="min-w-0 break-words text-muted-foreground">{a.message}</span>
                        </>
                      )}
                      {a.kind === 'pending' && (
                        <>
                          <span class="font-medium">{t('home.attnPending', { n: a.count })}</span>
                          <Link to="/nodes" class="font-medium underline-offset-4 hover:underline">
                            {t('home.review')}
                          </Link>
                        </>
                      )}
                      {a.kind === 'config' && (
                        <>
                          <span class="font-medium">{t('home.attnConfig', { n: a.count })}</span>
                          <StartOperationDialog kind="config" cluster={ref} label={t('ops.action.apply')} disabled={!!busy} />
                        </>
                      )}
                      {(a.kind === 'opFailed' || a.kind === 'opPaused') && current && (
                        <>
                          <span class="font-medium">{t(a.kind === 'opFailed' ? 'home.attnOpFailed' : 'home.attnOpPaused')}</span>
                          <div class="flex flex-wrap items-center gap-3">
                            {current.op.ref && (
                              <Link to={`/operations/${current.op.ref.name}`} class="font-medium underline-offset-4 hover:underline">
                                {t('home.open')}
                              </Link>
                            )}
                            <OperationControls op={current.op} onChanged={reloadCluster} onDeleted={reloadCluster} showDelete={false} />
                          </div>
                        </>
                      )}
                    </div>
                  </Alert>
                </li>
              )
            })}
          </ul>
        )}

        <div class="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Tile
            label={t('overview.tile.nodes')}
            target={targets.nodes}
            tone={machines === null ? 'neutral' : counts.ready === approved.length ? 'good' : 'warn'}
            value={machines ? nodesValue(counts.ready, approved.length) : '-'}
            sub={pending.length > 0 ? t('overview.waitingApproval', { n: pending.length }) : undefined}
          />
          <Tile
            label={t('overview.tile.operation')}
            target={targets.operation}
            tone={current ? opStateTone(currentState!) : 'neutral'}
            value={current ? enumLabel(OperationKind, current.op.kind) : t('overview.idle')}
            sub={current ? <ActiveProgress row={current} total={approved.length} /> : undefined}
          />
          <Tile label={t('overview.tile.backups')} target={targets.backups} tone="neutral" value={backupsValue(backups, Date.now())} />
          <Tile
            label={t('overview.tile.versions')}
            target={targets.versions}
            tone="neutral"
            value={versionsValue(cluster.talosVersion, cluster.kubernetesVersion)}
          />
        </div>

        <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>{t('home.recent')}</CardTitle>
              <CardAction>
                <Link to="/operations" class="text-sm text-muted-foreground underline-offset-4 hover:underline">
                  {t('overview.viewAll')}
                </Link>
              </CardAction>
            </CardHeader>
            <CardContent>
              {recent.length === 0 ? (
                <p class="text-muted-foreground">{t('ops.recentNone')}</p>
              ) : (
                <ul class="divide-y">
                  {recent.map(({ op }) => {
                    const state = operationState(op.phase, op.failureReason)
                    return (
                      <li key={op.ref?.name} class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2 first:pt-0 last:pb-0">
                        <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                          {op.ref ? (
                            <Link to={`/operations/${op.ref.name}`} class="font-medium hover:underline">
                              {enumLabel(OperationKind, op.kind)}
                            </Link>
                          ) : (
                            enumLabel(OperationKind, op.kind)
                          )}
                          <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
                        </div>
                        <span class="text-xs text-muted-foreground" title={t('overview.colStarted')}>
                          {formatTimestamp(op.createdAt)}
                        </span>
                      </li>
                    )
                  })}
                </ul>
              )}
            </CardContent>
          </Card>

          <div class="min-w-0 space-y-4">
            <Card>
              <CardHeader>
                <CardTitle>{t('overview.tile.nodes')}</CardTitle>
                {approved.length > 5 && (
                  <CardAction>
                    <Link to="/nodes" class="text-sm text-muted-foreground underline-offset-4 hover:underline">
                      {t('overview.viewAllNodes', { n: approved.length })}
                    </Link>
                  </CardAction>
                )}
              </CardHeader>
              <CardContent>
                {machines === null ? (
                  <p class="text-muted-foreground">-</p>
                ) : approved.length === 0 ? (
                  <p class="text-muted-foreground">{t('overview.nodesNone')}</p>
                ) : (
                  <ul class="divide-y">
                    {approved.slice(0, 5).map((m) => (
                      <NodeRow key={m.ref?.name} m={m} />
                    ))}
                  </ul>
                )}
              </CardContent>
            </Card>

            <Card id="versions">
              <CardHeader>
                <CardTitle>{t('home.versions')}</CardTitle>
              </CardHeader>
              <CardContent>
                <ul class="divide-y">
                    <VersionRow name={t('ops.row.talos')} installed={cluster.talosVersion}>
                      <StartOperationDialog kind="talos" cluster={ref} current={cluster.talosVersion} label={t('ops.action.upgrade')} disabled={!!busy} />
                    </VersionRow>
                    <VersionRow name={t('ops.row.kubernetes')} installed={cluster.kubernetesVersion}>
                      <StartOperationDialog kind="kubernetes" cluster={ref} current={cluster.kubernetesVersion} label={t('ops.action.upgrade')} disabled={!!busy} />
                    </VersionRow>
                    <VersionRow name={t('ops.row.ichor')} installed={ichorVersion ?? t('ops.installedUnknown')}>
                      <StartOperationDialog kind="ichor" cluster={ref} current={ichorVersion ?? ''} label={t('ops.action.upgrade')} disabled={!!busy} />
                    </VersionRow>
                    <VersionRow name={t('ops.row.cni')} installed={t(kubeProxy === 'replaced' ? 'ops.modeReplaced' : 'ops.modeEnabled')}>
                      <StartOperationDialog kind="cni" cluster={ref} current={kubeProxy} label={t('ops.action.switch')} disabled={!!busy} />
                    </VersionRow>
                </ul>
                {busyReason && <p class="mt-2 text-xs text-muted-foreground">{busyReason}</p>}
              </CardContent>
            </Card>
          </div>
        </div>

        <details class="rounded-lg border bg-card px-4 py-3">
          <summary class="cursor-pointer font-medium">{t('home.details')}</summary>
          <div class="mt-4 space-y-6">
            <Fields>
              <Field label={t('home.endpoint')} value={cluster.endpoint} />
              <Field
                label={t('cluster.cpCount')}
                value={`${cluster.controlPlaneCount}${cluster.controlPlaneSchedulable ? ` ${t('clusters.schedulable')}` : ''}`}
              />
            </Fields>
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
      </div>
    </TooltipProvider>
  )
}

const accent: Record<Tone, string> = {
  good: 'border-l-emerald-500',
  bad: 'border-l-red-500',
  warn: 'border-l-amber-500',
  progress: 'border-l-sky-500',
  neutral: 'border-l-border',
}

// Tile is one summary number: a small muted label, the value, and a subtle accent on the left edge.
// The whole tile leads somewhere: a page, or the versions table further down.
function Tile({ label, value, sub, tone, target }: { label: string; value: string; sub?: ComponentChildren; tone: Tone; target: TileTarget }) {
  const cls = `block min-w-0 rounded-lg border border-l-4 bg-card px-3 py-2.5 text-left transition-colors hover:bg-accent/50 ${accent[tone]}`
  const body = (
    <>
      <p class="text-xs text-muted-foreground">{label}</p>
      <p class="break-words text-base font-semibold leading-snug">{value}</p>
      {sub && <div class="text-xs text-muted-foreground">{sub}</div>}
    </>
  )
  if (target.kind === 'route') {
    return (
      <Link to={target.to} class={cls}>
        {body}
      </Link>
    )
  }
  return (
    <button type="button" class={`${cls} w-full`} onClick={() => document.getElementById(target.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}>
      {body}
    </button>
  )
}

function NodeRow({ m }: { m: Machine }) {
  const t = useT()
  const ready = readyOf(m)
  const tone: Tone = ready ? conditionStatusTone(ready.type, ready.status) : 'neutral'
  const word = !ready ? t('overview.nodeUnknown') : ready.status === 'True' ? t('overview.nodeReady') : t('overview.nodeNotReady')
  // The hostname, else the node name; the machine's UUID name is only the link target.
  const name = machineDisplayName(m)
  return (
    <li class="flex items-center justify-between gap-x-4 py-2 first:pt-0 last:pb-0">
      <div class="min-w-0">
        {m.ref ? (
          <Link to={`/nodes/${m.ref.name}`} class="block truncate font-medium hover:underline">
            {name}
          </Link>
        ) : (
          name
        )}
        <p class="text-xs text-muted-foreground">{enumLabel(MachineRole, m.role)}</p>
      </div>
      <StatusText tone={tone}>{word}</StatusText>
    </li>
  )
}

function VersionRow({ name, installed, children }: { name: string; installed: string; children: ComponentChildren }) {
  return (
    <li class="flex items-center justify-between gap-x-3 py-2 first:pt-0 last:pb-0">
      <div class="min-w-0">
        <p class="font-medium">{name}</p>
        <p class="text-sm text-muted-foreground [overflow-wrap:anywhere]">{installed || '-'}</p>
      </div>
      <div class="shrink-0">{children}</div>
    </li>
  )
}

// ActiveProgress is the running operation's progress: nodes done, or its state, and the Kubernetes stage.
function ActiveProgress({ row, total }: { row: Row; total: number }) {
  const t = useT()
  const op = row.op
  const { done } = progressCounts(op.machines)
  const kubeStage = kubernetesStageProgress(op.kubernetesStage)
  const tracksNodes =
    op.kind !== OperationKind.ICHOR && op.kind !== OperationKind.CONFIG && op.kind !== OperationKind.NODE && op.kind !== OperationKind.ETCD_RESTORE && total > 0
  return (
    <>
      {tracksNodes ? t('ops.currentNodes', { done, total }) : t(`ops.state.${operationState(op.phase, op.failureReason)}`)}
      {kubeStage && ` · ${t('ops.kubeStage', { stage: t(`ops.kubeStageName.${kubeStage.name}`), n: kubeStage.index, total: kubeStage.total })}`}
    </>
  )
}
