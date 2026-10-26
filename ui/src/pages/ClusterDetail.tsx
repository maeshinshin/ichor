import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConditionList, StatusBadge } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields, PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section, SectionLayout, type SectionLink } from '@/components/SectionNav'
import { StartOperationDialog } from '@/components/StartOperationDialog'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { clusterClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { useT } from '@/lib/useT'
import { Link } from '@/router'

export function ClusterDetail({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [cluster, setCluster] = useState<Cluster | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)

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
  const sections: SectionLink[] = [
    { id: 'overview', label: t('page.overview') },
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
            {cluster.rollout && <Badge variant="outline">{enumLabel(RolloutKind, cluster.rollout.kind)}</Badge>}
          </>
        }
      />
      <SectionLayout sections={sections}>
        <Section id="overview" title={t('page.overview')}>
          <Fields>
            <Field label={t('cluster.namespace')} value={cluster.ref?.namespace} />
            <Field label={t('cluster.talosVersion')} value={cluster.talosVersion} />
            <Field label={t('cluster.kubernetesVersion')} value={cluster.kubernetesVersion} />
            <Field label={t('cluster.endpoint')} value={cluster.endpoint} />
            <Field label={t('cluster.cpCount')} value={String(cluster.controlPlaneCount)} />
            <Field label={t('cluster.cpSchedulable')} value={cluster.controlPlaneSchedulable ? t('common.yes') : t('common.no')} />
          </Fields>
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
          <div class="flex flex-wrap gap-2">
            {cluster.ref &&
              (['talos', 'kubernetes', 'ichor', 'cni', 'ca-rotation', 'shutdown'] as const).map((kind) => (
                <StartOperationDialog key={kind} kind={kind} cluster={cluster.ref!} />
              ))}
          </div>
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
