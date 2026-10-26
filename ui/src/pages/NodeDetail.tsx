import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConditionList, StatusBadge } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields, PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { Section, SectionLayout, type SectionLink } from '@/components/SectionNav'
import { NodeActions } from '@/components/NodeActions'
import { NodeLogs } from '@/components/NodeLogs'
import { NodeActionsMenu, NodeActionStartedNotice, type NodeActionStarted } from '@/components/NodeActionsMenu'
import { TalosDetails } from '@/components/TalosDetails'
import { TalosRuntime } from '@/components/TalosRuntime'
import type { EffectiveConfigValue } from '@/gen/ichor/v1alpha1/node_pb'
import { ConfigLayer, type NodeDetail as NodeDetailMessage } from '@/gen/ichor/v1alpha1/node_pb'
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
import { formatBytes, formatCores, formatUsage } from '@/lib/format'
import { setPageTitle, titleKey } from '@/lib/pageTitle'
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
  const [started, setStarted] = useState<NodeActionStarted | null>(null)

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

  // The breadcrumb shows the hostname instead of the UUID the URL carries.
  useEffect(() => {
    const key = titleKey('nodes', namespace, name)
    setPageTitle(key, machine?.hostname)
    return () => setPageTitle(key, undefined)
  }, [namespace, name, machine?.hostname])

  if (error) return <ErrorAlert {...error} />
  if (!node) return <PageSkeleton label={t('node.loading')} />

  const ready = machine?.conditions.find((c) => c.type === 'Ready')
  const sections: SectionLink[] = [
    { id: 'conditions', label: t('cluster.conditions') },
    { id: 'hardware', label: t('node.hardware') },
    { id: 'usage', label: t('node.usage') },
    { id: 'talos', label: t('talos.system') },
    { id: 'storage', label: t('node.disks') },
    { id: 'network', label: t('node.network') },
    { id: 'services', label: t('talos.services') },
    { id: 'extensions', label: t('talos.extensions') },
    { id: 'k8s-node', label: t('rt.k8s') },
    { id: 'etcd', label: t('rt.etcd') },
    { id: 'processes', label: t('rt.processes') },
    { id: 'containers', label: t('rt.containers') },
    { id: 'netstate', label: t('rt.netstate') },
    { id: 'kernel', label: t('rt.kernel') },
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
              <StatusText tone={machine.approvalState === MachineApprovalState.APPROVED ? 'good' : 'warn'}>
                {enumLabel(MachineApprovalState, machine.approvalState)}
              </StatusText>
              {ready && <StatusBadge type={ready.type} status={ready.status} />}
              {machine.stage && <span class="text-sm text-muted-foreground">{machine.stage}</span>}
            </>
          )
        }
        meta={[
          { label: t('col.role'), value: machine ? enumLabel(MachineRole, machine.role) : undefined },
          {
            label: t('col.cluster'),
            value: machine?.cluster ? (
              <Link to={objectRefPath('/clusters', machine.cluster)} class="hover:underline">
                {machine.cluster.name}
              </Link>
            ) : undefined,
          },
          { label: t('col.endpoint'), value: machine?.endpoint },
          { label: t('cluster.talosVersion'), value: cluster?.talosVersion },
          { label: t('node.k8sKubelet'), value: node.kubernetes?.kubeletVersion },
        ]}
        actions={
          <>
            {machine?.cluster && (
              <Button asChild variant="outline" size="sm">
                <Link to={objectRefPath('/clusters', machine.cluster)}>{t('node.openCluster')}</Link>
              </Button>
            )}
            {machine && <NodeActions machine={{ namespace, name }} controlPlane={machine.role === MachineRole.CONTROL_PLANE} />}
            {machine && (
              <NodeActionsMenu
                machine={{
                  namespace,
                  name,
                  approved: machine.approvalState === MachineApprovalState.APPROVED,
                  deleting: machine.deleting,
                  role: machine.role === MachineRole.CONTROL_PLANE ? 'controlplane' : 'worker',
                }}
                onStarted={(s) => {
                  setStarted(s)
                  loadMachine()
                }}
              />
            )}
          </>
        }
      />
      {started && <NodeActionStartedNotice started={started} onDismiss={() => setStarted(null)} />}
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

        <Section id="usage" title={t('node.usage')}>
          {node.resourceUsage ? (
            <Fields>
              <Field label={t('node.cpuUsed')} value={formatCores(node.resourceUsage.cpuCoresUsed, node.resourceUsage.cpuSampled)} />
              <Field
                label={t('node.memory')}
                value={formatUsage(node.resourceUsage.memoryUsedBytes, node.resourceUsage.memoryTotalBytes)}
              />
              <Field
                label={t('node.filesystem')}
                value={formatUsage(node.resourceUsage.filesystemUsedBytes, node.resourceUsage.filesystemTotalBytes)}
              />
            </Fields>
          ) : (
            <EmptyNote>{t('node.noUsage')}</EmptyNote>
          )}
        </Section>

        <TalosDetails namespace={namespace} name={name} />
        <TalosRuntime namespace={namespace} name={name} />
        <EffectiveConfigSection namespace={namespace} name={name} />
        <div id="logs" class="scroll-mt-24">
          <NodeLogs namespace={namespace} name={name} />
        </div>
      </SectionLayout>
    </>
  )
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
