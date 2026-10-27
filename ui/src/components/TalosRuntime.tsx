import { useEffect, useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { Section } from '@/components/SectionNav'
import type { GetNodeTalosRuntimeResponse } from '@/gen/ichor/v1alpha1/talosruntime_pb'
import { nodeClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import { formatBytes, formatResource } from '@/lib/format'
import type { Tone } from '@/lib/talosDetails'
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
import { useT } from '@/lib/useT'

// Read-only live state of one machine (docs/adr/0082): etcd, top processes,
// containers, the Kubernetes view of the node, routes/discovery/KubeSpan/time
// and kernel modules. One call; each card carries its own error, and a card the
// maintenance API refuses says so instead of showing a failure. Tables scroll
// inside their own container. The ids feed the machine page's section nav.

function badgeVariant(tone: Tone) {
  return tone === 'bad' ? 'destructive' : tone === 'ok' ? 'secondary' : 'outline'
}

// part="talos" renders processes, containers and kernel (the Talos tab);
// part="kubernetes" renders the k8s node, etcd and network state.
export function TalosRuntime({ namespace, name, part }: { namespace: string; name: string; part: 'talos' | 'kubernetes' }) {
  const t = useT()
  const [data, setData] = useState<GetNodeTalosRuntimeResponse | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)

  useEffect(() => {
    let cancelled = false
    setData(null)
    setError(null)
    nodeClient.getNodeTalosRuntime({ machine: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setData(res)
      },
      (err) => {
        if (!cancelled) setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [namespace, name])

  if (error) return <ErrorAlert {...error} />
  if (!data) return <p class="text-muted-foreground">{t('rt.loading')}</p>
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
  error,
  maintenance,
  children,
}: {
  id: string
  title: string
  error: string
  maintenance: boolean
  children: ComponentChildren
}) {
  const t = useT()
  return (
    <Section id={id} title={title}>
      {error ? (
        maintenance ? (
          <p class="text-muted-foreground">{t('rt.unavailableMaintenance')}</p>
        ) : (
          <Alert variant="destructive">
            <AlertDescription>
              {t('talos.sectionFailed')} <span class="break-words font-mono text-xs">{error}</span>
            </AlertDescription>
          </Alert>
        )
      ) : (
        children
      )}
    </Section>
  )
}

function Empty() {
  const t = useT()
  return <p class="text-muted-foreground">{t('talos.none')}</p>
}

function EtcdCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const t = useT()
  const e = data.etcd
  if (!e) return null
  const pct = dbInUsePercent(e)
  return (
    <Panel id="etcd" title={t('rt.etcd')} error={e.error} maintenance={maintenance}>
      {e.notApplicable ? (
        <p class="text-muted-foreground">{t('rt.etcdNotApplicable')}</p>
      ) : (
        <>
          <Fields>
            <Field label={t('rt.etcdHealth')}>
              <Badge variant={badgeVariant(etcdTone(e))}>{etcdTone(e) === 'ok' ? t('rt.healthy') : t('rt.attention')}</Badge>
            </Field>
            <Field label={t('rt.etcdMember')} value={e.memberId} />
            <Field label={t('rt.etcdLeader')} value={e.leaderId} />
            <Field label={t('rt.etcdDb')} value={`${formatBytes(e.dbSizeBytes)}${pct >= 0 ? ` (${pct}% ${t('rt.inUse')})` : ''}`} />
            <Field label={t('rt.etcdRaft')} value={`${t('rt.term')} ${e.raftTerm}, ${t('rt.index')} ${e.raftIndex}, ${t('rt.applied')} ${e.raftAppliedIndex}`} />
            <Field label={t('rt.etcdVersion')} value={[e.protocolVersion, e.storageVersion].filter(Boolean).join(' / ')} />
          </Fields>
          {e.alarms.length > 0 && (
            <Alert variant="destructive">
              <AlertDescription>
                {t('rt.etcdAlarms')} <span class="break-words font-mono text-xs">{e.alarms.join(', ')}</span>
              </AlertDescription>
            </Alert>
          )}
          {e.statusErrors.length > 0 && (
            <Alert variant="destructive">
              <AlertDescription class="break-words font-mono text-xs">{e.statusErrors.join('; ')}</AlertDescription>
            </Alert>
          )}
          {e.members.length === 0 ? (
            <Empty />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('rt.colMember')}</TableHead>
                  <TableHead>{t('talos.hostname')}</TableHead>
                  <TableHead>{t('rt.colPeer')}</TableHead>
                  <TableHead>{t('rt.colClient')}</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {e.members.map((m) => (
                  <TableRow key={m.id}>
                    <TableCell className="font-mono text-xs">{m.id}</TableCell>
                    <TableCell>{m.hostname || '-'}</TableCell>
                    <TableCell className="break-all font-mono text-xs">{m.peerUrls.join(', ') || '-'}</TableCell>
                    <TableCell className="break-all font-mono text-xs">{m.clientUrls.join(', ') || '-'}</TableCell>
                    <TableCell className="space-x-1">
                      {m.leader && <Badge variant="secondary">{t('rt.leader')}</Badge>}
                      {m.learner && <Badge variant="outline">{t('rt.learner')}</Badge>}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </>
      )}
    </Panel>
  )
}

function ProcessesCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const t = useT()
  const [by, setBy] = useState<ProcessSort>('memory')
  const p = data.processes
  if (!p) return null
  return (
    <Panel id="processes" title={t('rt.processes')} error={p.error} maintenance={maintenance}>
      <div class="flex flex-wrap items-center gap-2 text-sm">
        <span class="text-muted-foreground">{t('rt.processesNote', { n: p.processes.length, total: p.total })}</span>
        <Button size="sm" variant={by === 'memory' ? 'secondary' : 'outline'} onClick={() => setBy('memory')}>
          {t('rt.sortMemory')}
        </Button>
        <Button size="sm" variant={by === 'cpu' ? 'secondary' : 'outline'} onClick={() => setBy('cpu')}>
          {t('rt.sortCpu')}
        </Button>
      </div>
      {p.processes.length === 0 ? (
        <Empty />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('rt.colPid')}</TableHead>
              <TableHead>{t('talos.colName')}</TableHead>
              <TableHead>{t('talos.colState')}</TableHead>
              <TableHead>{t('rt.colThreads')}</TableHead>
              <TableHead>{t('rt.colCpu')}</TableHead>
              <TableHead>{t('rt.colRss')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sortProcesses(p.processes, by).map((x) => (
              <TableRow key={x.pid}>
                <TableCell>{x.pid}</TableCell>
                <TableCell className="font-mono text-xs">{x.name}</TableCell>
                <TableCell>{x.state}</TableCell>
                <TableCell>{x.threads}</TableCell>
                <TableCell>{formatCpuSeconds(x.cpuSeconds)}</TableCell>
                <TableCell>{formatBytes(x.residentBytes)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  )
}

function ContainersCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const t = useT()
  const c = data.containers
  if (!c) return null
  return (
    <Panel id="containers" title={t('rt.containers')} error={c.error} maintenance={maintenance}>
      {c.containers.length === 0 ? (
        <Empty />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('rt.colRuntime')}</TableHead>
              <TableHead>{t('rt.colPod')}</TableHead>
              <TableHead>{t('talos.colName')}</TableHead>
              <TableHead>{t('rt.colImage')}</TableHead>
              <TableHead>{t('talos.colState')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {c.containers.map((x) => (
              <TableRow key={`${x.runtimeNamespace}/${x.id}`}>
                <TableCell>{x.runtimeNamespace}</TableCell>
                <TableCell className="break-all font-mono text-xs">{x.pod || '-'}</TableCell>
                <TableCell className="font-mono text-xs">{x.name || x.id}</TableCell>
                <TableCell className="break-all font-mono text-xs">{x.image}</TableCell>
                <TableCell>
                  <Badge variant="outline">{x.status || '-'}</Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {c.truncated && <p class="text-xs text-muted-foreground">{t('rt.truncated')}</p>}
    </Panel>
  )
}

function KubernetesCard({ data }: { data: GetNodeTalosRuntimeResponse }) {
  const t = useT()
  const k = data.kubernetes
  if (!k) return null
  return (
    <Panel id="k8s-node" title={t('rt.k8s')} error={k.error} maintenance={false}>
      {k.permissionDenied ? (
        <p class="text-muted-foreground">{t('rt.k8sDenied')}</p>
      ) : k.notJoined ? (
        <p class="text-muted-foreground">{t('rt.k8sNotJoined')}</p>
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
              <Badge key={c.type} variant={badgeVariant(conditionTone(c))} title={[c.reason, c.message].filter(Boolean).join(': ')}>
                {c.type}: {c.status}
              </Badge>
            ))}
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('rt.colResource')}</TableHead>
                <TableHead>{t('rt.colCapacity')}</TableHead>
                <TableHead>{t('rt.colAllocatable')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {k.resources.map((r) => (
                <TableRow key={r.name}>
                  <TableCell>{r.name}</TableCell>
                  <TableCell>{formatResource(r.name, r.capacity)}</TableCell>
                  <TableCell>{formatResource(r.name, r.allocatable)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <p class="text-xs text-muted-foreground">{t('rt.pods', { n: k.pods.length })}</p>
          {k.podsError ? (
            <Alert variant="destructive">
              <AlertDescription class="break-words font-mono text-xs">{k.podsError}</AlertDescription>
            </Alert>
          ) : k.pods.length === 0 ? (
            <Empty />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('rt.colNamespace')}</TableHead>
                  <TableHead>{t('talos.colName')}</TableHead>
                  <TableHead>{t('rt.colPhase')}</TableHead>
                  <TableHead>{t('rt.colReady')}</TableHead>
                  <TableHead>{t('rt.colRestarts')}</TableHead>
                  <TableHead>{t('rt.colAge')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortPods(k.pods).map((p) => (
                  <TableRow key={`${p.namespace}/${p.name}`}>
                    <TableCell className="font-mono text-xs">{p.namespace}</TableCell>
                    <TableCell className="break-all font-mono text-xs">{p.name}</TableCell>
                    <TableCell>
                      <Badge variant={badgeVariant(podTone(p))}>{p.phase || '-'}</Badge>
                    </TableCell>
                    <TableCell>
                      {p.readyContainers}/{p.totalContainers}
                    </TableCell>
                    <TableCell>{p.restarts}</TableCell>
                    <TableCell>{formatAge(p.ageSeconds)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          {k.podsTruncated && <p class="text-xs text-muted-foreground">{t('rt.truncated')}</p>}
        </>
      )}
    </Panel>
  )
}

function NetworkStateCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const t = useT()
  const n = data.networkState
  if (!n) return null
  return (
    <Panel id="netstate" title={t('rt.netstate')} error={n.error} maintenance={maintenance}>
      <Fields>
        <Field label={t('rt.timeStatus')}>
          {n.timeStatus ? <Badge variant={n.timeSync ? 'secondary' : 'outline'}>{n.timeStatus}</Badge> : '-'}
        </Field>
        <Field label={t('rt.timeOffset')} value={n.timeStatus ? formatNanos(n.timeOffsetNanos) : ''} />
        <Field label={t('rt.timeMaxError')} value={n.timeStatus ? formatNanos(n.timeMaxErrorNanos) : ''} />
      </Fields>
      <p class="text-xs text-muted-foreground">{t('rt.routes')}</p>
      {n.routes.length === 0 ? (
        <Empty />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('rt.colDestination')}</TableHead>
              <TableHead>{t('rt.colGateway')}</TableHead>
              <TableHead>{t('rt.colLink')}</TableHead>
              <TableHead>{t('rt.colTable')}</TableHead>
              <TableHead>{t('rt.colProtocol')}</TableHead>
              <TableHead>{t('rt.colPriority')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {n.routes.map((r, i) => (
              <TableRow key={i}>
                <TableCell className="font-mono text-xs">{r.destination}</TableCell>
                <TableCell className="font-mono text-xs">{r.gateway || '-'}</TableCell>
                <TableCell>{r.link || '-'}</TableCell>
                <TableCell>{r.table}</TableCell>
                <TableCell>{r.protocol}</TableCell>
                <TableCell>{r.priority}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {n.routesTruncated && <p class="text-xs text-muted-foreground">{t('rt.truncated')}</p>}
      <p class="text-xs text-muted-foreground">{t('rt.members')}</p>
      {n.members.length === 0 ? (
        <Empty />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('talos.hostname')}</TableHead>
              <TableHead>{t('talos.addresses')}</TableHead>
              <TableHead>{t('talos.colType')}</TableHead>
              <TableHead>{t('rt.colOs')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {n.members.map((m, i) => (
              <TableRow key={i}>
                <TableCell>{m.hostname || '-'}</TableCell>
                <TableCell className="break-all font-mono text-xs">{m.addresses.join(', ')}</TableCell>
                <TableCell>{m.machineType || '-'}</TableCell>
                <TableCell>{m.operatingSystem || '-'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {n.kubespanPeers.length > 0 && (
        <>
          <p class="text-xs text-muted-foreground">{t('rt.kubespan')}</p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('rt.colPeer')}</TableHead>
                <TableHead>{t('rt.colEndpoint')}</TableHead>
                <TableHead>{t('talos.colState')}</TableHead>
                <TableHead>{t('rt.colRx')}</TableHead>
                <TableHead>{t('rt.colTx')}</TableHead>
                <TableHead>{t('rt.colHandshake')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {n.kubespanPeers.map((p, i) => (
                <TableRow key={i}>
                  <TableCell>{p.label || '-'}</TableCell>
                  <TableCell className="font-mono text-xs">{p.endpoint}</TableCell>
                  <TableCell>
                    <Badge variant={p.state === 'up' ? 'secondary' : 'outline'}>{p.state}</Badge>
                  </TableCell>
                  <TableCell>{formatBytes(p.receiveBytes)}</TableCell>
                  <TableCell>{formatBytes(p.transmitBytes)}</TableCell>
                  <TableCell>{handshakeAge(p.lastHandshakeUnix, Date.now())}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
    </Panel>
  )
}

function KernelCard({ data, maintenance }: { data: GetNodeTalosRuntimeResponse; maintenance: boolean }) {
  const t = useT()
  const k = data.kernelModules
  if (!k) return null
  return (
    <Panel id="kernel" title={t('rt.kernel')} error={k.error} maintenance={maintenance}>
      {k.modules.length === 0 ? (
        <Empty />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('talos.colName')}</TableHead>
              <TableHead>{t('node.colSize')}</TableHead>
              <TableHead>{t('rt.colUsedBy')}</TableHead>
              <TableHead>{t('talos.colState')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {k.modules.map((m) => (
              <TableRow key={m.name}>
                <TableCell className="font-mono text-xs">{m.name}</TableCell>
                <TableCell>{formatBytes(m.sizeBytes)}</TableCell>
                <TableCell>{m.referenceCount}</TableCell>
                <TableCell>{m.state || '-'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  )
}
