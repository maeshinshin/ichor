import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { clusterClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { Link, objectRefPath } from '@/router'

// Cluster list: status, rollout progress and hand-over state
// (architecture.md, "CLI and UI").
export function ClusterList() {
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
  if (!clusters) return <p class="mt-10 text-center text-sm text-muted-foreground">Loading clusters…</p>

  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Clusters</CardTitle>
      </CardHeader>
      <CardContent>
        {clusters.length === 0 ? (
          <p class="text-sm text-muted-foreground">No clusters yet.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Cluster</TableHead>
                <TableHead>Talos</TableHead>
                <TableHead>Kubernetes</TableHead>
                <TableHead>Control plane</TableHead>
                <TableHead>Rollout</TableHead>
                <TableHead>Hand-over</TableHead>
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
                    {c.controlPlaneSchedulable ? ' (schedulable)' : ''}
                  </TableCell>
                  <TableCell>
                    {c.rollout ? (
                      <Badge variant="secondary">
                        {enumLabel(RolloutKind, c.rollout.kind)}: {c.rollout.currentPhase || 'in progress'} (
                        {c.rollout.nodesDone}/{c.rollout.nodesTotal})
                      </Badge>
                    ) : (
                      <span class="text-muted-foreground">none</span>
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
