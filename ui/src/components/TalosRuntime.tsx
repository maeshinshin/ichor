import { useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { CircleXIcon, InboxIcon } from 'lucide-preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { TableCell, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { EmptyNote, Failed, Grid } from '@/components/PageStates'
import { SectionCard, SectionSkeleton } from '@/components/SectionCard'
import { ToneBadge } from '@/components/ToneBadge'
import { CardDescription } from '@/components/ui/card'
import type { GetNodeTalosRuntimeResponse } from '@/gen/ichor/v1alpha1/talosruntime_pb'
import { nodeClient } from '@/lib/connect'
import { useLoad } from '@/lib/useLoad'
import { formatBytes, formatResource } from '@/lib/format'
import {
  conditionTone,
  dbInUsePercent,
  etcdTone,
  formatAge,
  formatCpuSeconds,
  formatNanos,
  formatTaint,
  handshakeAge,
  podTone,
  sortPods,
  sortProcesses,
  type ProcessSort,
} from '@/lib/talosRuntime'
import { t } from '@/lib/i18n'

// Read-only live state of one machine (docs/adr/0082): etcd, top processes,
// containers, the Kubernetes view of the node, routes/discovery/KubeSpan/time
// and kernel modules. One call; each card carries its own error, and a card the
// maintenance API refuses says so instead of showing a failure. Tables scroll
// inside their own container. The ids are the anchors for the page's sections.

// part="talos" renders processes, containers and kernel (the Talos tab);
// part="kubernetes" renders the k8s node, etcd and network state.
export function TalosRuntime({ namespace, name, part }: { namespace: string; name: string; part: 'talos' | 'kubernetes' }) {
  const { data, error } = useLoad(() => nodeClient.getNodeTalosRuntime({ machine: { namespace, name } }), [namespace, name])

  if (error) return <ErrorAlert {...error} />
  if (!data) return <SectionSkeleton label={t('rt.loading')} />
  const m = data.maintenanceMode
  if (part === 'kubernetes') {
    return (
      <>
        <KubernetesCard data={data} />
        <EtcdCard data={data} maintenance={m} />
        <NetworkStateCard data={data} maintenance={m} />
      </>
    )
  }
  return (
    <>
      <ProcessesCard data={data} maintenance={m} />
      <ContainersCard data={data} maintenance={m} />
      <KernelCard data={data} maintenance={m} />
    </>
  )
}

function Panel({
  id,
  title,
  description,
  actions,
  error,
  maintenance,
  children,
}: {
  id: string
  title: string
  description?: ComponentChildren
  actions?: ComponentChildren
  error: string
  maintenance: boolean
  children: ComponentChildren
}) {
  return (
    <SectionCard id={id} title={title} description={description} actions={actions}>
      {error && maintenance ? (
        <EmptyNote icon={InboxIcon}>{t('rt.unavailableMaintenance')}</EmptyNote>
      ) : (
        <Failed error={error}>{children}</Failed>
      )}
    </SectionCard>
  )
}

function EtcdCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const e = data.etcd
  if (!e) return null
  const pct = dbInUsePercent(e)
  return (
    <Panel id="etcd" title={t('rt.etcd')} error={e.error} maintenance={maintenance}>
      {e.notApplicable ? (
        <EmptyNote icon={InboxIcon}>{t('rt.etcdNotApplicable')}</EmptyNote>
      ) : (
        <>
          <Fields>
            <Field label={t('rt.etcdHealth')}>
              <ToneBadge tone={etcdTone(e)}>{etcdTone(e) === 'good' ? t('rt.healthy') : t('rt.attention')}</ToneBadge>
            </Field>
            <Field label={t('rt.etcdMember')} value={e.memberId} />
            <Field label={t('rt.etcdLeader')} value={e.leaderId} />
            <Field label={t('rt.etcdDb')} value={`${formatBytes(e.dbSizeBytes)}${pct >= 0 ? ` (${pct}% ${t('rt.inUse')})` : ''}`} />
            <Field label={t('rt.etcdRaft')} value={`${t('rt.term')} ${e.raftTerm}, ${t('rt.index')} ${e.raftIndex}, ${t('rt.applied')} ${e.raftAppliedIndex}`} />
            <Field label={t('rt.etcdVersion')} value={[e.protocolVersion, e.storageVersion].filter(Boolean).join(' / ')} />
          </Fields>
          {e.alarms.length > 0 && (
            <Alert variant="destructive">
              <CircleXIcon />
              <AlertTitle>{t('rt.etcdAlarms')}</AlertTitle>
              <AlertDescription className="break-words">{e.alarms.join(', ')}</AlertDescription>
            </Alert>
          )}
          {e.statusErrors.length > 0 && (
            <Alert variant="destructive">
              <CircleXIcon />
              <AlertDescription className="break-words">{e.statusErrors.join('; ')}</AlertDescription>
            </Alert>
          )}
          <Grid heads={[t('rt.colMember'), t('talos.hostname'), t('rt.colPeer'), t('rt.colClient'), '']} empty={e.members.length === 0}>
            {e.members.map((m) => (
              <TableRow key={m.id}>
                <TableCell className="font-mono">{m.id}</TableCell>
                <TableCell>{m.hostname || '-'}</TableCell>
                <TableCell className="min-w-40 whitespace-normal break-all font-mono">{m.peerUrls.join(', ') || '-'}</TableCell>
                <TableCell className="min-w-40 whitespace-normal break-all font-mono">{m.clientUrls.join(', ') || '-'}</TableCell>
                <TableCell>
                  <div class="flex flex-wrap gap-1">
                    {m.leader && <Badge variant="secondary">{t('rt.leader')}</Badge>}
                    {m.learner && <Badge variant="outline">{t('rt.learner')}</Badge>}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </Grid>
        </>
      )}
    </Panel>
  )
}

function ProcessesCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const [by, setBy] = useState<ProcessSort>('memory')
  const p = data.processes
  if (!p) return null
  return (
    <Panel
      id="processes"
      title={t('rt.processes')}
      description={t('rt.processesNote', { n: p.processes.length, total: p.total })}
      actions={
        <div class="flex flex-wrap items-center gap-2">
          <Button size="sm" variant={by === 'memory' ? 'secondary' : 'outline'} onClick={() => setBy('memory')}>
            {t('rt.sortMemory')}
          </Button>
          <Button size="sm" variant={by === 'cpu' ? 'secondary' : 'outline'} onClick={() => setBy('cpu')}>
            {t('rt.sortCpu')}
          </Button>
        </div>
      }
      error={p.error}
      maintenance={maintenance}
    >
      <Grid heads={[t('rt.colPid'), t('talos.colName'), t('talos.colState'), t('rt.colThreads'), t('rt.colCpu'), t('rt.colRss')]} empty={p.processes.length === 0}>
        {sortProcesses(p.processes, by).map((x) => (
          <TableRow key={x.pid}>
            <TableCell>{x.pid}</TableCell>
            <TableCell className="font-mono">{x.name}</TableCell>
            <TableCell>{x.state}</TableCell>
            <TableCell>{x.threads}</TableCell>
            <TableCell>{formatCpuSeconds(x.cpuSeconds)}</TableCell>
            <TableCell>{formatBytes(x.residentBytes)}</TableCell>
          </TableRow>
        ))}
      </Grid>
    </Panel>
  )
}

function ContainersCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const c = data.containers
  if (!c) return null
  return (
    <Panel id="containers" title={t('rt.containers')} error={c.error} maintenance={maintenance}>
      <Grid heads={[t('rt.colRuntime'), t('rt.colPod'), t('talos.colName'), t('rt.colImage'), t('talos.colState')]} empty={c.containers.length === 0}>
        {c.containers.map((x) => (
          <TableRow key={`${x.runtimeNamespace}/${x.id}`}>
            <TableCell>{x.runtimeNamespace}</TableCell>
            <TableCell className="min-w-40 whitespace-normal break-all font-mono">{x.pod || '-'}</TableCell>
            <TableCell className="font-mono">{x.name || x.id}</TableCell>
            <TableCell className="min-w-40 whitespace-normal break-all font-mono">{x.image}</TableCell>
            <TableCell>
              <Badge variant="outline">{x.status || '-'}</Badge>
            </TableCell>
          </TableRow>
        ))}
      </Grid>
      {c.truncated && <CardDescription>{t('rt.truncated')}</CardDescription>}
    </Panel>
  )
}

function KubernetesCard({ data }: { data: GetNodeTalosRuntimeResponse }) {
  const k = data.kubernetes
  if (!k) return null
  return (
    <Panel id="k8s-node" title={t('rt.k8s')} error={k.error} maintenance={false}>
      {k.permissionDenied ? (
        <EmptyNote icon={InboxIcon}>{t('rt.k8sDenied')}</EmptyNote>
      ) : k.notJoined ? (
        <EmptyNote icon={InboxIcon}>{t('rt.k8sNotJoined')}</EmptyNote>
      ) : (
        <>
          <Fields>
            <Field label={t('talos.k8sNodename')} value={k.nodeName} />
            <Field label={t('node.k8sKubelet')} value={k.kubeletVersion} />
            <Field label={t('node.k8sSchedulable')} value={k.unschedulable ? t('common.no') : t('common.yes')} />
            <Field label={t('rt.taints')} value={k.taints.map(formatTaint).join(', ')} />
          </Fields>
          <div class="flex flex-wrap gap-1">
            {k.conditions.map((c) => (
              <ToneBadge key={c.type} tone={conditionTone(c)} title={[c.reason, c.message].filter(Boolean).join(': ')}>
                {c.type}: {c.status}
              </ToneBadge>
            ))}
          </div>
          <Grid heads={[t('rt.colResource'), t('rt.colCapacity'), t('rt.colAllocatable')]}>
            {k.resources.map((r) => (
              <TableRow key={r.name}>
                <TableCell>{r.name}</TableCell>
                <TableCell>{formatResource(r.name, r.capacity)}</TableCell>
                <TableCell>{formatResource(r.name, r.allocatable)}</TableCell>
              </TableRow>
            ))}
          </Grid>
          <CardDescription>{t('rt.pods', { n: k.pods.length })}</CardDescription>
          {k.podsError ? (
            <Alert variant="destructive">
              <CircleXIcon />
              <AlertDescription className="break-words">{k.podsError}</AlertDescription>
            </Alert>
          ) : (
            <Grid heads={[t('rt.colNamespace'), t('talos.colName'), t('rt.colPhase'), t('rt.colReady'), t('rt.colRestarts'), t('rt.colAge')]} empty={k.pods.length === 0}>
              {sortPods(k.pods).map((p) => (
                <TableRow key={`${p.namespace}/${p.name}`}>
                  <TableCell className="font-mono">{p.namespace}</TableCell>
                  <TableCell className="min-w-40 whitespace-normal break-all font-mono">{p.name}</TableCell>
                  <TableCell>
                    <ToneBadge tone={podTone(p)}>{p.phase || '-'}</ToneBadge>
                  </TableCell>
                  <TableCell>
                    {p.readyContainers}/{p.totalContainers}
                  </TableCell>
                  <TableCell>{p.restarts}</TableCell>
                  <TableCell>{formatAge(p.ageSeconds)}</TableCell>
                </TableRow>
              ))}
            </Grid>
          )}
          {k.podsTruncated && <CardDescription>{t('rt.truncated')}</CardDescription>}
        </>
      )}
    </Panel>
  )
}

function NetworkStateCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const n = data.networkState
  if (!n) return null
  return (
    <Panel id="netstate" title={t('rt.netstate')} error={n.error} maintenance={maintenance}>
      <Fields>
        <Field label={t('rt.timeStatus')}>
          {n.timeStatus ? <ToneBadge tone={n.timeSync ? 'good' : 'neutral'}>{n.timeStatus}</ToneBadge> : '-'}
        </Field>
        <Field label={t('rt.timeOffset')} value={n.timeStatus ? formatNanos(n.timeOffsetNanos) : ''} />
        <Field label={t('rt.timeMaxError')} value={n.timeStatus ? formatNanos(n.timeMaxErrorNanos) : ''} />
      </Fields>
      <CardDescription>{t('rt.routes')}</CardDescription>
      <Grid heads={[t('rt.colDestination'), t('rt.colGateway'), t('rt.colLink'), t('rt.colTable'), t('rt.colProtocol'), t('rt.colPriority')]} empty={n.routes.length === 0}>
        {n.routes.map((r, i) => (
          <TableRow key={i}>
            <TableCell className="font-mono">{r.destination}</TableCell>
            <TableCell className="font-mono">{r.gateway || '-'}</TableCell>
            <TableCell>{r.link || '-'}</TableCell>
            <TableCell>{r.table}</TableCell>
            <TableCell>{r.protocol}</TableCell>
            <TableCell>{r.priority}</TableCell>
          </TableRow>
        ))}
      </Grid>
      {n.routesTruncated && <CardDescription>{t('rt.truncated')}</CardDescription>}
      <CardDescription>{t('rt.members')}</CardDescription>
      <Grid heads={[t('talos.hostname'), t('talos.addresses'), t('talos.colType'), t('rt.colOs')]} empty={n.members.length === 0}>
        {n.members.map((m, i) => (
          <TableRow key={i}>
            <TableCell>{m.hostname || '-'}</TableCell>
            <TableCell className="min-w-40 whitespace-normal break-all font-mono">{m.addresses.join(', ')}</TableCell>
            <TableCell>{m.machineType || '-'}</TableCell>
            <TableCell>{m.operatingSystem || '-'}</TableCell>
          </TableRow>
        ))}
      </Grid>
      {n.kubespanPeers.length > 0 && (
        <>
          <CardDescription>{t('rt.kubespan')}</CardDescription>
          <Grid heads={[t('rt.colPeer'), t('rt.colEndpoint'), t('talos.colState'), t('rt.colRx'), t('rt.colTx'), t('rt.colHandshake')]}>
            {n.kubespanPeers.map((p, i) => (
              <TableRow key={i}>
                <TableCell>{p.label || '-'}</TableCell>
                <TableCell className="font-mono">{p.endpoint}</TableCell>
                <TableCell>
                  <ToneBadge tone={p.state === 'up' ? 'good' : 'neutral'}>{p.state}</ToneBadge>
                </TableCell>
                <TableCell>{formatBytes(p.receiveBytes)}</TableCell>
                <TableCell>{formatBytes(p.transmitBytes)}</TableCell>
                <TableCell>{handshakeAge(p.lastHandshakeUnix, Date.now())}</TableCell>
              </TableRow>
            ))}
          </Grid>
        </>
      )}
    </Panel>
  )
}

function KernelCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const k = data.kernelModules
  if (!k) return null
  return (
    <Panel id="kernel" title={t('rt.kernel')} error={k.error} maintenance={maintenance}>
      <Grid heads={[t('talos.colName'), t('node.colSize'), t('rt.colUsedBy'), t('talos.colState')]} empty={k.modules.length === 0}>
        {k.modules.map((m) => (
          <TableRow key={m.name}>
            <TableCell className="font-mono">{m.name}</TableCell>
            <TableCell>{formatBytes(m.sizeBytes)}</TableCell>
            <TableCell>{m.referenceCount}</TableCell>
            <TableCell>{m.state || '-'}</TableCell>
          </TableRow>
        ))}
      </Grid>
    </Panel>
  )
}
