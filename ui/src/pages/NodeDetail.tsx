import { useEffect, useState } from 'preact/hooks'
import { ArrowRightIcon, InboxIcon, TriangleAlertIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConditionList } from '@/components/ConditionList'
import { ReadyCell } from '@/components/ReadyCell'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { SectionCard } from '@/components/SectionCard'
import { PageTitle } from '@/components/PageTitle'
import { ToneBadge } from '@/components/ToneBadge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { NodeActions } from '@/components/NodeActions'
import { NodeLogs } from '@/components/NodeLogs'
import { TalosDetails } from '@/components/TalosDetails'
import { TalosRuntime } from '@/components/TalosRuntime'
import type { EffectiveConfigValue } from '@/gen/ichor/v1alpha1/node_pb'
import { ConfigLayer } from '@/gen/ichor/v1alpha1/node_pb'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { SetInstallDialog } from '@/components/SetInstallDialog'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient, nodeClient } from '@/lib/connect'
import { machineDisplayName } from '@/lib/machineName'
import { needsInstallDisk } from '@/lib/installDisk'
import { enumLabel } from '@/lib/enum'
import { clipValue, formatBytes, formatCores, formatUsage } from '@/lib/format'
import { nodeTabs, nodeTabSearch, parseNodeTab, type NodeTab } from '@/lib/nodeTabs'
import { setPageTitle, titleKey } from '@/lib/pageTitle'
import { useCluster } from '@/lib/useCluster'
import { useLoad } from '@/lib/useLoad'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { t } from '@/lib/i18n'
import { Link } from '@/router'

// Node detail: hardware, disks, network, live resource usage, Kubernetes
// state, effective config (origin-annotated, secrets masked) and logs
// (architecture.md, "CLI and UI").
export function NodeDetail({ name }: { name: string }) {
  const { cluster: ichorCluster } = useCluster()
  const namespace = ichorCluster?.ref?.namespace
  if (!namespace) return <PageSkeleton label={t('node.loading')} />
  return <NodePage namespace={namespace} name={name} />
}

// useNodeTab keeps the active tab in ?tab= so it is linkable and survives a reload.
function useNodeTab(): [NodeTab, (tab: NodeTab) => void] {
  const [tab, setTab] = useState(() => parseNodeTab(window.location.search))
  useEffect(() => {
    const onPop = () => setTab(parseNodeTab(window.location.search))
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  return [
    tab,
    (next) => {
      window.history.replaceState({}, '', window.location.pathname + nodeTabSearch(window.location.search, next))
      setTab(next)
    },
  ]
}

function NodePage({ namespace, name }: { namespace: string; name: string }) {
  const [tab, setTab] = useNodeTab()
  const [machine, setMachine] = useState<Machine | null>(null)
  const [cluster, setCluster] = useState<Cluster | null>(null)
  const perms = useMyPermissions({ namespace, name })

  const { data: node, error } = useLoad(() => nodeClient.getNode({ machine: { namespace, name } }).then((res) => res.node), [namespace, name])

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

  // The breadcrumb shows the display name instead of the UUID the URL carries.
  useEffect(() => {
    const key = titleKey('nodes', name)
    setPageTitle(key, machine ? machineDisplayName(machine) : undefined)
    return () => setPageTitle(key, undefined)
  }, [name, machine?.hostname, machine?.nodeName])

  if (error) return <ErrorAlert {...error} />
  if (!node) return <PageSkeleton label={t('node.loading')} />

  const ready = machine?.conditions.find((c) => c.type === 'Ready')
  const approved = machine?.approvalState === MachineApprovalState.APPROVED

  return (
    <>
      <PageTitle
        title={machine ? machineDisplayName(machine) : name}
        description={t('kind.machine')}
        badges={
          machine && (
            <>
              <ToneBadge tone={approved ? 'good' : 'warn'}>{enumLabel(MachineApprovalState, machine.approvalState)}</ToneBadge>
              {ready && <ReadyCell machine={machine} />}
              {machine.stage && <Badge variant="outline">{machine.stage}</Badge>}
            </>
          )
        }
        actions={
          <>
            {machine?.cluster && (
              <Button asChild variant="outline" size="sm">
                <Link to={'/'}>
                  {t('node.openCluster')}
                  <ArrowRightIcon data-icon="inline-end" aria-hidden="true" />
                </Link>
              </Button>
            )}
            {machine && (
              <NodeActions
                machine={{ namespace, name }}
                displayName={machineDisplayName(machine)}
                perms={perms}
                controlPlane={machine.role === MachineRole.CONTROL_PLANE}
                approved={approved}
                deleting={machine.deleting}
                onStarted={loadMachine}
              />
            )}
          </>
        }
      />
      <Card size="sm" className="mb-4">
        <CardContent>
          <Fields>
            <Field label={t('col.role')} value={machine ? enumLabel(MachineRole, machine.role) : undefined} />
            <Field label={t('col.cluster')}>
              {machine?.cluster ? (
                <Button asChild variant="link" size="sm">
                  <Link to={'/'}>{machine.cluster.name}</Link>
                </Button>
              ) : undefined}
            </Field>
            <Field label={t('col.endpoint')} value={machine?.endpoint} />
            <Field label={t('cluster.talosVersion')} value={cluster?.talosVersion} />
            <Field label={t('node.k8sKubelet')} value={node.kubernetes?.kubeletVersion} />
          </Fields>
        </CardContent>
      </Card>
      {machine && needsInstallDisk({ ...machine, approved }) && (
        <Alert className="mb-4">
          <TriangleAlertIcon />
          <AlertTitle>{t('installDisk.waitingTitle')}</AlertTitle>
          <AlertDescription>
            <p>{t('installDisk.waitingBody')}</p>
            <div class="mt-2">
              <SetInstallDialog
                machine={machine}
                disks={node.disks}
                onDone={loadMachine}
              />
            </div>
          </AlertDescription>
        </Alert>
      )}
      <Tabs value={tab} onValueChange={(v) => setTab(parseNodeTab(`?tab=${v}`))}>
        <TabsList variant="line" aria-label={t('node.tabs')} className="w-full flex-wrap justify-start group-data-horizontal/tabs:h-auto">
          {nodeTabs.map((id) => (
            <TabsTrigger key={id} value={id} className="flex-none">
              {t(`node.tab.${id}`)}
            </TabsTrigger>
          ))}
        </TabsList>
        {/* Radix mounts only the active panel, so a tab's data loads when it is first opened. */}
        <TabsContent value="summary" className="space-y-4">
        <SectionCard id="conditions" title={t('cluster.conditions')}>
          <ConditionList conditions={machine?.conditions ?? []} />
        </SectionCard>

        <SectionCard id="hardware" title={t('node.hardware')}>
          <Fields>
            <Field label={t('node.manufacturer')} value={node.hardware?.manufacturer} />
            <Field label={t('node.model')} value={node.hardware?.model} />
            <Field label={t('node.serialNumber')} value={node.hardware?.serialNumber} />
            <Field label={t('node.cpuCores')} value={node.hardware ? String(node.hardware.cpuCores) : undefined} />
            <Field label={t('node.memory')} value={node.hardware ? formatBytes(node.hardware.memoryTotalBytes) : undefined} />
          </Fields>
        </SectionCard>

        <SectionCard id="usage" title={t('node.usage')}>
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
            <EmptyNote icon={InboxIcon}>{t('node.noUsage')}</EmptyNote>
          )}
        </SectionCard>
        </TabsContent>
        <TabsContent value="storage" className="space-y-4">
          <TalosDetails namespace={namespace} name={name} part="storage" />
        </TabsContent>
        <TabsContent value="talos" className="space-y-4">
          <TalosDetails namespace={namespace} name={name} part="talos" />
          <TalosRuntime namespace={namespace} name={name} part="talos" />
        </TabsContent>
        <TabsContent value="kubernetes" className="space-y-4">
          <TalosRuntime namespace={namespace} name={name} part="kubernetes" />
        </TabsContent>
        <TabsContent value="config" className="space-y-4">
          <EffectiveConfigSection namespace={namespace} name={name} />
        </TabsContent>
        <TabsContent value="logs" className="space-y-4">
          <NodeLogs namespace={namespace} name={name} />
        </TabsContent>
      </Tabs>
    </>
  )
}

