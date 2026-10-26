import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { caStageProgress, operationState, pollIntervalMs, progressCounts, stateBadge } from '@/lib/operations'
import { useT } from '@/lib/useT'
import { Link, navigate } from '@/router'

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
  if (!op) return <p class="mt-10 text-center text-sm text-muted-foreground">{t('ops.loading')}</p>

  const state = operationState(op.phase, op.failureReason)
  const { done, running } = progressCounts(op.machines)
  const position = op.caRotationStage === '' ? null : caStageProgress(op.caRotationStage)
  const stage = position ? { name: op.caRotationStage, ...position } : null

  return (
    <div class="mt-6 space-y-4">
      <Link to="/operations" class="text-sm text-muted-foreground hover:underline">
        {t('ops.back')}
      </Link>
      <Card>
        <CardHeader className="flex flex-wrap items-center justify-between gap-2">
          <CardTitle>{op.ref?.name}</CardTitle>
          <OperationControls op={op} onChanged={setOp} onDeleted={() => navigate('/operations')} />
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div class="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{enumLabel(OperationKind, op.kind)}</Badge>
            <Badge variant={stateBadge(state)}>{t(`ops.state.${state}`)}</Badge>
            <span class="text-muted-foreground">{t('ops.progress', { done, running })}</span>
            {stage && <Badge variant="outline">{t('ops.caStage', { stage: stage.name, n: stage.index, total: stage.total })}</Badge>}
          </div>
          <dl class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div>
              <dt class="text-xs text-muted-foreground">{t('ops.cluster')}</dt>
              <dd>{op.cluster?.name}</dd>
            </div>
            <div>
              <dt class="text-xs text-muted-foreground">{t('ops.maxUnavailable')}</dt>
              <dd>{op.maxUnavailable || 1}</dd>
            </div>
          </dl>
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
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('ops.machines')}</CardTitle>
        </CardHeader>
        <CardContent>
          {op.machines.length === 0 ? (
            <p class="text-sm text-muted-foreground">{t('ops.noMachines')}</p>
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
        </CardContent>
      </Card>
    </div>
  )
}
