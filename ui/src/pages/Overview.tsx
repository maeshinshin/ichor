import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import {
  ArchiveRestoreIcon,
  ArrowRightIcon,
  ContainerIcon,
  ChevronsUpDownIcon,
  CircleAlertIcon,
  CloudCogIcon,
  CircleXIcon,
  EllipsisIcon,
  InboxIcon,
  NetworkIcon,
  PackageCheckIcon,
  PlusIcon,
  ServerCogIcon,
  ServerIcon,
  ShieldCheckIcon,
  CpuIcon,
  TriangleAlertIcon,
  WorkflowIcon,
  type LucideIcon,
} from 'lucide-preact'
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { ToneBadge } from '@/components/ToneBadge'
import { ReadyCell } from '@/components/ReadyCell'
import { Button } from '@/components/ui/button'
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Item, ItemActions, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemSeparator, ItemTitle } from '@/components/ui/item'
import { Progress } from '@/components/ui/progress'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { TooltipProvider } from '@/components/ui/tooltip'
import { BackupNow } from '@/components/BackupNow'
import { CARotationAdvice } from '@/components/CARotationAdvice'
import { isCniUnmanaged } from '@/lib/cni'
import { CiliumUpgradeDialog } from '@/components/CiliumUpgradeDialog'
import { ConditionList } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { Field, Fields } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ReaderTalosconfigDialog } from '@/components/ReaderTalosconfigDialog'
import { StartOperationDialog } from '@/components/StartOperationDialog'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { OperationKind, type EtcdBackup, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { apiRestartPendingCount, blockingOperation, recentOperations, rebootPendingCount, type OpRow } from '@/lib/clusterOps'
import { machineClient, operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import type { ErrorShape } from '@/lib/errors'
import { machineDisplayName } from '@/lib/machineName'
import { formatTimestamp } from '@/lib/format'
import { kubernetesStageProgress, operationState, progressCounts, type OpKind } from '@/lib/operations'
import { attentionItems, backupsValue, tileTargets, versionLabel, type TileTarget } from '@/lib/overview'
import { installerUnverified, releaseNotice } from '@/lib/ichorVersion'
import { opStateTone } from '@/lib/status'
import { componentOf, stateKey, stateTone, versionPair } from '@/lib/versions'
import { hasPatch, patchComponents, unavailableNotes, type TargetList } from '@/lib/upgradeTargets'
import { useCluster } from '@/lib/useCluster'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { clusterMenu, mayStart } from '@/lib/actions'
import { t } from '@/lib/i18n'
import { Link } from '@/router'

type Row = OpRow & { op: Operation }
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
  const { cluster, operations, opsError, loading, error, reload } = useCluster()
  if (error) return <ErrorAlert {...error} />
  if (loading) return <PageSkeleton label={t('cluster.loading')} />
  if (!cluster?.ref) return <EmptyNote icon={InboxIcon}>{t('home.noCluster')}</EmptyNote>
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
  const ref = cluster.ref!
  const ops = operations.map(toRow)
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const [backups, setBackups] = useState<EtcdBackup[] | null>(null)
  const [ichorVersion, setIchorVersion] = useState<string | null>(null)
  // The newer Ichor release the controller found, or null.
  const [release, setRelease] = useState<string | null>(null)
  // Automatic patching: whether it is on, and the automatic operation that halted it.
  const [autoPatch, setAutoPatch] = useState<{ enabled: boolean; halted: string }>({ enabled: false, halted: '' })
  // Whether the installer image's signature was not checked.
  const [unverified, setUnverified] = useState(false)
  // What Talos and Kubernetes can move to, for the patch hints; null when the list could not be read.
  const [candidates, setCandidates] = useState<{ talos: TargetList | null; kubernetes: TargetList | null }>({
    talos: null,
    kubernetes: null,
  })
  const [dialog, setDialog] = useState<'ca-rotation' | 'key-rotation' | 'shutdown' | 'reader-talosconfig' | null>(null)
  // The caller's "download" verb on talosclusters/talosconfig-reader; undefined offers nothing.
  const perms = useMyPermissions(undefined, ref)
  const wasBusy = useRef(false)
  // What the caller's role may do; the actions it cannot are not offered (the server checks again).
  const menu = clusterMenu(perms)
  const may = (kind: OpKind) => mayStart(perms, kind)

  async function loadCandidates() {
    const read = (kind: OperationKind) => operationClient.listUpgradeTargets({ cluster: ref, kind }).catch(() => null)
    const [talos, kubernetes] = await Promise.all([read(OperationKind.TALOS), read(OperationKind.KUBERNETES)])
    setCandidates({ talos, kubernetes })
  }

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

  async function loadRelease() {
    try {
      const info = await operationClient.getIchorInstallation({})
      setRelease(releaseNotice(info))
      setAutoPatch({ enabled: info.autoPatchEnabled, halted: info.autoPatchHaltedOperation })
      setUnverified(installerUnverified(info) !== null)
    } catch {
      setRelease(null)
      setUnverified(false)
      setAutoPatch({ enabled: false, halted: '' })
    }
  }

  function reloadAll() {
    reloadCluster()
    reloadMachines()
    loadBackups()
    loadIchor()
    loadRelease()
    loadCandidates()
  }

  // The operations come from the shared cluster poll; a visit refreshes them at once.
  useEffect(reloadAll, [ref.namespace, ref.name])

  const current = blockingOperation(ops)
  // When an operation of this cluster finishes, read the versions, nodes and backups again.
  useEffect(() => {
    if (current) {
      wasBusy.current = true
    } else if (wasBusy.current) {
      wasBusy.current = false
      reloadAll()
    }
  }, [current !== null])

  const approved = (machines ?? []).filter((m) => m.approvalState === MachineApprovalState.APPROVED)
  const pending = (machines ?? []).filter((m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED)
  const ready = approved.filter((m) => readyOf(m)?.status === 'True').length
  const waiting = approved.filter((m) => rebootPendingCount([{ rebootPaths: m.rebootPaths, readyReason: readyOf(m)?.reason ?? '' }]) > 0)
  const apiRestart = approved.filter((m) => apiRestartPendingCount([{ readyReason: readyOf(m)?.reason ?? '' }]) > 0)
  const currentState = current ? operationState(current.op.phase, current.op.failureReason) : null
  const kubeProxy = cluster.kubeProxyMode || 'enabled'
  const versionOf = (name: string) => componentOf(cluster.versions, name)
  const cilium = versionOf('cilium')
  const notes = unavailableNotes(candidates)
  const patches = patchComponents(candidates)
  const items = attentionItems({ conditions: cluster.conditions, pending: pending.length, configWaiting: waiting.length, apiRestartWaiting: apiRestart.length, opState: currentState, patches, release, autoPatchHalted: autoPatch.halted, installerUnverified: unverified })
  const recent = recentOperations(ops, 5)
  const targets = tileTargets(current?.op.ref?.name ?? null)

  return (
    <TooltipProvider>
      <header class="mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
        <div class="min-w-0">
          <h1 class="text-xl font-semibold tracking-tight">{t('overview.title')}</h1>
          <p class="break-words text-sm text-muted-foreground">{ref.name}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          {perms?.clusterUpdate && (
            <Button asChild size="sm">
              <Link to="/nodes/add">
                <PlusIcon data-icon="inline-start" aria-hidden="true" />
                {t('cluster.addNode')}
              </Link>
            </Button>
          )}
          {perms?.updateBackups && backups && backups.length > 0 && (
            <BackupNow backup={backups[0]} onChanged={(next) => setBackups(backups.map((x) => (x.ref?.name === next.ref?.name ? next : x)))} />
          )}
          {menu.length > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="icon-sm" variant="outline" aria-label={t('overview.more')}>
                  <EllipsisIcon />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-auto min-w-48">
                {menu.includes('ca-rotation') && (
                  <DropdownMenuItem onSelect={() => setDialog('ca-rotation')}>{t('overview.rotateCAs')}</DropdownMenuItem>
                )}
                {menu.includes('key-rotation') && (
                  <DropdownMenuItem onSelect={() => setDialog('key-rotation')}>{t('overview.rotateKeys')}</DropdownMenuItem>
                )}
                {menu.includes('reader-talosconfig') && (
                  <DropdownMenuItem onSelect={() => setDialog('reader-talosconfig')}>{t('readerTalosconfig.menu')}</DropdownMenuItem>
                )}
                {menu.includes('shutdown') && (
                  <DropdownMenuItem variant="destructive" onSelect={() => setDialog('shutdown')}>
                    {t('overview.shutdownCluster')}
                  </DropdownMenuItem>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <StartOperationDialog kind="ca-rotation" cluster={ref} open={dialog === 'ca-rotation'} onOpenChange={(o) => !o && setDialog(null)} />
          <StartOperationDialog kind="key-rotation" cluster={ref} open={dialog === 'key-rotation'} onOpenChange={(o) => !o && setDialog(null)} />
          <StartOperationDialog kind="shutdown" cluster={ref} open={dialog === 'shutdown'} onOpenChange={(o) => !o && setDialog(null)} />
          <ReaderTalosconfigDialog cluster={ref} open={dialog === 'reader-talosconfig'} onOpenChange={(o) => !o && setDialog(null)} />
        </div>
      </header>
      {opsError && <ErrorAlert {...opsError} />}
      <div class="space-y-4">
        {cluster.conditions.some((c) => c.type === 'CARotationAdvised' && c.status === 'True') && <CARotationAdvice cluster={ref} />}
        {items.length > 0 && (
          <ul class="space-y-2">
            {items.map((a) => {
              const bad = a.kind === 'opFailed'
              return (
                <li key={a.kind}>
                  <Alert variant={bad ? 'destructive' : 'default'}>
                    {bad ? <CircleXIcon /> : <TriangleAlertIcon />}
                      {(a.kind === 'certs' || a.kind === 'backupStale' || a.kind === 'clientsNotRestarted') && (
                        <>
                          <AlertTitle>{t(a.kind === 'certs' ? 'cluster.certsExpiringTitle' : a.kind === 'backupStale' ? 'home.attnBackupStale' : 'home.attnClientsNotRestarted')}</AlertTitle>
                          <AlertDescription className="break-words">{a.message}</AlertDescription>
                        </>
                      )}
                      {a.kind === 'pending' && (
                        <>
                          <AlertTitle>{t('home.attnPending', { n: a.count })}</AlertTitle>
                          <AlertAction>
                            <Button asChild variant="link" size="xs">
                              <Link to="/nodes">{t('home.review')}</Link>
                            </Button>
                          </AlertAction>
                        </>
                      )}
                      {(a.kind === 'config' || a.kind === 'apiRestart') && (
                        <>
                          <AlertTitle>{t(a.kind === 'config' ? 'home.attnConfig' : 'home.attnApiRestart', { n: a.count })}</AlertTitle>
                          {may('config') && (
                            <AlertAction>
                              <StartOperationDialog kind="config" cluster={ref} label={t('ops.action.apply')} size="xs" />
                            </AlertAction>
                          )}
                        </>
                      )}
                      {a.kind === 'patch' && <AlertTitle>{t('home.attnPatch', { components: a.components.join(', ') })}</AlertTitle>}
                      {a.kind === 'autoPatchHalted' && (
                        <>
                          <AlertTitle>{t('home.attnAutoPatchHalted')}</AlertTitle>
                          <AlertDescription>{t('home.attnAutoPatchHaltedHint', { operation: a.operation })}</AlertDescription>
                          <AlertAction>
                            <Button asChild variant="link" size="xs">
                              <Link to="/settings">{t('nav.settings')}</Link>
                            </Button>
                          </AlertAction>
                        </>
                      )}
                      {a.kind === 'installerUnverified' && (
                        <>
                          <AlertTitle>{t('home.attnInstallerUnverified')}</AlertTitle>
                          <AlertDescription>{t('home.attnInstallerUnverifiedHint')}</AlertDescription>
                        </>
                      )}
                      {a.kind === 'release' && (
                        <>
                          <AlertTitle>{t('home.attnRelease', { version: a.version })}</AlertTitle>
                          <AlertDescription>{t('home.attnReleaseHint')}</AlertDescription>
                        </>
                      )}
                      {(a.kind === 'opFailed' || a.kind === 'opPaused') && current && (
                        <>
                          <AlertTitle>{t(a.kind === 'opFailed' ? 'home.attnOpFailed' : 'home.attnOpPaused')}</AlertTitle>
                          <AlertAction className="flex items-center gap-1">
                            {current.op.ref && (
                              <Button asChild variant="link" size="xs">
                                <Link to={`/operations/${current.op.ref.name}`}>{t('home.open')}</Link>
                              </Button>
                            )}
                            {perms?.createOperations && (
                              <OperationControls op={current.op} onChanged={reloadCluster} onDeleted={reloadCluster} showDelete={false} />
                            )}
                          </AlertAction>
                        </>
                      )}
                  </Alert>
                </li>
              )
            })}
          </ul>
        )}

        <div class="grid auto-rows-fr grid-cols-2 gap-3 lg:grid-cols-4">
          <Tile
            label={t('overview.tile.nodes')}
            icon={ServerIcon}
            target={targets.nodes}
            badge={machines !== null && ready !== approved.length ? t('overview.nodeNotReady') : undefined}
            value={machines ? `${ready} / ${approved.length} ready` : '-'}
            sub={pending.length > 0 ? t('overview.waitingApproval', { n: pending.length }) : undefined}
          />
          <Tile
            label={t('overview.tile.operation')}
            icon={WorkflowIcon}
            target={targets.operation}
            badge={current && opStateTone(currentState!) === 'bad' ? t(`ops.state.${currentState!}`) : undefined}
            value={current ? enumLabel(OperationKind, current.op.kind) : t('overview.idle')}
            sub={current ? <ActiveProgress row={current} total={approved.length} /> : undefined}
          />
          <Tile label={t('overview.tile.backups')} icon={ArchiveRestoreIcon} target={targets.backups} value={backupsValue(backups, Date.now())} />
          <Tile
            label={t('overview.tile.versions')}
            icon={PackageCheckIcon}
            target={targets.versions}
            sub={
              <>
                <VersionLine label="Talos" version={cluster.talosVersion} />
                <VersionLine label="Kubernetes" version={cluster.kubernetesVersion} />
              </>
            }
          />
        </div>

        <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>{t('home.recent')}</CardTitle>
              <CardAction>
                <Button asChild variant="link" size="sm">
                  <Link to="/operations">{t('overview.viewAll')}
                  <ArrowRightIcon data-icon="inline-end" aria-hidden="true" />
                </Link>
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent>
              {recent.length === 0 ? (
                <EmptyNote icon={WorkflowIcon}>{t('ops.recentNone')}</EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('ops.colKind')}</TableHead>
                      <TableHead>{t('ops.colState')}</TableHead>
                      <TableHead>{t('overview.colStarted')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {recent.map(({ op }) => {
                      const state = operationState(op.phase, op.failureReason)
                      return (
                        <TableRow key={op.ref?.name}>
                          <TableCell>
                            {op.ref ? (
                              <Link to={`/operations/${op.ref.name}`} className="font-medium hover:underline">
                                {enumLabel(OperationKind, op.kind)}
                              </Link>
                            ) : (
                              enumLabel(OperationKind, op.kind)
                            )}
                          </TableCell>
                          <TableCell>
                            <ToneBadge tone={opStateTone(state)}>{t(`ops.state.${state}`)}</ToneBadge>
                          </TableCell>
                          <TableCell className="max-sm:whitespace-normal">{formatTimestamp(op.createdAt)}</TableCell>
                        </TableRow>
                      )
                    })}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          <div class="min-w-0 space-y-4">
            <Card>
              <CardHeader>
                <CardTitle>{t('overview.tile.nodes')}</CardTitle>
                {approved.length > 5 && (
                  <CardAction>
                    <Button asChild variant="link" size="sm">
                      <Link to="/nodes">{t('overview.viewAllNodes', { n: approved.length })}
                      <ArrowRightIcon data-icon="inline-end" aria-hidden="true" />
                    </Link>
                    </Button>
                  </CardAction>
                )}
              </CardHeader>
              <CardContent>
                {machines === null ? (
                  <p>-</p>
                ) : approved.length === 0 ? (
                  <EmptyNote icon={ServerIcon}>{t('overview.nodesNone')}</EmptyNote>
                ) : (
                  <ItemGroup>
                    {approved.slice(0, 5).map((m, i) => (
                      <NodeRow key={m.ref?.name} m={m} first={i === 0} />
                    ))}
                  </ItemGroup>
                )}
              </CardContent>
            </Card>

            <Card id="versions">
              <CardHeader>
                <CardTitle>{t('home.versions')}</CardTitle>
                {autoPatch.enabled && (
                  <CardAction>
                    <Badge variant="secondary">{t('versions.autoPatchOn')}</Badge>
                  </CardAction>
                )}
              </CardHeader>
              <CardContent>
                <ItemGroup>
                    <VersionRow name={t('ops.row.talos')} icon={CpuIcon} installed={cluster.talosVersion}>
                      {hasPatch(candidates.talos) && <Badge variant="secondary">{t('versions.patchAvailable')}</Badge>}
                      {may('talos') && <StartOperationDialog kind="talos" cluster={ref} current={cluster.talosVersion} label={t('ops.action.upgrade')} />}
                    </VersionRow>
                    <ItemSeparator />
                    <VersionRow name={t('ops.row.kubernetes')} icon={ContainerIcon} installed={cluster.kubernetesVersion}>
                      {hasPatch(candidates.kubernetes) && <Badge variant="secondary">{t('versions.patchAvailable')}</Badge>}
                      {may('kubernetes') && <StartOperationDialog kind="kubernetes" cluster={ref} current={cluster.kubernetesVersion} label={t('ops.action.upgrade')} />}
                    </VersionRow>
                    {!isCniUnmanaged(cluster.cniPreset) && (
                      <>
                        <ItemSeparator />
                        <VersionRow
                          name={t('ops.row.cilium')}
                          icon={ShieldCheckIcon}
                          installed={cilium?.current ?? ''}
                          target={cilium?.state === 'behind' ? cilium.target : undefined}
                        >
                          {cilium && cilium.state !== 'up-to-date' && <ToneBadge tone={stateTone(cilium.state)}>{t(stateKey(cilium.state))}</ToneBadge>}
                          {cilium?.state === 'behind' && may('cni') && <CiliumUpgradeDialog cluster={ref} chart={cilium.target} size="sm" />}
                        </VersionRow>
                      </>
                    )}
                    <ItemSeparator />
                    <VersionRow name={t('ops.row.ichor')} icon={CloudCogIcon} installed={ichorVersion ?? t('ops.installedUnknown')}>
                      {release && <Badge variant="secondary">{t('version.released', { version: release })}</Badge>}
                      {may('ichor') && <StartOperationDialog kind="ichor" cluster={ref} current={ichorVersion ?? ''} label={t('ops.action.upgrade')} />}
                    </VersionRow>
                    {!isCniUnmanaged(cluster.cniPreset) && (
                      <>
                        <ItemSeparator />
                        <VersionRow name={t('ops.row.cni')} icon={NetworkIcon} installed={t(kubeProxy === 'replaced' ? 'ops.modeReplaced' : 'ops.modeEnabled')}>
                          {may('cni') && <StartOperationDialog kind="cni" cluster={ref} current={kubeProxy} label={t('ops.action.switch')} />}
                        </VersionRow>
                      </>
                    )}
                </ItemGroup>
              </CardContent>
              {notes.length > 0 && (
                <CardFooter className="flex-col items-start gap-1">
                  {notes.map((n) => (
                    <CardDescription key={n.reason}>{t('versions.checkFailed', { components: n.components.join(', '), reason: n.reason })}</CardDescription>
                  ))}
                </CardFooter>
              )}
            </Card>
          </div>
        </div>

        <Collapsible>
          <Card>
            <CardHeader>
              <CollapsibleTrigger asChild>
                <Button variant="ghost" className="w-full justify-between">
                  {t('home.details')}
                  <ChevronsUpDownIcon aria-hidden="true" />
                </Button>
              </CollapsibleTrigger>
            </CardHeader>
            <CollapsibleContent>
              <CardContent className="space-y-4">
            <Fields>
              <Field label={t('home.endpoint')} value={cluster.endpoint} />
              <Field label={t('ops.row.contract')} value={versionOf('version-contract')?.current ?? ''} />
              <Field
                label={t('cluster.cpCount')}
                value={`${cluster.controlPlaneCount}${cluster.controlPlaneSchedulable ? ` ${t('clusters.schedulable')}` : ''}`}
              />
            </Fields>
            <Card size="sm">
<CardHeader><CardTitle>{t('cluster.conditions')}</CardTitle></CardHeader><CardContent>
              <ConditionList conditions={cluster.conditions} />
            </CardContent></Card>
            <Card size="sm">
<CardHeader><CardTitle>{t('cluster.rollout')}</CardTitle></CardHeader><CardContent>
              {cluster.rollout ? (
                <Fields>
                  <Field label={t('cluster.rolloutKind')} value={enumLabel(RolloutKind, cluster.rollout.kind)} />
                  <Field label={t('cluster.rolloutTarget')} value={cluster.rollout.targetVersion} />
                  <Field label={t('cluster.rolloutPaused')} value={cluster.rollout.paused ? t('common.yes') : t('common.no')} />
                  <Field label={t('cluster.rolloutProgress')} value={`${cluster.rollout.nodesDone}/${cluster.rollout.nodesTotal}`} />
                  <Field label={t('cluster.rolloutPhase')} value={cluster.rollout.currentPhase} />
                </Fields>
              ) : (
                <EmptyNote icon={InboxIcon}>{t('cluster.noRollout')}</EmptyNote>
              )}
            </CardContent></Card>
            <Card size="sm">
<CardHeader><CardTitle>{t('cluster.handover')}</CardTitle></CardHeader><CardContent>
              {cluster.handovers.length === 0 ? (
                <EmptyNote icon={InboxIcon}>{t('cluster.noHandover')}</EmptyNote>
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
            </CardContent></Card>
</CardContent></CollapsibleContent></Card></Collapsible>
      </div>
    </TooltipProvider>
  )
}

// VersionLine is one labelled version in the versions tile.
function VersionLine({ label, version }: { label: string; version: string }) {
  return (
    <div class="flex items-baseline justify-between gap-2">
      <CardDescription>{label}</CardDescription>
      <span>{versionLabel(version)}</span>
    </div>
  )
}

// Tile is one summary number as a stock Card: label (CardDescription), value (CardTitle), and a Badge when something needs attention.
// The whole tile leads somewhere: a page, or the versions card further down.
function Tile({ label, icon: Icon, value, sub, badge, target }: { label: string; icon: LucideIcon; value?: string; sub?: ComponentChildren; badge?: string; target: TileTarget }) {
  const body = (
    <Card size="sm" className="h-full">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        {value && <CardTitle>{value}</CardTitle>}
        <CardAction>
          <Icon aria-hidden="true" />
        </CardAction>
      </CardHeader>
      {(badge || sub) && (
        <CardContent className="space-y-2">
          {badge && (
            <Badge variant="destructive" className="max-w-full">
              <CircleAlertIcon data-icon="inline-start" aria-hidden="true" />
              {badge}
            </Badge>
          )}
          {sub}
        </CardContent>
      )}
    </Card>
  )
  if (target.kind === 'route') {
    return (
      <Link to={target.to} className="block h-full min-w-0">
        {body}
      </Link>
    )
  }
  return (
    <button type="button" class="block h-full w-full min-w-0 text-left" onClick={() => document.getElementById(target.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}>
      {body}
    </button>
  )
}

function NodeRow({ m, first }: { m: Machine; first: boolean }) {
  // The hostname, else the node name; the machine's UUID name is only the link target.
  const name = machineDisplayName(m)
  const body = (
    <>
      <ItemMedia variant="icon">
        {m.role === MachineRole.CONTROL_PLANE ? <ServerCogIcon aria-hidden="true" /> : <ServerIcon aria-hidden="true" />}
      </ItemMedia>
      <ItemContent>
        <ItemTitle>{name}</ItemTitle>
        <ItemDescription>{enumLabel(MachineRole, m.role)}</ItemDescription>
      </ItemContent>
      <ItemActions>
        <ReadyCell machine={m} />
      </ItemActions>
    </>
  )
  return (
    <>
      {!first && <ItemSeparator />}
      {m.ref ? (
        <Item asChild>
          <Link to={`/nodes/${m.ref.name}`}>{body}</Link>
        </Item>
      ) : (
        <Item>{body}</Item>
      )}
    </>
  )
}

// VersionRow is one component: what it runs, and the version it is going to when there is one (the Cilium chart
// that is newer than the installed release). Talos, the contract and Kubernetes show what they run.
function VersionRow({
  name,
  icon: Icon,
  installed,
  target,
  children,
}: {
  name: string
  icon: LucideIcon
  installed: string
  target?: string
  children?: ComponentChildren
}) {
  return (
    <Item>
      <ItemMedia variant="icon">
        <Icon aria-hidden="true" />
      </ItemMedia>
      <ItemContent>
        <ItemTitle>{name}</ItemTitle>
        <ItemDescription>{target !== undefined ? versionPair(installed, target) : installed || '-'}</ItemDescription>
      </ItemContent>
      <ItemActions>{children}</ItemActions>
    </Item>
  )
}

// ActiveProgress is the running operation's progress: nodes done, or its state, and the Kubernetes stage.
function ActiveProgress({ row, total }: { row: Row; total: number }) {
  const op = row.op
  const { done } = progressCounts(op.machines)
  const kubeStage = kubernetesStageProgress(op.kubernetesStage)
  const tracksNodes =
    op.kind !== OperationKind.ICHOR && op.kind !== OperationKind.CONFIG && op.kind !== OperationKind.NODE && op.kind !== OperationKind.ETCD_RESTORE && total > 0
  return (
    <>
      <p>
        {tracksNodes ? t('ops.currentNodes', { done, total }) : opStateTone(operationState(op.phase, op.failureReason)) === 'bad' ? null : t(`ops.state.${operationState(op.phase, op.failureReason)}`)}
        {kubeStage && ` · ${t('ops.kubeStage', { stage: t(`ops.kubeStageName.${kubeStage.name}`), n: kubeStage.index, total: kubeStage.total })}`}
      </p>
      {tracksNodes && <Progress value={(done / total) * 100} />}
    </>
  )
}
