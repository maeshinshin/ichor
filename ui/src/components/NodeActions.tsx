import { useEffect, useRef, useState } from 'preact/hooks'
import { ChevronDownIcon } from 'lucide-preact'
import { toast } from '@/lib/shims/sonner'
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
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { NodeAction, OperationKind, type PlanOperationResponse } from '@/gen/ichor/v1alpha1/operation_pb'
import { machineClient, nodeClient, operationClient } from '@/lib/connect'
import { notifyDone } from '@/lib/notify'
import {
  availableActions as availableNodeActions,
  canStart,
  restartableServices,
  type NodeActionDef,
  type NodeActionId,
} from '@/lib/nodeActions'
import {
  availableActions,
  captureInterfaces,
  checkCapture,
  defaultCaptureSeconds,
  type DownloadKind,
  type Permissions,
} from '@/lib/diagnostics'
import { download, type DiagnosticsDownloadRequest } from '@/lib/download'
import { describeError, type ErrorShape } from '@/lib/errors'
import { t } from '@/lib/i18n'

type Ref = { namespace: string; name: string }
const wireAction: Record<NodeActionId, NodeAction> = {
  reboot: NodeAction.REBOOT,
  shutdown: NodeAction.SHUTDOWN,
  'restart-service': NodeAction.RESTART_SERVICE,
  'etcd-defrag': NodeAction.ETCD_DEFRAG,
  'etcd-forfeit-leadership': NodeAction.ETCD_FORFEIT_LEADERSHIP,
  reset: NodeAction.RESET,
  remove: NodeAction.REMOVE,
}

const downloadLabel = { 'support-bundle': 'actions.supportBundle', 'etcd-snapshot': 'actions.etcdSnapshot', 'packet-capture': 'actions.capture' } as const

