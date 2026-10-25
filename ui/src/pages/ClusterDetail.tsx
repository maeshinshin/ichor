import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import { ErrorAlert } from '@/components/ErrorAlert'
import { HandoverPhase, RolloutKind, type Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { clusterClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'

export function ClusterDetail({ namespace, name }: { namespace: string; name: string }) {
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
  if (!cluster) return <p class="mt-10 text-center text-sm text-muted-foreground">Loading cluster…</p>

  return (
    <div class="mt-6 space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>{cluster.ref?.name}</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <Field label="Namespace" value={cluster.ref?.namespace} />
          <Field label="Talos version" value={cluster.talosVersion} />
          <Field label="Kubernetes version" value={cluster.kubernetesVersion} />
          <Field label="Endpoint" value={cluster.endpoint} />
          <Field label="Control plane count" value={String(cluster.controlPlaneCount)} />
          <Field label="Control plane schedulable" value={cluster.controlPlaneSchedulable ? 'yes' : 'no'} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Conditions</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {cluster.conditions.length === 0 && <p class="text-muted-foreground">No conditions reported.</p>}
          {cluster.conditions.map((c) => (
            <div key={c.type} class="flex flex-wrap items-center gap-2">
              <Badge variant={c.status === 'True' ? 'secondary' : 'outline'}>{c.type}</Badge>
              <span class="text-muted-foreground">{c.status}</span>
              {c.message && <span>- {c.message}</span>}
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Rollout</CardTitle>
        </CardHeader>
        <CardContent className="text-sm">
          {cluster.rollout ? (
            <dl class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Field label="Kind" value={enumLabel(RolloutKind, cluster.rollout.kind)} />
              <Field label="Target version" value={cluster.rollout.targetVersion} />
              <Field label="Paused" value={cluster.rollout.paused ? 'yes' : 'no'} />
              <Field label="Progress" value={`${cluster.rollout.nodesDone}/${cluster.rollout.nodesTotal}`} />
              <Field label="Current phase" value={cluster.rollout.currentPhase} />
            </dl>
          ) : (
            <p class="text-muted-foreground">No rollout in progress.</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Hand-over</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {cluster.handovers.length === 0 && <p class="text-muted-foreground">No hand-over components tracked.</p>}
          {cluster.handovers.map((h, i) => (
            <div key={h.component}>
              {i > 0 && <Separator className="my-2" />}
              <div class="flex items-center gap-2">
                <span class="font-medium">{h.component}</span>
                <Badge variant="outline">{enumLabel(HandoverPhase, h.phase)}</Badge>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}

function Field({ label, value }: { label: string; value: string | undefined }) {
  return (
    <div>
      <dt class="text-xs text-muted-foreground">{label}</dt>
      <dd>{value || '-'}</dd>
    </div>
  )
}
