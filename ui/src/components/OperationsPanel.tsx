import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'
import { InfoIcon } from 'lucide-preact'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { StartOperationDialog } from '@/components/StartOperationDialog'
import { StatusText } from '@/components/Status'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import type { Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { blockingOperation, caExpiry, currentOperation, recentOperations, rebootPendingCount, type OpRow } from '@/lib/clusterOps'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { formatTimestamp } from '@/lib/format'
import { operationState, progressCounts } from '@/lib/operations'
import { opStateTone } from '@/lib/status'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

type Row = OpRow & { op: Operation }
type ErrorShape = { title: string; detail: string }

const toRow = (op: Operation): Row => ({
  op,
  name: op.ref?.name ?? '',
  phase: op.phase,
  failureReason: op.failureReason,
  isNode: op.kind === OperationKind.NODE,
  created: Number(op.createdAt?.seconds ?? 0n),
})

// OperationsPanel is the Operations section of the cluster page: what runs now, each action with the context it needs (the
// version now, the nodes waiting for a reboot, the CA dates) and whether it can
// start, and the last few operations. It only reads; every change is made
// through the dialogs, which ask the server for a plan first.
export function OperationsPanel({
  cluster,
  machines,
  onSettled,
}: {
  cluster: Cluster
  // machines are the approved ones; null while they load.
  machines: Machine[] | null
  // onSettled is called when an operation of this cluster stops being unfinished,
  // so the page reads the versions and nodes again.
  onSettled: () => void
}) {
  const t = useT()
  const ref = cluster.ref!
  const [ops, setOps] = useState<Row[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [ichorVersion, setIchorVersion] = useState<string | null>(null)
  const wasBusy = useRef(false)

  async function reload() {
    try {
      const res = await operationClient.listOperations({ cluster: ref })
      setOps(res.operations.map(toRow))
      setError(null)
    } catch (err) {
      setError(describeError(err))
    }
  }

  async function loadIchor() {
    try {
      const plan = await operationClient.planOperation({ cluster: ref, kind: OperationKind.ICHOR, target: '' })
      setIchorVersion(plan.current)
    } catch {
      setIchorVersion(null)
    }
  }

  useEffect(() => {
    reload()
    loadIchor()
    const id = setInterval(reload, 5000)
    return () => clearInterval(id)
  }, [ref.namespace, ref.name])

  const current = ops ? currentOperation(ops) : null
  const busy = ops ? blockingOperation(ops) : null
  useEffect(() => {
    if (busy) {
      wasBusy.current = true
    } else if (wasBusy.current && ops) {
      wasBusy.current = false
      onSettled()
      loadIchor()
    }
  }, [busy !== null, ops !== null])

  const waiting = (machines ?? []).filter((m) => rebootPendingCount([{ rebootPaths: m.rebootPaths, readyReason: readyReason(m) }]) > 0)
  const busyReason = busy ? t('ops.disabledBusy') : undefined
  const kubeProxy = cluster.kubeProxyMode || 'enabled'

  return (
    <div class="space-y-4">
      {error && <ErrorAlert {...error} />}
      {current && <CurrentOperation row={current} total={machines?.length ?? 0} onChanged={reload} />}

      <Group title={t('ops.group.versions')} hint={t('ops.group.versionsHint')}>
        <Action
          title={t('ops.row.talos')}
          hint={t('ops.row.talosHint')}
          detail={t('ops.installed', { version: cluster.talosVersion })}
          reason={busyReason}
        >
          <StartOperationDialog kind="talos" cluster={ref} current={cluster.talosVersion} label={t('ops.action.upgrade')} disabled={!!busy} />
        </Action>
        <Action
          title={t('ops.row.kubernetes')}
          hint={t('ops.row.kubernetesHint')}
          detail={t('ops.installed', { version: cluster.kubernetesVersion })}
          reason={busyReason}
        >
          <StartOperationDialog kind="kubernetes" cluster={ref} current={cluster.kubernetesVersion} label={t('ops.action.upgrade')} disabled={!!busy} />
        </Action>
        <Action
          title={t('ops.row.ichor')}
          hint={t('ops.row.ichorHint')}
          detail={ichorVersion ? t('ops.installed', { version: ichorVersion }) : t('ops.installedUnknown')}
          reason={busyReason}
        >
          <StartOperationDialog kind="ichor" cluster={ref} current={ichorVersion ?? ''} label={t('ops.action.upgrade')} disabled={!!busy} />
        </Action>
      </Group>

      <Group title={t('ops.group.config')} hint={t('ops.group.configHint')}>
        <Action
          title={t('ops.row.config')}
          hint={t('ops.row.configHint')}
          detail={
            machines === null
              ? ''
              : waiting.length > 0
                ? t('ops.rebootWaiting', { n: waiting.length, names: waiting.map((m) => m.hostname || m.ref?.name).join(', ') })
                : t('ops.rebootNone')
          }
          reason={busyReason ?? (machines !== null && waiting.length === 0 ? t('ops.disabledNothing') : undefined)}
        >
          <StartOperationDialog kind="config" cluster={ref} label={t('ops.action.apply')} disabled={!!busy || machines === null || waiting.length === 0} />
        </Action>
        <Action
          title={t('ops.row.cni')}
          hint={t('ops.row.cniHint')}
          detail={t(kubeProxy === 'replaced' ? 'ops.modeReplaced' : 'ops.modeEnabled')}
          reason={busyReason}
        >
          <StartOperationDialog kind="cni" cluster={ref} current={kubeProxy} label={t('ops.action.switch')} disabled={!!busy} />
        </Action>
        <Action
          title={t('ops.row.caRotation')}
          hint={t('ops.row.caRotationHint')}
          detail={<CertificateLines cluster={cluster} />}
          reason={busyReason}
        >
          <StartOperationDialog kind="ca-rotation" cluster={ref} label={t('ops.action.rotate')} disabled={!!busy} />
        </Action>
      </Group>

      <Group title={t('ops.group.danger')} hint={t('ops.group.dangerHint')} danger>
        <Action title={t('ops.row.shutdown')} hint={t('ops.row.shutdownHint')} reason={busyReason}>
          <StartOperationDialog kind="shutdown" cluster={ref} label={t('ops.action.shutdown')} disabled={!!busy} />
        </Action>
      </Group>

      <Recent ops={ops} />
    </div>
  )
}

function readyReason(m: Machine): string {
  return m.conditions.find((c) => c.type === 'Ready')?.reason ?? ''
}

// Group is one card of related actions, each a row with its own button.
function Group({ title, hint, danger, children }: { title: string; hint: string; danger?: boolean; children: ComponentChildren }) {
  return (
    <Card className={danger ? 'ring-destructive/40' : undefined}>
      <CardHeader>
        <CardTitle className={danger ? 'text-destructive' : undefined}>{title}</CardTitle>
        <CardDescription>{hint}</CardDescription>
      </CardHeader>
      <CardContent className="divide-y px-0 [&>*]:px-(--card-spacing)">{children}</CardContent>
    </Card>
  )
}

// Action is one row: what it does, what it is like now, and its button. When
// the button is off, the reason is written under it, not left to a server refusal.
function Action({
  title,
  hint,
  detail,
  reason,
  children,
}: {
  title: string
  hint: string
  detail?: ComponentChildren
  reason?: string
  children: ComponentChildren
}) {
  return (
    <div class="flex flex-col gap-3 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-start sm:justify-between sm:gap-6">
      <div class="min-w-0 space-y-1">
        <p class="font-medium">{title}</p>
        <p class="text-muted-foreground">{hint}</p>
        {detail && <div class="text-foreground">{detail}</div>}
        {reason && (
          <p class="flex items-center gap-1.5 text-muted-foreground">
            <InfoIcon class="size-4 shrink-0" aria-hidden="true" />
            {reason}
          </p>
        )}
      </div>
      <div class="shrink-0">{children}</div>
    </div>
  )
}

// CertificateLines lists each CA with its expiry date; one that is close or
// past is marked, with words as well as colour.
function CertificateLines({ cluster }: { cluster: Cluster }) {
  const t = useT()
  if (cluster.certificates.length === 0) return <span class="text-muted-foreground">{t('ops.caNone')}</span>
  const now = Date.now() / 1000
  return (
    <ul class="space-y-0.5">
      {cluster.certificates.map((c) => {
        const at = Number(c.notAfter?.seconds ?? 0n)
        const { days, level } = caExpiry(at, now)
        const date = new Intl.DateTimeFormat('en', { dateStyle: 'medium' }).format(new Date(at * 1000))
        const text = t(level === 'expired' ? 'ops.caExpired' : level === 'soon' ? 'ops.caSoon' : 'ops.caLine', { name: c.name, date, days })
        return (
          <li key={c.name}>
            {level === 'ok' ? text : <StatusText tone={level === 'expired' ? 'bad' : 'warn'}>{text}</StatusText>}
          </li>
        )
      })}
    </ul>
  )
}

function CurrentOperation({ row, total, onChanged }: { row: Row; total: number; onChanged: () => void }) {
  const t = useT()
  const op = row.op
  const state = operationState(op.phase, op.failureReason)
  const { done, running } = progressCounts(op.machines)
  const now = op.machines.find((m) => m.phase === 'InProgress')?.name
  const kind = enumLabel(OperationKind, op.kind)
  const tracksNodes = op.kind !== OperationKind.ICHOR && op.kind !== OperationKind.CONFIG && op.kind !== OperationKind.NODE && total > 0

  return (
    <Card className="ring-sky-500/40">
      <CardHeader>
        <CardTitle>{t('ops.current')}</CardTitle>
        <CardDescription>{t('ops.currentBusy')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span class="font-medium">{kind}</span>
          <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
          {op.caRotationStage && <span class="text-muted-foreground">{op.caRotationStage}</span>}
        </div>
        <div class="space-y-1 text-muted-foreground">
          {op.machines.length > 0 && (
            <p>{tracksNodes ? t('ops.currentNodes', { done, total }) : t('ops.progress', { done, running })}</p>
          )}
          {now && <p>{t('ops.currentNode', { name: now })}</p>}
          {op.nodeMessage && <p>{op.nodeMessage}</p>}
          {op.failureReason && <p class="break-words text-destructive">{op.failureReason}</p>}
        </div>
        <div class="flex flex-wrap items-center gap-3">
          {op.ref && (
            <Link to={objectRefPath('/operations', op.ref)} class="font-medium underline-offset-4 hover:underline">
              {t('ops.currentOpen')}
            </Link>
          )}
          <OperationControls op={op} onChanged={onChanged} onDeleted={onChanged} showDelete={false} />
        </div>
      </CardContent>
    </Card>
  )
}

function Recent({ ops }: { ops: Row[] | null }) {
  const t = useT()
  if (ops === null) return null
  const rows = recentOperations(ops, 5)
  return (
    <div class="space-y-2">
      <h3 class="font-medium">{t('ops.recent')}</h3>
      {rows.length === 0 ? (
        <p class="text-muted-foreground">{t('ops.recentNone')}</p>
      ) : (
        <ul class="divide-y rounded-md border">
          {rows.map(({ op }) => {
            const state = operationState(op.phase, op.failureReason)
            return (
              <li key={op.ref?.name} class="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-3 py-2">
                <div class="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
                  <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
                  {op.ref ? (
                    <Link to={objectRefPath('/operations', op.ref)} class="font-medium hover:underline">
                      {enumLabel(OperationKind, op.kind)}
                    </Link>
                  ) : (
                    enumLabel(OperationKind, op.kind)
                  )}
                </div>
                <span class="text-muted-foreground">{formatTimestamp(op.createdAt)}</span>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