// The node page's one Actions menu, in three groups. Downloads (docs/adr/0084): three downloads, each
// shown only when Kubernetes RBAC lets this user start it, and each behind a warning that the file may
// contain secrets. Node and Danger zone (docs/adr/0085): the node actions, each opening a dialog with the
// server's plan; choosing an item writes nothing, only the dialog's own button does. The UI only writes
// custom resources (an operation, or the deletion of the machine); the controller works on the node.
// Actions that cannot apply to the node are not offered, and the whole menu is absent when none is.
export function NodeActions({
  machine,
  displayName,
  perms,
  controlPlane,
  approved,
  deleting,
  onStarted,
}: {
  machine: Ref
  // The hostname the page shows for the machine; the machine's own name (a UUID) is what a typed confirmation needs.
  displayName?: string
  // What the caller may download, fetched once by the page; undefined offers no download.
  perms: Permissions | undefined
  controlPlane: boolean
  approved: boolean
  deleting: boolean
  onStarted: () => void
}) {
  // The download waiting for the user's confirmation, with its request when it has parameters.
  const [pending, setPending] = useState<DiagnosticsDownloadRequest | null>(null)
  const [captureOpen, setCaptureOpen] = useState(false)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => () => abort.current?.abort(), [])

  const [active, setActive] = useState<NodeActionDef | null>(null)
  const actions = availableActions(perms, controlPlane)
  const nodeDefs = availableNodeActions({ approved, deleting, role: controlPlane ? 'controlplane' : 'worker', perms })
  const safeDefs = nodeDefs.filter((d) => !d.destructive)
  const dangerDefs = nodeDefs.filter((d) => d.destructive)
  if (actions.length === 0 && nodeDefs.length === 0) return null

  async function run(req: DiagnosticsDownloadRequest) {
    setPending(null)
    const controller = new AbortController()
    abort.current = controller
    const id = toast.loading(t('actions.working', { what: t(`actions.what.${req.kind}`) }), { duration: Infinity })
    try {
      const file = await download(req, controller.signal)
      toast.success(t('actions.done', { file }), { id, duration: 8000 })
    } catch (err) {
      if (controller.signal.aborted) return toast.dismiss(id)
      const e = describeError(err)
      toast.error(t('actions.failed'), { id, description: e.detail || e.title, duration: 15000 })
    }
  }

  const start = (kind: DownloadKind) => {
    if (kind === 'packet-capture') return setCaptureOpen(true)
    setPending({ kind, machine })
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="sm" aria-label={t('actions.menuLabel')}>
            {t('actions.menu')}
            <ChevronDownIcon className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-auto min-w-56">
          {actions.length > 0 && (
            <>
              <DropdownMenuLabel>{t('actions.groupDownloads')}</DropdownMenuLabel>
              {actions.map((k) => (
                <DropdownMenuItem key={k} onSelect={() => start(k)}>
                  {t(downloadLabel[k])}
                </DropdownMenuItem>
              ))}
            </>
          )}
          {safeDefs.length > 0 && (
            <>
              {actions.length > 0 && <DropdownMenuSeparator />}
              <DropdownMenuLabel>{t('actions.groupNode')}</DropdownMenuLabel>
              {safeDefs.map((d) => (
                <DropdownMenuItem key={d.id} onSelect={() => setActive(d)}>
                  {t(`node.action.${d.id}`)}
                </DropdownMenuItem>
              ))}
            </>
          )}
          {dangerDefs.length > 0 && (
            <>
              {(actions.length > 0 || safeDefs.length > 0) && <DropdownMenuSeparator />}
              <DropdownMenuLabel>{t('actions.groupDanger')}</DropdownMenuLabel>
              {dangerDefs.map((d) => (
                <DropdownMenuItem key={d.id} variant="destructive" onSelect={() => setActive(d)}>
                  {t(`node.action.${d.id}`)}
                </DropdownMenuItem>
              ))}
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      {/* An action that stops being offered (the permissions changed) takes its open dialog with it. */}
      {active && nodeDefs.some((d) => d.id === active.id) && (
        <NodeActionDialog
          key={active.id}
          def={active}
          machine={machine}
          displayName={displayName}
          onClose={() => setActive(null)}
          onStarted={() => {
            setActive(null)
            onStarted()
          }}
        />
      )}

      <CaptureDialog
        machine={machine}
        open={captureOpen}
        onOpenChange={setCaptureOpen}
        onSubmit={(capture) => {
          setCaptureOpen(false)
          setPending({ kind: 'packet-capture', capture: { ...capture, machine } })
        }}
      />

      <AlertDialog open={pending !== null} onOpenChange={(o) => !o && setPending(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('actions.warnTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {pending && <span class="block">{pending.kind === 'support-bundle' ? t('actions.warnSupportBundle', { name: machine.name }) : t(pending.kind === 'etcd-snapshot' ? 'actions.warnEtcd' : 'actions.warnCapture')}</span>}
              <span class="mt-2 block">{t('actions.warnAudit')}</span>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={() => pending && run(pending)}>{t('actions.download')}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

type CaptureParams = { interface: string; durationSeconds: number; bpfFilter: string }

// CaptureDialog is the packet capture form: the interface (from the node's own links), the time and
// an optional filter. It validates before anything is sent; the server checks again.
function CaptureDialog({
  machine,
  open,
  onOpenChange,
  onSubmit,
}: {
  machine: Ref
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (c: CaptureParams) => void
}) {
  const [names, setNames] = useState<string[]>([])
  const [iface, setIface] = useState('')
  const [duration, setDuration] = useState(`${defaultCaptureSeconds}s`)
  const [filter, setFilter] = useState('')
  const [tried, setTried] = useState(false)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setTried(false)
    nodeClient.getNodeTalosDetails({ machine }).then(
      (res) => {
        if (cancelled) return
        const found = captureInterfaces(res.network?.links ?? [])
        setNames(found.names)
        setIface((cur) => (found.names.includes(cur) ? cur : found.initial))
      },
      () => {},
    )
    return () => {
      cancelled = true
    }
  }, [open, machine.namespace, machine.name])

  const check = checkCapture({ iface, duration, filter })
  const errors = !check.ok ? check.errors : {}

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('capture.title', { name: machine.name })}</DialogTitle>
          <DialogDescription>{t('capture.description')}</DialogDescription>
        </DialogHeader>
        <div class="space-y-4 text-sm">
          <div class="space-y-1">
            <Label htmlFor="capture-interface">{t('capture.interface')}</Label>
            {names.length > 0 ? (
              <Select value={iface} onValueChange={setIface}>
                <SelectTrigger id="capture-interface" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {names.map((n) => (
                    <SelectItem key={n} value={n}>
                      {n}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <>
                <Input id="capture-interface" value={iface} onInput={(e) => setIface(e.currentTarget.value)} placeholder="eth0" />
                <p class="text-xs text-muted-foreground">{t('capture.interfaceNone')}</p>
              </>
            )}
            {tried && errors.iface && <p class="text-xs text-destructive">{errors.iface}</p>}
          </div>
          <div class="space-y-1">
            <Label htmlFor="capture-duration">{t('capture.duration')}</Label>
            <Input id="capture-duration" value={duration} onInput={(e) => setDuration(e.currentTarget.value)} aria-invalid={tried && !!errors.duration} />
            <p class="text-xs text-muted-foreground">{t('capture.durationHint')}</p>
            {tried && errors.duration && <p class="text-xs text-destructive">{errors.duration}</p>}
          </div>
          <div class="space-y-1">
            <Label htmlFor="capture-filter">{t('capture.filter')}</Label>
            <Textarea
              id="capture-filter"
              className="font-mono text-xs"
              rows={4}
              value={filter}
              onInput={(e) => setFilter(e.currentTarget.value)}
              aria-invalid={tried && !!errors.filter}
            />
            <p class="text-xs text-muted-foreground">{t('capture.filterHint')}</p>
            {tried && errors.filter && <p class="text-xs text-destructive">{errors.filter}</p>}
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button
            onClick={() => {
              setTried(true)
              if (check.ok) onSubmit({ interface: check.interface, durationSeconds: check.durationSeconds, bpfFilter: check.bpfFilter })
            }}
          >
            {t('capture.start')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function NodeActionDialog({
  def,
  machine,
  displayName,
  onClose,
  onStarted,
}: {
  def: NodeActionDef
  machine: { namespace: string; name: string }
  displayName?: string
  onClose: () => void
  onStarted: () => void
}) {
  const shown = displayName || machine.name
  const [service, setService] = useState(def.id === 'restart-service' ? restartableServices[0] : '')
  const [allowLeader, setAllowLeader] = useState(false)
  const [typed, setTyped] = useState('')
  const [overridden, setOverridden] = useState(false)
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
    override: overridden ? {} : undefined,
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
    overridden,
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
        notifyDone(t('node.action.removed', { node: shown }))
        onStarted()
        return
      }
      await operationClient.createOperation({ kind: OperationKind.NODE, node: request(typed.trim()) })
      notifyDone(t('node.action.started', { action: label, node: shown }))
      onStarted()
    } catch (err) {
      setError(describeError(err))
      setBusy(false)
    }
  }

  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && !busy && onClose()}
      title={t('node.action.title', { action: t(`node.action.${def.id}`), node: shown })}
      description={plan === null && !error ? t('node.action.checking') : undefined}
      confirm={{
        label: busy ? t('node.action.starting') : t(`node.action.go.${def.id}`),
        onClick: () => void start(),
        disabled: !startable,
        destructive: def.destructive,
      }}
    >

        {def.id === 'restart-service' && (
          <div class="space-y-2">
            <Label htmlFor="node-action-service">{t('node.action.service')}</Label>
            <Select value={service} onValueChange={setService}>
              <SelectTrigger id="node-action-service" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {restartableServices.map((s) => (
                  <SelectItem key={s} value={s}>
                    {s}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}

        {def.id === 'etcd-defrag' && (
          <div class="flex items-center gap-2 text-sm">
            <Checkbox id="node-action-leader" checked={allowLeader} onCheckedChange={(on: boolean | 'indeterminate') => setAllowLeader(on === true)} />
            <Label htmlFor="node-action-leader">{t('node.action.allowLeader')}</Label>
          </div>
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
                <Label htmlFor="node-action-name">{shown === machine.name ? t('node.action.typeName', { name: machine.name }) : t('node.action.typeNameHost', { name: machine.name, host: shown })}</Label>
                <Input
                  id="node-action-name"
                  autoComplete="off"
                  value={typed}
                  onInput={(e) => setTyped((e.currentTarget as HTMLInputElement).value)}
                />
              </div>
            )}
            {plan.overrideRequired && (
              <div class="flex items-center gap-2 text-sm">
                <Checkbox id="node-action-override" checked={overridden} onCheckedChange={(on: boolean | 'indeterminate') => setOverridden(on === true)} />
                <Label htmlFor="node-action-override">{t('node.action.override')}</Label>
              </div>
            )}
          </div>
        )}

        {error && <ErrorAlert {...error} />}

    </ConfirmDialog>
  )
}

