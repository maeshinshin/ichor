import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ErrorAlert } from '@/components/ErrorAlert'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { clusterClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

// Cluster list: status, rollout progress and hand-over state
// (architecture.md, "CLI and UI").
export function ClusterList() {
  const t = useT()
  const [clusters, setClusters] = useState<Cluster[] | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)

  useEffect(() => {
    let cancelled = false
    clusterClient.listClusters({}).then(
      (res) => {
        if (!cancelled) setClusters(res.clusters)
      },
      (err) => {
        if (!cancelled) setError(describeError(err))
      },
    )
    return () => {
      cancelled = true
    }
  }, [])

  if (error) return <ErrorAlert {...error} />
  if (!clusters) return <PageSkeleton label={t('clusters.loading')} />

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('clusters.title')}</CardTitle>
      </CardHeader>
      <CardContent>
        {clusters.length === 0 ? (
          <EmptyNote>{t('clusters.empty')}</EmptyNote>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('col.cluster')}</TableHead>
                <TableHead>{t('clusters.colTalos')}</TableHead>
                <TableHead>{t('clusters.colKubernetes')}</TableHead>
                <TableHead>{t('clusters.colControlPlane')}</TableHead>
                <TableHead>{t('clusters.colRollout')}</TableHead>
                <TableHead>{t('clusters.colHandover')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {clusters.map((c) => (
                <TableRow key={`${c.ref?.namespace}/${c.ref?.name}`}>
                  <TableCell>
                    {c.ref ? (
                      <Link to={objectRefPath('/clusters', c.ref)} class="font-medium hover:underline">
                        {c.ref.name}
                      </Link>
                    ) : (
                      '-'
                    )}
                  </TableCell>
                  <TableCell>{c.talosVersion || '-'}</TableCell>
                  <TableCell>{c.kubernetesVersion || '-'}</TableCell>
                  <TableCell>
                    {c.controlPlaneCount}
                    {c.controlPlaneSchedulable ? ` ${t('clusters.schedulable')}` : ''}
                  </TableCell>
                  <TableCell>
                    {c.rollout ? (
                      <Badge variant="secondary">
                        {enumLabel(RolloutKind, c.rollout.kind)}: {c.rollout.currentPhase || t('clusters.inProgress')} (
                        {c.rollout.nodesDone}/{c.rollout.nodesTotal})
                      </Badge>
                    ) : (
                      <span class="text-muted-foreground">{t('common.none')}</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <div class="flex flex-wrap gap-1">
                      {c.handovers.length === 0 && <span class="text-muted-foreground">-</span>}
                      {c.handovers.map((h) => (
                        <Badge key={h.component} variant="outline">
                          {h.component}: {enumLabel(HandoverPhase, h.phase)}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  )
}
