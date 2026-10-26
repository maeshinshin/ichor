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
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { diagnosticsClient, nodeClient } from '@/lib/connect'
import {
  availableActions,
  captureInterfaces,
  checkCapture,
  defaultCaptureSeconds,
  type DownloadKind,
  type Permissions,
} from '@/lib/diagnostics'
import { download, type DownloadRequest } from '@/lib/download'
import { describeError } from '@/lib/errors'
import { useT } from '@/lib/useT'

type Ref = { namespace: string; name: string }

// The node page's Actions menu (docs/adr/0084): three downloads, each shown only when Kubernetes RBAC
// lets this user start it, and each behind a warning that the file may contain secrets. It renders
// nothing for a user who may do none of them, or when the permission check fails.
export function NodeActions({ machine, controlPlane }: { machine: Ref; controlPlane: boolean }) {
  const t = useT()
  const [perms, setPerms] = useState<Permissions | undefined>()
  // The download waiting for the user's confirmation, with its request when it has parameters.
  const [pending, setPending] = useState<DownloadRequest | null>(null)
  const [captureOpen, setCaptureOpen] = useState(false)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => {
    let cancelled = false
    setPerms(undefined)
    diagnosticsClient.getDiagnosticsPermissions({ machine }).then(
      (res) => {
        if (!cancelled) setPerms({ supportBundle: res.supportBundle, etcdSnapshot: res.etcdSnapshot, packetCapture: res.packetCapture })
      },
      () => {},
    )
    return () => {
      cancelled = true
      abort.current?.abort()
    }
  }, [machine.namespace, machine.name])

  const actions = availableActions(perms, controlPlane)
  if (actions.length === 0) return null

  async function run(req: DownloadRequest) {
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
            <ChevronDownIcon class="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          {actions.includes('support-bundle') && (
            <DropdownMenuItem onSelect={() => start('support-bundle')}>{t('actions.supportBundle')}</DropdownMenuItem>
          )}
          {actions.includes('etcd-snapshot') && (
            <DropdownMenuItem onSelect={() => start('etcd-snapshot')}>{t('actions.etcdSnapshot')}</DropdownMenuItem>
          )}
          {actions.includes('packet-capture') && (
            <DropdownMenuItem onSelect={() => start('packet-capture')}>{t('actions.capture')}</DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

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
              {pending && <span class="block">{warning(t, pending.kind, machine.name)}</span>}
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

function warning(t: ReturnType<typeof useT>, kind: DownloadKind, name: string): string {
  switch (kind) {
    case 'support-bundle':
      return t('actions.warnSupportBundle', { name })
    case 'etcd-snapshot':
      return t('actions.warnEtcd')
    case 'packet-capture':
      return t('actions.warnCapture')
  }
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
  const t = useT()
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
  const show = tried

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
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
            {show && errors.iface && <p class="text-xs text-destructive">{errors.iface}</p>}
          </div>
          <div class="space-y-1">
            <Label htmlFor="capture-duration">{t('capture.duration')}</Label>
            <Input id="capture-duration" value={duration} onInput={(e) => setDuration(e.currentTarget.value)} aria-invalid={show && !!errors.duration} />
            <p class="text-xs text-muted-foreground">{t('capture.durationHint')}</p>
            {show && errors.duration && <p class="text-xs text-destructive">{errors.duration}</p>}
          </div>
          <div class="space-y-1">
            <Label htmlFor="capture-filter">{t('capture.filter')}</Label>
            <Textarea
              id="capture-filter"
              class="font-mono text-xs"
              rows={4}
              value={filter}
              onInput={(e) => setFilter(e.currentTarget.value)}
              aria-invalid={show && !!errors.filter}
            />
            <p class="text-xs text-muted-foreground">{t('capture.filterHint')}</p>
            {show && errors.filter && <p class="text-xs text-destructive">{errors.filter}</p>}
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
