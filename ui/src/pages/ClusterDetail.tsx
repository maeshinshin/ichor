import { useEffect, useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import { ConditionList } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
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
  if (!cluster) return <p class="mt-10 text-center text-sm text-muted-foreground">{t('cluster.loading')}</p>

  return (
    <div class="mt-6 space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>{cluster.ref?.name}</CardTitle>
        </CardHeader>
        <CardContent className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <Field label={t('cluster.namespace')} value={cluster.ref?.namespace} />
          <Field label={t('cluster.talosVersion')} value={cluster.talosVersion} />
          <Field label={t('cluster.kubernetesVersion')} value={cluster.kubernetesVersion} />
          <Field label={t('cluster.endpoint')} value={cluster.endpoint} />
          <Field label={t('cluster.cpCount')} value={String(cluster.controlPlaneCount)} />
          <Field label={t('cluster.cpSchedulable')} value={cluster.controlPlaneSchedulable ? t('common.yes') : t('common.no')} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('cluster.conditions')}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <ConditionList conditions={cluster.conditions} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('cluster.rollout')}</CardTitle>
        </CardHeader>
        <CardContent className="text-sm">
          {cluster.rollout ? (
            <dl class="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Field label={t('cluster.rolloutKind')} value={enumLabel(RolloutKind, cluster.rollout.kind)} />
              <Field label={t('cluster.rolloutTarget')} value={cluster.rollout.targetVersion} />
              <Field label={t('cluster.rolloutPaused')} value={cluster.rollout.paused ? t('common.yes') : t('common.no')} />
              <Field label={t('cluster.rolloutProgress')} value={`${cluster.rollout.nodesDone}/${cluster.rollout.nodesTotal}`} />
              <Field label={t('cluster.rolloutPhase')} value={cluster.rollout.currentPhase} />
            </dl>
          ) : (
            <p class="text-muted-foreground">{t('cluster.noRollout')}</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex items-center justify-between">
          <CardTitle>{t('cluster.operations')}</CardTitle>
          <Link to="/operations" class="text-sm text-muted-foreground hover:underline">
            {t('ops.title')}
          </Link>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-2">
          {cluster.ref &&
            (['talos', 'kubernetes', 'ichor', 'cni', 'ca-rotation', 'shutdown'] as const).map((kind) => (
              <StartOperationDialog key={kind} kind={kind} cluster={cluster.ref!} />
            ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('cluster.handover')}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {cluster.handovers.length === 0 && <p class="text-muted-foreground">{t('cluster.noHandover')}</p>}
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
