import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { DataTable, LinkRow, ListToolbar } from '@/components/ListToolbar'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { ErrorAlert } from '@/components/ErrorAlert'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { clusterClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { matchesQuery } from '@/lib/filter'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

// Cluster list: status, rollout progress and hand-over state
// (architecture.md, "CLI and UI").
export function ClusterList() {
  const t = useT()
  const [clusters, setClusters] = useState<Cluster[] | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)

  const [query, setQuery] = useState('')

  function reload() {
    clusterClient.listClusters({}).then(
      (res) => {
        setClusters(res.clusters)
        setError(null)
      },
      (err) => setError(describeError(err)),
    )
  }

  useEffect(reload, [])

  if (error) return <ErrorAlert {...error} />
  if (!clusters) return <PageSkeleton label={t('clusters.loading')} />

  const rows = clusters.filter((c) => matchesQuery(query, c.ref?.name, c.talosVersion, c.kubernetesVersion))

  return (
    <div>
      <PageHeader title={t('clusters.title')} />
      <ListToolbar query={query} onQuery={setQuery} shown={rows.length} total={clusters.length} onRefresh={reload} />
      {clusters.length === 0 ? (
        <EmptyNote>{t('clusters.empty')}</EmptyNote>
      ) : rows.length === 0 ? (
        <EmptyNote>{t('list.noMatch')}</EmptyNote>
      ) : (
        <DataTable>
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
              {rows.map((c) => (
                <LinkRow key={`${c.ref?.namespace}/${c.ref?.name}`} to={c.ref ? objectRefPath('/clusters', c.ref) : '/clusters'}>
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
                      <StatusText tone="progress">
                        {enumLabel(RolloutKind, c.rollout.kind)}: {c.rollout.currentPhase || t('clusters.inProgress')} (
                        {c.rollout.nodesDone}/{c.rollout.nodesTotal})
                      </StatusText>
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
                </LinkRow>
              ))}
            </TableBody>
          </Table>
        </DataTable>
      )}
    </div>
  )
}
