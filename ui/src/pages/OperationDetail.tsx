import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section } from '@/components/SectionNav'
import { StatusText } from '@/components/Status'
import { OperationControls } from '@/components/OperationControls'
import { CARotationCredentials } from '@/components/CARotationCredentials'
import { NodeAction, OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { credentialsPanel } from '@/lib/credentials'
import { describeError } from '@/lib/errors'
import { restoreStepProgress } from '@/lib/etcdRestore'
import { formatTimestamp } from '@/lib/format'
import { caStageProgress, kubernetesStageProgress, operationState, pollIntervalMs, progressCounts } from '@/lib/operations'
import { opStateTone } from '@/lib/status'
import { useCluster } from '@/lib/useCluster'
import { useT } from '@/lib/useT'
import { Link, navigate } from '@/router'

type ErrorShape = { title: string; detail: string }

// Progress view of one operation (architecture.md, "Upgrades": the three
// upgrade kinds share a progress view): the phase and each machine's state
// exactly as the controller recorded them in status. A machine that does not
// appear has not been reached. It polls while the operation can still change.
export function OperationDetail({ name }: { name: string }) {
  const t = useT()
  const { cluster, error: clusterError } = useCluster()
  const namespace = cluster?.ref?.namespace
  const [op, setOp] = useState<Operation | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => {
    if (!namespace) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    async function load() {
      try {
        const res = await operationClient.getOperation({ operation: { namespace, name } })
        if (cancelled) return
        setOp(res.operation ?? null)
        setError(null)
        const next = res.operation ? pollIntervalMs(operationState(res.operation.phase, res.operation.failureReason)) : null
        if (next !== null) timer = setTimeout(load, next)
      } catch (err) {
        if (cancelled) return
        setError(describeError(err))
        timer = setTimeout(load, 10_000)
      }
    }

    setOp(null)
    load()
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [namespace, name])

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (error && !op) return <ErrorAlert {...error} />
  if (!op) return <PageSkeleton label={t('ops.loading')} />

  const state = operationState(op.phase, op.failureReason)
  const { done, running } = progressCounts(op.machines)
  const position = op.caRotationStage === '' ? null : caStageProgress(op.caRotationStage)
  const stage = position ? { name: op.caRotationStage, ...position } : null
  const kubeStage = kubernetesStageProgress(op.kubernetesStage)
  const restoreStep = restoreStepProgress(op.etcdRestoreStep)
  const panel = credentialsPanel(op.kind === OperationKind.CA_ROTATION, op.caRotationStage, state === 'done')

  return (
    <>
      <PageHeader
        title={op.ref?.name ?? name}
        kind={t('kind.operation')}
        badges={
          <>
            <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
            {op.machines.length > 0 && <span class="text-sm text-muted-foreground">{t('ops.progress', { done, running })}</span>}
            {stage && <Badge variant="outline">{t('ops.caStage', { stage: stage.name, n: stage.index, total: stage.total })}</Badge>}
            {restoreStep && (
              <Badge variant="outline">
                {t('restore.stepName', { n: restoreStep.index, total: restoreStep.total, name: t(`restore.stepName.${restoreStep.name}`) })}
              </Badge>
            )}
            {kubeStage && (
              <Badge variant="outline">
                {t('ops.kubeStage', { stage: t(`ops.kubeStageName.${kubeStage.name}`), n: kubeStage.index, total: kubeStage.total })}
              </Badge>
            )}
          </>
        }
        meta={[
          { label: t('ops.colKind'), value: enumLabel(OperationKind, op.kind) },
          ...(op.kind === OperationKind.NODE
            ? [
                { label: t('node.action.actionLabel'), value: enumLabel(NodeAction, op.nodeAction) },
                {
                  label: t('node.action.nodeLabel'),
                  value: op.nodeMachine ? (
                    <Link to={`/nodes/${op.nodeMachine.name}`} class="hover:underline">
                      {op.nodeMachine.name}
                    </Link>
                  ) : undefined,
                },
                { label: t('node.action.progress'), value: op.nodeMessage },
              ]
            : op.kind === OperationKind.ETCD_RESTORE
              ? [
                  { label: t('restore.snapshotLabel'), value: op.etcdRestoreSnapshot },
                  { label: t('restore.snapshotTaken'), value: formatTimestamp(op.etcdRestoreSnapshotTime) },
                  { label: t('restore.backupLabel'), value: op.etcdRestoreBackup?.name },
                  { label: t('restore.progress'), value: op.etcdRestoreMessage },
                ]
              : [{ label: t('ops.maxUnavailable'), value: String(op.maxUnavailable || 1) }]),
        ]}
        actions={<OperationControls op={op} onChanged={setOp} onDeleted={() => navigate('/operations')} />}
      />
      <div class="space-y-8">
        <div class="space-y-4 empty:hidden">
          {panel === 'gate' && (
            <Alert variant="destructive">
              <AlertTitle>{t('ops.awaitingCredentialsTitle')}</AlertTitle>
              <AlertDescription>
                {t('ops.awaitingCredentials', { name: op.cluster?.name ?? '' })}
                <CARotationCredentials op={op} panel={panel} onChanged={setOp} />
              </AlertDescription>
            </Alert>
          )}
          {panel === 'issued' && (
            <Alert>
              <AlertTitle>{t('creds.issuedTitle')}</AlertTitle>
              <AlertDescription>
                {t('creds.issued')}
                <CARotationCredentials op={op} panel={panel} onChanged={setOp} />
              </AlertDescription>
            </Alert>
          )}
          {op.kind === OperationKind.CA_ROTATION && state === 'done' && (
            <Alert>
              <AlertTitle>{t('ops.plan.bundleStaleTitle')}</AlertTitle>
              <AlertDescription>{t('ops.plan.bundleStale', { name: op.cluster?.name ?? '' })}</AlertDescription>
            </Alert>
          )}
          {state === 'failed' && (
            <Alert variant="destructive">
              <AlertTitle>{t('ops.failure')}</AlertTitle>
              <AlertDescription>
                {op.failureReason}
                <br />
                {t('ops.failureHint')}
              </AlertDescription>
            </Alert>
          )}
          {error && <ErrorAlert {...error} />}
        </div>

        <Section id="machines" title={t('ops.machines')}>
          {op.machines.length === 0 ? (
            <EmptyNote>{t(op.kind === OperationKind.ICHOR ? 'ops.ichorJob' : 'ops.noMachines')}</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.name')}</TableHead>
                  <TableHead>{t('ops.colState')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {op.machines.map((m) => (
                  <TableRow key={m.name}>
                    <TableCell>{m.name}</TableCell>
                    <TableCell>
                      {/* The controller's own word (InProgress, Completed), as received. */}
                      <StatusText tone={m.phase === 'Completed' ? 'good' : m.phase === 'InProgress' ? 'progress' : 'neutral'}>{m.phase}</StatusText>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </Section>
      </div>
    </>
  )
}
