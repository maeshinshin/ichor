import type { ComponentChildren } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { PageHeader } from '@/components/PageHeader'
import { PageSkeleton } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { DataTable, LinkRow } from '@/components/ListToolbar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { clusterClient, machineClient, operationClient, patchClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { operationState, type OpState } from '@/lib/operations'
import { clusterReadiness, expiringCertificatesMessage, machineCounts, operationCounts, operationsLine, type MachineLite } from '@/lib/overview'
import { opStateTone } from '@/lib/status'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

type Data = {
  clusters: Cluster[] | null
  machines: Machine[] | null
  ops: Operation[] | null
  patches: number | null
}

function approval(m: Machine): MachineLite['approval'] {
  if (m.approvalState === MachineApprovalState.APPROVED) return 'approved'
  if (m.approvalState === MachineApprovalState.REJECTED) return 'rejected'
  return 'pending'
}

function readyOf(m: Machine): string {
  return m.conditions.find((c) => c.type === 'Ready')?.status ?? ''
}

function opSeconds(o: Operation): number {
  return Number(o.createdAt?.seconds ?? 0n)
}

// Overview is the landing page: how many of each thing there is and whether it
// is healthy, what needs a person, and the latest operations. Every figure
// links to the list it summarises. A list the user may not read shows "not
// available" instead of failing the page.
export function Overview() {
  const t = useT()
  const [data, setData] = useState<Data | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)

  async function reload() {
    const [c, m, o, p] = await Promise.allSettled([
      clusterClient.listClusters({}),
      machineClient.listMachines({}),
      operationClient.listOperations({}),
      patchClient.listPatches({}),
    ])
    if (c.status === 'rejected' && m.status === 'rejected' && o.status === 'rejected') {
      setError(describeError(c.reason))
      return
    }
    setError(null)
    setData({
      clusters: c.status === 'fulfilled' ? c.value.clusters : null,
      machines: m.status === 'fulfilled' ? m.value.machines : null,
      ops: o.status === 'fulfilled' ? o.value.operations.slice().sort((a, b) => opSeconds(b) - opSeconds(a)) : null,
      patches: p.status === 'fulfilled' ? p.value.patches.length : null,
    })
  }

  useEffect(() => {
    reload()
    const id = setInterval(reload, 10000)
    return () => clearInterval(id)
  }, [])

  if (error) return <ErrorAlert {...error} />
  if (!data) return <PageSkeleton label={t('overview.loading')} />

  const opStates: OpState[] = (data.ops ?? []).map((o) => operationState(o.phase, o.failureReason))
  const mc = data.machines ? machineCounts(data.machines.map((m) => ({ approval: approval(m), ready: readyOf(m) }))) : null
  const oc = data.ops ? operationCounts(opStates) : null
  const cr = data.clusters ? clusterReadiness(data.clusters.map((c) => c.conditions)) : null
  const rolling =data.clusters?.filter((c) => c.rollout).length ?? 0
  const na = t('overview.noAccess')

  return (
    <div>
      <PageHeader title={t('overview.title')} />
      <div class="mb-6 grid grid-cols-2 divide-x divide-y rounded-md border bg-card lg:grid-cols-4 lg:divide-y-0">
        <Tile to="/clusters" label={t('overview.clusters')} value={data.clusters?.length}>
          {cr && cr.total > 0 && (
            <StatusText tone={cr.ready === cr.total ? 'good' : 'warn'}>
              {cr.ready === cr.total ? t('overview.clustersReady', { n: cr.ready }) : t('overview.clustersSomeReady', { n: cr.ready, total: cr.total })}
            </StatusText>
          )}
          {rolling > 0 && <StatusText tone="progress">{t('overview.clustersRollout', { n: rolling })}</StatusText>}
        </Tile>
        <Tile to="/machines" label={t('overview.machines')} value={mc?.total}>
          {mc && (
            <>
              <StatusText tone="good">{t('overview.machinesReady', { n: mc.ready })}</StatusText>
              {mc.notReady > 0 && <StatusText tone="bad">{t('overview.machinesNotReady', { n: mc.notReady })}</StatusText>}
              {mc.pending > 0 && <StatusText tone="warn">{t('overview.machinesPending', { n: mc.pending })}</StatusText>}
              {mc.unknown > 0 && <StatusText tone="neutral">{t('overview.machinesUnknown', { n: mc.unknown })}</StatusText>}
            </>
          )}
        </Tile>
        <Tile to="/operations" label={t('overview.operations')} value={oc?.total}>
          {oc && (
            <>
              {operationsLine(oc).map((line) =>
                line === 'none' ? (
                  <StatusText key={line} tone="neutral">{t('overview.opsNone')}</StatusText>
                ) : line === 'allCompleted' ? (
                  <StatusText key={line} tone="good">{t('overview.opsAllCompleted')}</StatusText>
                ) : line === 'running' ? (
                  <StatusText key={line} tone="progress">{t('overview.opsRunning', { n: oc.running })}</StatusText>
                ) : line === 'failed' ? (
                  <StatusText key={line} tone="bad">{t('overview.opsFailed', { n: oc.failed })}</StatusText>
                ) : (
                  <StatusText key={line} tone="warn">{t('overview.opsPaused', { n: oc.paused })}</StatusText>
                ),
              )}
            </>
          )}
        </Tile>
        <Tile to="/patches" label={t('overview.patches')} value={data.patches ?? undefined} />
      </div>

      <div class="grid gap-6 lg:grid-cols-2">
        <section>
          <h2 class="mb-2 text-sm font-medium">{t('overview.attention')}</h2>
          <Attention data={data} states={opStates} na={na} />
        </section>
        <section>
          <div class="mb-2 flex items-baseline justify-between">
            <h2 class="text-sm font-medium">{t('overview.recentOps')}</h2>
            <Link to="/operations" class="text-xs text-muted-foreground hover:underline">
              {t('overview.viewAll')}
            </Link>
          </div>
          <DataTable>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.name')}</TableHead>
                  <TableHead>{t('ops.colKind')}</TableHead>
                  <TableHead>{t('ops.colState')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(data.ops ?? []).slice(0, 5).map((op) => {
                  const s = operationState(op.phase, op.failureReason)
                  return (
                    <LinkRow key={`${op.ref?.namespace}/${op.ref?.name}`} to={op.ref ? objectRefPath('/operations', op.ref) : '/operations'}>
                      <TableCell className="font-medium">{op.ref?.name}</TableCell>
                      <TableCell>{enumLabel(OperationKind, op.kind)}</TableCell>
                      <TableCell>
                        <StatusText tone={opStateTone(s)}>{t(`ops.state.${s}`)}</StatusText>
                      </TableCell>
                    </LinkRow>
                  )
                })}
                {(data.ops ?? []).length === 0 && (
                  <TableRow>
                    <TableCell colSpan={3} className="text-muted-foreground">
                      {t('ops.empty')}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </DataTable>
        </section>
      </div>
    </div>
  )
}

function Tile({ to, label, value, children }: { to: string; label: string; value?: number; children?: ComponentChildren }) {
  return (
    <Link to={to} class="block min-w-0 space-y-2 p-4 transition-colors hover:bg-accent/50">
      <div class="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
      <div class="text-3xl font-semibold tabular-nums">{value ?? '-'}</div>
      <div class="flex flex-col items-start gap-1">{children}</div>
    </Link>
  )
}

type Item = { key: string; to: string; name: string; tone: 'warn' | 'bad' | 'progress'; text: string }

function Attention({ data, states, na }: { data: Data; states: OpState[]; na: string }) {
  const t = useT()
  const items: Item[] = []
  for (const m of data.machines ?? []) {
    const name = m.hostname || m.ref?.name || ''
    const to = m.ref ? objectRefPath('/nodes', m.ref) : '/machines'
    const a = approval(m)
    if (a === 'pending') items.push({ key: `m-${m.ref?.name}`, to: '/machines', name: m.identity?.uuid || name, tone: 'warn', text: t('overview.attentionPending') })
    else if (a === 'approved' && readyOf(m) === 'False') items.push({ key: `m-${m.ref?.name}`, to, name, tone: 'bad', text: t('overview.attentionNotReady') })
  }
  ;(data.ops ?? []).forEach((o, i) => {
    const s = states[i]
    if (s === 'failed' || s === 'paused' || s === 'running') {
      items.push({
        key: `o-${o.ref?.name}`,
        to: o.ref ? objectRefPath('/operations', o.ref) : '/operations',
        name: o.ref?.name ?? '',
        tone: s === 'failed' ? 'bad' : s === 'paused' ? 'warn' : 'progress',
        text: t('overview.attentionOp', { state: t(`ops.state.${s}`).toLowerCase() }),
      })
    }
  })
  for (const c of data.clusters ?? []) {
    if (c.rollout && c.ref) items.push({ key: `c-${c.ref.name}`, to: objectRefPath('/clusters', c.ref), name: c.ref.name, tone: 'progress', text: t('overview.attentionRollout') })
  }
  for (const c of data.clusters ?? []) {
    if (c.ref && expiringCertificatesMessage(c.conditions) !== null) {
      items.push({ key: `x-${c.ref.name}`, to: objectRefPath('/clusters', c.ref), name: c.ref.name, tone: 'warn', text: t('overview.attentionCerts') })
    }
  }
  if (data.machines === null && data.ops === null) return <p class="text-sm text-muted-foreground">{na}</p>
  return (
    <DataTable>
      <Table>
        <TableBody>
          {items.length === 0 && (
            <TableRow>
              <TableCell className="text-muted-foreground">{t('overview.attentionEmpty')}</TableCell>
            </TableRow>
          )}
          {items.map((i) => (
            <LinkRow key={i.key} to={i.to}>
              <TableCell className="font-medium">{i.name}</TableCell>
              <TableCell>
                <StatusText tone={i.tone}>{i.text}</StatusText>
              </TableCell>
            </LinkRow>
          ))}
        </TableBody>
      </Table>
    </DataTable>
  )
}
