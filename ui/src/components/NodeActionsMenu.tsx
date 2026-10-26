import { useEffect, useRef, useState } from 'preact/hooks'
import { ChevronDown } from 'lucide-preact'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { NodeAction, OperationKind, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient, machineClient } from '@/lib/connect'
import { describeError } from '@/lib/errors'
import {
  availableActions,
  canStart,
  restartableServices,
  type NodeActionDef,
  type NodeActionId,
} from '@/lib/nodeActions'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

const wireAction: Record<NodeActionId, NodeAction> = {
  reboot: NodeAction.REBOOT,
  shutdown: NodeAction.SHUTDOWN,
  'restart-service': NodeAction.RESTART_SERVICE,
  'etcd-defrag': NodeAction.ETCD_DEFRAG,
  'etcd-forfeit-leadership': NodeAction.ETCD_FORFEIT_LEADERSHIP,
  reset: NodeAction.RESET,
  remove: NodeAction.REMOVE,
}

// What the page shows after an action started, until it is dismissed.
export type NodeActionStarted = { message: string; operation?: { namespace: string; name: string } }

// NodeActionsMenu is the node page's one actions menu: every node action, each opening an alert
// dialog with the server's plan (docs/adr/0083). Choosing an item writes nothing; only the dialog's
// own button does, after the plan was shown. The UI only writes custom resources (an operation,
// or the deletion of the machine); the controller does the work on the node. extra is where other
// entries of the same menu (downloads) are added.
export function NodeActionsMenu({
  machine,
  onStarted,
  extra,
}: {
  machine: { namespace: string; name: string; approved: boolean; deleting: boolean; role: string }
  onStarted: (s: NodeActionStarted) => void
  extra?: preact.ComponentChildren
}) {
  const t = useT()
  const [active, setActive] = useState<NodeActionDef | null>(null)
  const defs = availableActions(machine)

  if (defs.length === 0 && !extra) return null

  const safe = defs.filter((d) => !d.destructive)
  const destructive = defs.filter((d) => d.destructive)

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" aria-label={t('node.actions.menuLabel')}>
            {t('node.actions.menu')}
            <ChevronDown class="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-auto min-w-56">
          {safe.map((d) => (
            <DropdownMenuItem key={d.id} onSelect={() => setActive(d)}>
              {t(`node.action.${d.id}`)}
            </DropdownMenuItem>
          ))}
          {extra}
          {destructive.length > 0 && (safe.length > 0 || extra) && <DropdownMenuSeparator />}
          {destructive.map((d) => (
            <DropdownMenuItem key={d.id} variant="destructive" onSelect={() => setActive(d)}>
              {t(`node.action.${d.id}`)}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      {active && (
        <NodeActionDialog
          key={active.id}
          def={active}
          machine={machine}
          onClose={() => setActive(null)}
          onStarted={(s) => {
            setActive(null)
            onStarted(s)
          }}
        />
      )}
    </>
  )
}

function NodeActionDialog({
  def,
  machine,
  onClose,
  onStarted,
}: {
  def: NodeActionDef
  machine: { namespace: string; name: string }
  onClose: () => void
  onStarted: (s: NodeActionStarted) => void
}) {
  const t = useT()
  const [service, setService] = useState(def.id === 'restart-service' ? restartableServices[0] : '')
  const [allowLeader, setAllowLeader] = useState(false)
  const [typed, setTyped] = useState('')
  const [reason, setReason] = useState('')
  const [plan, setPlan] = useState<PlanOperationResponse | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const latest = useRef(0)

  const request = (confirm: string) => ({
    machine: { namespace: machine.namespace, name: machine.name },
    action: wireAction[def.id],
    service: def.id === 'restart-service' ? service : '',
    allowLeader: def.id === 'etcd-defrag' && allowLeader,
    confirm,
    breakGlass: def.breakGlass && reason.trim() !== '' ? { reason: reason.trim() } : undefined,
  })

  // The plan is asked for as soon as the dialog opens, and again when an option that changes it
  // does. Only the answer to the latest question is shown.
  useEffect(() => {
    const mine = ++latest.current
    setPlan(null)
    setError(null)
    operationClient.planOperation({ kind: OperationKind.NODE, node: request('') }).then(
      (res) => {
        if (latest.current === mine) setPlan(res)
      },
      (err) => {
        if (latest.current === mine) setError(describeError(err))
      },
    )
  }, [service, allowLeader])

  const confirmName = plan?.confirmName ?? ''
  const startable = canStart({
    def,
    blocked: plan?.blocked ?? '',
    planned: plan !== null,
    confirmName,
    typed,
    nodeName: machine.name,
    reason,
    service,
    busy,
  })

  async function start() {
    setBusy(true)
    setError(null)
    try {
      const label = t(`node.action.go.${def.id}`)
      if (def.id === 'remove') {
        await machineClient.removeMachine({ machine: { namespace: machine.namespace, name: machine.name }, confirm: typed.trim() })
        onStarted({ message: t('node.action.removed', { node: machine.name }) })
        return
      }
      const res = await operationClient.createOperation({ kind: OperationKind.NODE, node: request(typed.trim()) })
      onStarted({
        message: t('node.action.started', { action: label, node: machine.name }),
        operation: res.operation?.ref ? { namespace: res.operation.ref.namespace, name: res.operation.ref.name } : undefined,
      })
    } catch (err) {
      setError(describeError(err))
      setBusy(false)
    }
  }

  return (
    <AlertDialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <AlertDialogContent className="max-h-[90vh] overflow-y-auto data-[size=default]:max-w-lg data-[size=default]:sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>{t('node.action.title', { action: t(`node.action.${def.id}`).replace('…', ''), node: machine.name })}</AlertDialogTitle>
          <AlertDialogDescription>{plan === null && !error ? t('node.action.checking') : ''}</AlertDialogDescription>
        </AlertDialogHeader>

        {def.id === 'restart-service' && (
          <div class="space-y-2">
            <Label htmlFor="node-action-service">{t('node.action.service')}</Label>
            <select
              id="node-action-service"
              class="h-9 w-full rounded-md border bg-background px-2 text-sm"
              value={service}
              onChange={(e) => setService((e.currentTarget as HTMLSelectElement).value)}
            >
              {restartableServices.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
        )}

        {def.id === 'etcd-defrag' && (
          <label class="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              class="mt-1"
              checked={allowLeader}
              onChange={(e) => setAllowLeader((e.currentTarget as HTMLInputElement).checked)}
            />
            <span>{t('node.action.allowLeader')}</span>
          </label>
        )}

        {plan && plan.blocked !== '' && (
          <Alert variant="destructive">
            <AlertTitle>{t('node.action.cannot')}</AlertTitle>
            <AlertDescription>{plan.blocked}</AlertDescription>
          </Alert>
        )}

        {plan && plan.blocked === '' && (
          <div class="space-y-3 text-sm">
            <div>
              <p class="mb-1 font-medium">{t('node.action.stepsTitle')}</p>
              <ol class="list-inside list-decimal space-y-0.5">
                {plan.steps.map((s) => (
                  <li key={s}>{s}</li>
                ))}
              </ol>
            </div>
            {plan.kubernetesApiUnavailable && (
              <Alert variant="destructive">
                <AlertTitle>{t('node.action.apiDown')}</AlertTitle>
              </Alert>
            )}
            {plan.warning !== '' && (
              <Alert variant={def.destructive ? 'destructive' : 'default'}>
                <AlertTitle>{t('node.action.warning')}</AlertTitle>
                <AlertDescription>{plan.warning}</AlertDescription>
              </Alert>
            )}
            {confirmName !== '' && (
              <div class="space-y-2">
                <Label htmlFor="node-action-name">{t('node.action.typeName', { name: machine.name })}</Label>
                <Input
                  id="node-action-name"
                  autoComplete="off"
                  value={typed}
                  onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)}
                />
              </div>
            )}
            {plan.breakGlassRequired && (
              <div class="space-y-2">
                <Label htmlFor="node-action-reason">{t('node.action.reason')}</Label>
                <Textarea id="node-action-reason" value={reason} onInput={(e) => setReason((e.currentTarget as HTMLTextAreaElement).value)} />
                <p class="text-xs text-muted-foreground">{t('node.action.reasonHint')}</p>
              </div>
            )}
          </div>
        )}

        {error && <ErrorAlert {...error} />}

        <AlertDialogFooter>
          <AlertDialogCancel disabled={busy}>{t('common.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            variant={def.destructive ? 'destructive' : 'default'}
            disabled={!startable}
            onClick={(e: Event) => {
              // The dialog stays open while the request runs, and on an error.
              e.preventDefault()
              void start()
            }}
          >
            {busy ? t('node.action.starting') : t(`node.action.go.${def.id}`)}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// NodeActionStartedNotice is the confirmation shown on the page after an action started.
export function NodeActionStartedNotice({ started, onDismiss }: { started: NodeActionStarted; onDismiss: () => void }) {
  const t = useT()
  return (
    <Alert className="mb-4" role="status">
      <AlertTitle>{started.message}</AlertTitle>
      <AlertDescription>
        <div class="flex flex-wrap items-center gap-3">
          {started.operation && (
            <Link to={objectRefPath('/operations', started.operation)} class="underline">
              {t('node.action.viewOperation')}
            </Link>
          )}
          <Button size="sm" variant="ghost" onClick={onDismiss}>
            {t('node.action.dismiss')}
          </Button>
        </div>
      </AlertDescription>
    </Alert>
  )
}
