import { useEffect, useState } from 'preact/hooks'
import { PlusIcon } from 'lucide-preact'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConditionList, StatusBadge } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields, PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section, SectionLayout, type SectionLink } from '@/components/SectionNav'
import { ReadyCell } from '@/components/ReadyCell'
import { StatusText } from '@/components/Status'
import { OperationsPanel } from '@/components/OperationsPanel'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient, nodeClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { expiringCertificatesMessage, machineCounts } from '@/lib/overview'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

export function ClusterDetail({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [cluster, setCluster] = useState<Cluster | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)
  const [machines, setMachines] = useState<Machine[] | null>(null)
  // The kubelet version comes from each node's own read, so it fills in after the table.
  const [kubelets, setKubelets] = useState<Record<string, string>>({})

  useEffect(() => {
    let cancelled = false
    setMachines(null)
    setKubelets({})
    machineClient.listMachines({ cluster: { namespace, name } }).then(
      (res) => {
        if (cancelled) return
        setMachines(res.machines)
        for (const m of res.machines) {
          if (m.approvalState !== MachineApprovalState.APPROVED || !m.ref) continue
          const ref = m.ref
          nodeClient.getNode({ machine: ref }).then(
            (r) => {
              const v = r.node?.kubernetes?.kubeletVersion
              if (!cancelled && v) setKubelets((prev) => ({ ...prev, [ref.name]: v }))
            },
            () => {},
          )
        }
      },
      () => {
        if (!cancelled) setMachines([])
      },
    )
    return () => {
      cancelled = true
    }
  }, [namespace, name])

  // refresh reads the cluster and its nodes again without blanking the page (an operation just finished).
  function refresh() {
    clusterClient.getCluster({ cluster: { namespace, name } }).then(
      (res) => setCluster(res.cluster ?? null),
      () => {},
    )
    machineClient.listMachines({ cluster: { namespace, name } }).then(
      (res) => setMachines(res.machines),
      () => {},
    )
  }

  useEffect(() => {
    let cancelled = false
    setCluster(null)
    setError(null)
    clusterClient.getCluster({ cluster: { namespace, name } }).then(
      (res) => {
        if (!cancelled) setCluster(res.cluster ?? null)
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
  if (!cluster) return <PageSkeleton label={t('cluster.loading')} />

  const ready = cluster.conditions.find((c) => c.type === 'Ready')
  const certsExpiring = expiringCertificatesMessage(cluster.conditions)
  const approved = (machines ?? []).filter((m) => m.approvalState === MachineApprovalState.APPROVED)
  const pending = (machines ?? []).filter(
    (m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED,
  )
  const counts = machineCounts(
    approved.map((m) => ({ approval: 'approved' as const, ready: m.conditions.find((c) => c.type === 'Ready')?.status ?? '' })),
  )
  const addNodePath = `${objectRefPath('/clusters', { namespace, name })}/add-node`
  const sections: SectionLink[] = [
    { id: 'nodes', label: t('cluster.nodes') },
    { id: 'conditions', label: t('cluster.conditions') },
    { id: 'rollout', label: t('cluster.rollout') },
    { id: 'operations', label: t('cluster.operations') },
    { id: 'handover', label: t('cluster.handover') },
  ]

  return (
    <>
      <PageHeader
        title={cluster.ref?.name ?? name}
        kind={t('kind.cluster')}
        badges={
          <>
            {ready && <StatusBadge type={ready.type} status={ready.status} />}
            {cluster.rollout && (
              <StatusText tone="progress">
                {enumLabel(RolloutKind, cluster.rollout.kind)} {cluster.rollout.nodesDone}/{cluster.rollout.nodesTotal}
              </StatusText>
            )}
          </>
        }
        actions={
          <Button asChild size="sm">
            <Link to={addNodePath}>
              <PlusIcon />
              {t('cluster.addNode')}
            </Link>
          </Button>
        }
        meta={[
          {
            label: t('cluster.nodes'),
            value: machines
              ? [
                  t('cluster.nodeCount', { n: approved.length }),
                  t('cluster.nodeReady', { n: counts.ready }),
                  ...(pending.length > 0 ? [t('cluster.nodePending', { n: pending.length })] : []),
                ].join(' · ')
              : '',
          },
          { label: t('cluster.talosVersion'), value: cluster.talosVersion },
          { label: t('cluster.kubernetesVersion'), value: cluster.kubernetesVersion },
          { label: t('cluster.endpoint'), value: cluster.endpoint },
          {
            label: t('cluster.cpCount'),
            value: `${cluster.controlPlaneCount}${cluster.controlPlaneSchedulable ? ` ${t('clusters.schedulable')}` : ''}`,
          },
          { label: t('cluster.namespace'), value: cluster.ref?.namespace },
        ]}
      />
      {certsExpiring !== null && (
        <Alert className="mb-4">
          <AlertTitle>{t('cluster.certsExpiringTitle')}</AlertTitle>
          <AlertDescription>{certsExpiring}</AlertDescription>
        </Alert>
      )}
      <SectionLayout sections={sections}>
        <Section
          id="nodes"
          title={t('cluster.nodes')}
          actions={
            <Link to={addNodePath} class="text-sm text-muted-foreground hover:underline">
              {t('cluster.addNode')}
            </Link>
          }
        >
          {machines === null ? (
            <p class="text-muted-foreground">{t('machines.loading')}</p>
          ) : (
            <>
              {pending.length > 0 && (
                <p>
                  <Link to={addNodePath} class="text-amber-600 underline dark:text-amber-400">
                    {t('cluster.pendingLink', { n: pending.length })}
                  </Link>
                </p>
              )}
              {approved.length === 0 ? (
                <EmptyNote>{t('cluster.noNodes')}</EmptyNote>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('machines.colHostname')}</TableHead>
                      <TableHead>{t('col.role')}</TableHead>
                      <TableHead>{t('col.ready')}</TableHead>
                      <TableHead>{t('cluster.colStage')}</TableHead>
                      <TableHead>{t('cluster.colTalos')}</TableHead>
                      <TableHead>{t('cluster.colKubelet')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {approved.map((m) => (
                      <TableRow key={m.ref?.name}>
                        <TableCell>
                          {m.ref ? (
                            <Link to={objectRefPath('/nodes', m.ref)} class="font-medium hover:underline">
                              {m.hostname || m.ref.name}
                            </Link>
                          ) : (
                            m.hostname || '-'
                          )}
                        </TableCell>
                        <TableCell>{enumLabel(MachineRole, m.role)}</TableCell>
                        <TableCell>
                          <ReadyCell machine={m} />
                        </TableCell>
                        <TableCell>{m.stage || '-'}</TableCell>
                        <TableCell>{cluster.talosVersion || '-'}</TableCell>
                        <TableCell>{(m.ref && kubelets[m.ref.name]) || '-'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </>
          )}
        </Section>

        <Section id="conditions" title={t('cluster.conditions')}>
          <ConditionList conditions={cluster.conditions} />
        </Section>

        <Section id="rollout" title={t('cluster.rollout')}>
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
        </Section>

        <Section
          id="operations"
          title={t('cluster.operations')}
          actions={
            <Link to="/operations" class="text-sm text-muted-foreground hover:underline">
              {t('ops.title')}
            </Link>
          }
        >
          {cluster.ref && <OperationsPanel cluster={cluster} machines={machines === null ? null : approved} onSettled={refresh} />}
        </Section>

        <Section id="handover" title={t('cluster.handover')}>
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
        </Section>
      </SectionLayout>
    </>
  )
}
