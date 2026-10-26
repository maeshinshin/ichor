import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields, PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { Section } from '@/components/SectionNav'
import { OperationControls } from '@/components/OperationControls'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { caStageProgress, operationState, pollIntervalMs, progressCounts, stateBadge } from '@/lib/operations'
import { useT } from '@/lib/useT'
import { navigate } from '@/router'

type ErrorShape = { title: string; detail: string }

// Progress view of one operation (architecture.md, "Upgrades": the three
// upgrade kinds share a progress view): the phase and each machine's state
// exactly as the controller recorded them in status. A machine that does not
// appear has not been reached. It polls while the operation can still change.
export function OperationDetail({ namespace, name }: { namespace: string; name: string }) {
  const t = useT()
  const [op, setOp] = useState<Operation | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)

  useEffect(() => {
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

  if (error && !op) return <ErrorAlert {...error} />
  if (!op) return <PageSkeleton label={t('ops.loading')} />

  const state = operationState(op.phase, op.failureReason)
  const { done, running } = progressCounts(op.machines)
  const position = op.caRotationStage === '' ? null : caStageProgress(op.caRotationStage)
  const stage = position ? { name: op.caRotationStage, ...position } : null

  return (
    <>
      <PageHeader
        title={op.ref?.name ?? name}
        kind={t('kind.operation')}
        badges={
          <>
            <Badge variant="outline">{enumLabel(OperationKind, op.kind)}</Badge>
            <Badge variant={stateBadge(state)}>{t(`ops.state.${state}`)}</Badge>
            <span class="text-sm text-muted-foreground">{t('ops.progress', { done, running })}</span>
            {stage && <Badge variant="outline">{t('ops.caStage', { stage: stage.name, n: stage.index, total: stage.total })}</Badge>}
          </>
        }
        actions={<OperationControls op={op} onChanged={setOp} onDeleted={() => navigate('/operations')} />}
      />
      <div class="space-y-6">
        <Section id="overview" title={t('page.overview')}>
          <Fields>
            <Field label={t('ops.cluster')} value={op.cluster?.name} />
            <Field label={t('ops.maxUnavailable')} value={String(op.maxUnavailable || 1)} />
          </Fields>
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
        </Section>

        <Section id="machines" title={t('ops.machines')}>
          {op.machines.length === 0 ? (
            <EmptyNote>{t('ops.noMachines')}</EmptyNote>
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
                      <Badge variant={m.phase === 'Completed' ? 'secondary' : 'outline'}>{m.phase}</Badge>
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