function EffectiveConfigSection({ namespace, name }: { namespace: string; name: string }) {
  const { data: values, error } = useLoad(
    () => nodeClient.getEffectiveConfig({ machine: { namespace, name } }).then((res) => res.config?.values ?? []),
    [namespace, name],
  )

  // Grouped bottom-up (architecture.md, "Patch layers": "Patches apply
  // bottom-up: 1. System ... 2. Cluster, 3. role, 4. selector, 5.
  // machine"), which is also ConfigLayer's own numeric order - "The
  // effective-config view in the UI shows which layer each value came
  // from" is this section's one explicit design requirement.
  const groups = groupByLayer(values ?? [])

  return (
    <SectionCard
      id="config"
      title={t('node.config')}
      description={
        <>
          {t('node.configIntroBefore')}
          <Link to="/config" className="underline">
            {t('node.configIntroLink')}
          </Link>
          {t('node.configIntroAfter')}
        </>
      }
    >
      {error && <ErrorAlert {...error} />}
      {!error && !values && <Skeleton className="h-24 w-full" />}
      {values && values.length === 0 && <EmptyNote icon={InboxIcon}>{t('node.configEmpty')}</EmptyNote>}
      {groups.map(([layer, group]) => (
        <SectionCard key={layer} size="sm" title={enumLabel(ConfigLayer, layer)}>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('node.colPath')}</TableHead>
                <TableHead className="max-md:hidden">{t('node.colValue')}</TableHead>
                <TableHead className="max-md:hidden">{t('node.colSource')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {group.map((v) => (
                <TableRow key={v.path}>
                  <TableCell className="font-mono whitespace-normal break-words max-md:w-full max-md:max-w-0 max-md:wrap-anywhere md:w-1/3">
                    {v.path}
                    <div class="font-sans text-muted-foreground md:hidden">{v.origin?.source || '-'}</div>
                    <div class="mt-1 text-foreground md:hidden">
                      {clipValue(v.value)}
                      {v.secret && (
                        <>
                          {' '}
                          <Badge variant="outline">{t('node.secret')}</Badge>
                        </>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="font-mono whitespace-normal wrap-anywhere max-md:hidden" title={v.value.length > 200 ? v.value : undefined}>
                    {clipValue(v.value)}
                    {/* The server masks any secret value before it reaches the browser
                        (EffectiveConfigValue.secret) - there is nothing here
                        that can or should reveal it. */}
                    {v.secret && (
                      <>
                        {' '}
                        <Badge variant="outline">{t('node.secret')}</Badge>
                      </>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap max-md:hidden">{v.origin?.source || '-'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </SectionCard>
      ))}
    </SectionCard>
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
    byLayer.set(layer, [...(byLayer.get(layer) ?? []), v])
  }
  return [...byLayer.entries()].sort((a, b) => a[0] - b[0])
}
