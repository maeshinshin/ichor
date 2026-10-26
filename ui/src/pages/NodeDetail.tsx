import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConditionList, StatusBadge } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields, PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section, SectionLayout, type SectionLink } from '@/components/SectionNav'
import { NodeLogs } from '@/components/NodeLogs'
import { TalosDetails } from '@/components/TalosDetails'
import type { EffectiveConfigValue } from '@/gen/ichor/v1alpha1/node_pb'
import { ConfigLayer, DiskRole, type NodeDetail as NodeDetailMessage } from '@/gen/ichor/v1alpha1/node_pb'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { type BreakGlassRun } from '@/components/BreakGlassDialog'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import { SetInstallDialog } from '@/components/SetInstallDialog'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient, nodeClient } from '@/lib/connect'
import { needsInstallDisk } from '@/lib/installDisk'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { formatBytes } from '@/lib/format'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

// Node detail: hardware, disks, network, live resource usage, Kubernetes
// state, effective config (origin-annotated, secrets masked) and logs
// (architecture.md, "CLI and UI").
export function NodeDetail({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [node, setNode] = useState<NodeDetailMessage | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [machine, setMachine] = useState<Machine | null>(null)
  const [pullRequest, setPullRequest] = useState<{ url: string; run?: BreakGlassRun } | null>(null)
  const [cluster, setCluster] = useState<Cluster | null>(null)

  useEffect(() => {
    let cancelled = false
    setNode(null)
    setError(null)
    nodeClient.getNode({ machine: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setNode(res.node ?? null)
      },
      (err) => {
        if (!cancelled) setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [namespace, name])

  // The machine and its cluster are context around the node: the overview, and whether it is
  // waiting for an install disk. If either lookup fails the page still renders from
  // the node detail alone. loadMachine also refreshes after the install disk is set.
  function loadMachine() {
    let cancelled = false
    machineClient
      .getMachine({ machine: { namespace, name } })
      .then(async (res) => {
        if (cancelled) return
        setMachine(res.machine ?? null)
        if (res.machine?.cluster) {
          const c = await clusterClient.getCluster({ cluster: res.machine.cluster })
          if (!cancelled) setCluster(c.cluster ?? null)
        }
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }
  useEffect(() => {
    setMachine(null)
    setCluster(null)
    return loadMachine()
  }, [namespace, name])

  if (error) return <ErrorAlert {...error} />
  if (!node) return <PageSkeleton label={t('node.loading')} />

  const ready = machine?.conditions.find((c) => c.type === 'Ready')
  const sections: SectionLink[] = [
    { id: 'overview', label: t('page.overview') },
    { id: 'conditions', label: t('cluster.conditions') },
    { id: 'hardware', label: t('node.hardware') },
    { id: 'disks', label: t('node.disks') },
    { id: 'network', label: t('node.network') },
    { id: 'usage', label: t('node.usage') },
    { id: 'kubernetes', label: t('node.kubernetes') },
    { id: 'talos', label: t('talos.title') },
    { id: 'config', label: t('node.config') },
    { id: 'logs', label: t('node.logs') },
  ]

  return (
    <>
      <PageHeader
        title={machine?.hostname || name}
        kind={t('kind.machine')}
        badges={
          machine && (
            <>
              <Badge variant={machine.approvalState === MachineApprovalState.APPROVED ? 'secondary' : 'outline'}>
                {enumLabel(MachineApprovalState, machine.approvalState)}
              </Badge>
              {ready && <StatusBadge type={ready.type} status={ready.status} />}
              {machine.stage && <Badge variant="outline">{machine.stage}</Badge>}
            </>
          )
        }
        actions={
          machine?.cluster && (
            <Button asChild variant="outline" size="sm">
              <Link to={objectRefPath('/clusters', machine.cluster)}>{t('node.openCluster')}</Link>
            </Button>
          )
        }
      />
      {pullRequest && <PullRequestNotice url={pullRequest.url} breakGlass={pullRequest.run} onBreakGlassDone={loadMachine} />}
      {machine && needsInstallDisk({ ...machine, approved: machine.approvalState === MachineApprovalState.APPROVED }) && (
        <Alert className="mb-4">
          <AlertTitle>{t('installDisk.waitingTitle')}</AlertTitle>
          <AlertDescription>
            <p>{t('installDisk.waitingBody')}</p>
            <div class="mt-2">
              <SetInstallDialog
                machine={machine}
                disks={node.disks}
                onDone={loadMachine}
                onPullRequest={(url, run) => setPullRequest({ url, run })}
              />
            </div>
          </AlertDescription>
        </Alert>
      )}
      <SectionLayout sections={sections}>
        <Section id="overview" title={t('page.overview')}>
          <Fields>
            <Field label={t('machines.colHostname')} value={machine?.hostname || name} />
            <Field label={t('col.role')} value={machine ? enumLabel(MachineRole, machine.role) : undefined} />
            <Field label={t('col.cluster')}>
              {machine?.cluster ? (
                <Link to={objectRefPath('/clusters', machine.cluster)} class="hover:underline">
                  {machine.cluster.name}
                </Link>
              ) : (
                '-'
              )}
            </Field>
            <Field label={t('node.approval')} value={machine ? enumLabel(MachineApprovalState, machine.approvalState) : undefined} />
            <Field label={t('col.ready')}>{ready ? <StatusBadge type={ready.type} status={ready.status} /> : '-'}</Field>
            <Field label={t('node.stage')} value={machine?.stage} />
            <Field label={t('col.endpoint')} value={machine?.endpoint} />
            <Field label={t('cluster.talosVersion')} value={cluster?.talosVersion} />
            <Field label={t('node.k8sKubelet')} value={node.kubernetes?.kubeletVersion} />
          </Fields>
        </Section>

        <Section id="conditions" title={t('cluster.conditions')}>
          <ConditionList conditions={machine?.conditions ?? []} />
        </Section>

        <Section id="hardware" title={t('node.hardware')}>
          <Fields>
            <Field label={t('node.manufacturer')} value={node.hardware?.manufacturer} />
            <Field label={t('node.model')} value={node.hardware?.model} />
            <Field label={t('node.serialNumber')} value={node.hardware?.serialNumber} />
            <Field label={t('node.cpuCores')} value={node.hardware ? String(node.hardware.cpuCores) : undefined} />
            <Field label={t('node.memory')} value={node.hardware ? formatBytes(node.hardware.memoryTotalBytes) : undefined} />
          </Fields>
        </Section>

        <Section id="disks" title={t('node.disks')}>
          {node.disks.length === 0 ? (
            <EmptyNote>{t('node.noDisks')}</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('node.colIdentification')}</TableHead>
                  <TableHead>{t('node.colSize')}</TableHead>
                  <TableHead>{t('col.role')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {node.disks.map((d, i) => (
                  <TableRow key={i}>
                    <TableCell>{diskIdentification(d, t('node.noStableDiskId'))}</TableCell>
                    <TableCell>{formatBytes(d.sizeBytes)}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{enumLabel(DiskRole, d.role)}</Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Section>

        <Section id="network" title={t('node.network')}>
          {node.networkInterfaces.length === 0 ? (
            <EmptyNote>{t('node.noNetwork')}</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('node.colInterface')}</TableHead>
                  <TableHead>{t('node.colMac')}</TableHead>
                  <TableHead>{t('node.colUp')}</TableHead>
                  <TableHead>{t('node.colAddresses')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {node.networkInterfaces.map((n) => (
                  <TableRow key={n.name}>
                    <TableCell>{n.alias || n.name}</TableCell>
                    <TableCell>{n.permanentMac || '-'}</TableCell>
                    <TableCell>
                      <Badge variant={n.up ? 'secondary' : 'outline'}>{n.up ? t('node.up') : t('node.down')}</Badge>
                    </TableCell>
                    <TableCell>{n.addresses.length ? n.addresses.join(', ') : '-'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Section>

        <Section id="usage" title={t('node.usage')}>
          {node.resourceUsage ? (
            <Fields>
              <Field label={t('node.cpuUsed')} value={node.resourceUsage.cpuCoresUsed.toFixed(2)} />
              <Field
                label={t('node.memory')}
                value={`${formatBytes(node.resourceUsage.memoryUsedBytes)} / ${formatBytes(node.resourceUsage.memoryTotalBytes)}`}
              />
              <Field
                label={t('node.filesystem')}
                value={`${formatBytes(node.resourceUsage.filesystemUsedBytes)} / ${formatBytes(node.resourceUsage.filesystemTotalBytes)}`}
              />
            </Fields>
          ) : (
            <EmptyNote>{t('node.noUsage')}</EmptyNote>
          )}
        </Section>

        <Section id="kubernetes" title={t('node.kubernetes')}>
          {node.kubernetes ? (
            <Fields>
              <Field label={t('node.k8sName')} value={node.kubernetes.nodeName} />
              <Field label={t('node.k8sReady')} value={node.kubernetes.ready ? t('common.yes') : t('common.no')} />
              <Field label={t('node.k8sKubelet')} value={node.kubernetes.kubeletVersion} />
              <Field label={t('node.k8sSchedulable')} value={node.kubernetes.schedulable ? t('common.yes') : t('common.no')} />
            </Fields>
          ) : node.kubernetesPermissionDenied ? (
            <EmptyNote>{t('common.noPermission')}</EmptyNote>
          ) : (
            <EmptyNote>{t('node.notJoined')}</EmptyNote>
          )}
        </Section>

        <div id="talos" class="scroll-mt-24">
          <TalosDetails namespace={namespace} name={name} />
        </div>
        <EffectiveConfigSection namespace={namespace} name={name} />
        <div id="logs" class="scroll-mt-24">
          <NodeLogs namespace={namespace} name={name} />
        </div>
      </SectionLayout>
    </>
  )
}

function diskIdentification(d: { wwid: string; serial: string; byIdPath: string; busPath: string }, none: string): string {
  // architecture.md's own fallback chain: WWID, then serial, then by-id
  // path; any of the first three may be empty (node.proto's own comment).
  return d.wwid || d.serial || d.byIdPath || d.busPath || none
}

function EffectiveConfigSection({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [values, setValues] = useState<EffectiveConfigValue[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => {
    let cancelled = false
    setValues(null)
    setError(null)
    nodeClient.getEffectiveConfig({ machine: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setValues(res.config?.values ?? [])
      },
      (err) => {
        if (!cancelled) setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [namespace, name])

  // Grouped bottom-up (architecture.md, "Patch layers": "Patches apply
  // bottom-up: 1. System ... 2. Cluster, 3. role, 4. selector, 5.
  // machine"), which is also ConfigLayer's own numeric order - "The
  // effective-config view in the UI shows which layer each value came
  // from" is this section's one explicit design requirement.
  const groups = groupByLayer(values ?? [])

  return (
    <Section id="config" title={t('node.config')}>
        <p class="text-sm text-muted-foreground">
          {t('node.configIntroBefore')}
          <Link to="/patches" class="underline">
            {t('node.configIntroLink')}
          </Link>
          {t('node.configIntroAfter')}
        </p>
        {error && <ErrorAlert {...error} />}
        {!error && !values && <Skeleton className="h-24 w-full" />}
        {values && values.length === 0 && <EmptyNote>{t('node.configEmpty')}</EmptyNote>}
        {groups.map(([layer, group]) => (
          <div key={layer}>
            <h4 class="mb-1 text-sm font-medium">{enumLabel(ConfigLayer, layer)}</h4>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('node.colPath')}</TableHead>
                  <TableHead>{t('node.colValue')}</TableHead>
                  <TableHead>{t('node.colSource')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {group.map((v) => (
                  <TableRow key={v.path}>
                    <TableCell className="font-mono text-xs">{v.path}</TableCell>
                    <TableCell className="font-mono text-xs">
                      {v.value}
                      {/* The server masks any secret value before it reaches the browser
                          (EffectiveConfigValue.secret) - there is nothing here
                          that can or should reveal it. */}
                      {v.secret && (
                        <Badge variant="outline" className="ml-2">
                          {t('node.secret')}
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{v.origin?.source || '-'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        ))}
    </Section>
  )
}

// groupByLayer buckets values by origin.layer (ConfigLayer.UNSPECIFIED for
// a value with no origin at all) and returns the groups in ConfigLayer's
// own numeric order, which is already the bottom-up order the design
// describes - no separate sort table to maintain.
function groupByLayer(values: EffectiveConfigValue[]): [number, EffectiveConfigValue[]][] {
  const byLayer = new Map<number, EffectiveConfigValue[]>()
  for (const v of values) {
    const layer = v.origin?.layer ?? ConfigLayer.UNSPECIFIED
    const group = byLayer.get(layer)
    if (group) {
      group.push(v)
    } else {
      byLayer.set(layer, [v])
    }
  }
  return [...byLayer.entries()].sort((a, b) => a[0] - b[0])
}
