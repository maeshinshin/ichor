import { useEffect, useState } from 'preact/hooks'
import { TriangleAlertIcon } from 'lucide-preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import type { Reclaim } from '@/gen/ichor/v1alpha1/reclaim_pb'
import { useAction } from '@/hooks/use-action'
import { reclaimClient } from '@/lib/connect'
import { reclaimSettled, usesClusterCredentials, uuidMatches } from '@/lib/reclaim'
import { useLoad } from '@/lib/useLoad'
import { t } from '@/lib/i18n'

const POLL_MS = 2000

// "Reset and add": reset a machine that runs another cluster's Talos configuration, then add it here (ADR 0103, 2.2; ADR 0107). The
// caller only renders it when useCanReclaim is true. The typed UUID box starts empty and is never filled
// in for the person.
export function ReclaimDialog({
  address: initialAddress = '',
  cluster,
  label,
  ownCredentials = false,
}: {
  address?: string
  cluster?: { namespace: string; name: string }
  label?: string
  // ownCredentials presets "It was removed from this cluster" (an orphan of the cluster, ADR 0136).
  ownCredentials?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [address, setAddress] = useState(initialAddress)
  const [talosconfig, setTalosconfig] = useState('')
  const [context, setContext] = useState('')
  const [wantOwn, setOwn] = useState(ownCredentials)
  const [reclaim, setReclaim] = useState<Reclaim | null>(null)
  const [typed, setTyped] = useState('')
  const { busy, error, setError, run } = useAction()

  // The cluster is offered only when the caller may update it; otherwise reclaim goes ahead without registering.
  const clusterAllowed = useLoad(cluster ? () => reclaimClient.canReclaim({ cluster }) : undefined, [cluster?.namespace, cluster?.name]).data?.clusterAllowed ?? false

  const own = usesClusterCredentials(wantOwn, clusterAllowed)
  const ref = reclaim?.ref
  const phase = reclaim?.phase ?? ''
  const uuid = reclaim?.inspection?.uuid ?? ''

  useEffect(() => {
    if (!ref || reclaimSettled(phase)) return
    const id = setInterval(async () => {
      try {
        const res = await reclaimClient.getReclaim({ reclaim: ref })
        if (res.reclaim) setReclaim(res.reclaim)
      } catch {
        // The next poll tries again.
      }
    }, POLL_MS)
    return () => clearInterval(id)
  }, [ref?.name, phase])

  const create = () =>
    run(async () => {
      const res = await reclaimClient.createReclaim({
        address: address.trim(),
        talosconfig: own ? new Uint8Array() : new TextEncoder().encode(talosconfig),
        context: own ? '' : context.trim(),
        cluster: clusterAllowed ? cluster : undefined,
        useClusterCredentials: own,
      })
      setTalosconfig('')
      setReclaim(res.reclaim ?? null)
    })

  function confirm() {
    if (!ref || !uuidMatches(typed, uuid)) return
    return run(async () => {
      const res = await reclaimClient.confirmReclaim({ reclaim: ref, uuid: typed.trim() })
      if (res.reclaim) setReclaim(res.reclaim)
    })
  }

  async function readFile(e: Event) {
    const file = (e.target as HTMLInputElement).files?.[0]
    if (file) setTalosconfig(await file.text())
  }

  function onOpenChange(next: boolean) {
    setOpen(next)
    if (!next) {
      setTalosconfig('')
      setOwn(ownCredentials)
      setTyped('')
      setReclaim(null)
      setError(null)
    }
  }

  const insp = reclaim?.inspection
  const canCreate = address.trim() !== '' && (own || talosconfig.trim() !== '') && !busy

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          {label ?? t('reclaim.button')}
        </Button>
      </DialogTrigger>
      <DialogContent className="grid-cols-1 sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('reclaim.title')}</DialogTitle>
          <DialogDescription>{t('reclaim.description')}</DialogDescription>
        </DialogHeader>
        <Alert variant="destructive">
          <TriangleAlertIcon />
          <AlertDescription>{t('reclaim.banner')}</AlertDescription>
        </Alert>

        {!reclaim && (
          <form
            class="grid gap-4 text-sm"
            onSubmit={(e) => {
              e.preventDefault()
              if (canCreate) create()
            }}
          >
            <div className="-mx-4 max-h-[50dvh] space-y-3 overflow-y-auto px-4">
            <div class="space-y-1">
              <Label htmlFor="reclaim-address">{t('reclaim.address')}</Label>
              <Input id="reclaim-address" value={address} onInput={(e) => setAddress((e.target as HTMLInputElement).value)} />
            </div>
            {clusterAllowed && (
              <div class="flex items-center gap-2">
                <Checkbox id="reclaim-own" checked={own} onCheckedChange={(on: boolean | 'indeterminate') => setOwn(on === true)} />
                <Label htmlFor="reclaim-own">{t('reclaim.ownCredentials')}</Label>
              </div>
            )}
            {own ? (
              <Alert>
                <TriangleAlertIcon />
                <AlertDescription>{t('reclaim.ownWarning')}</AlertDescription>
              </Alert>
            ) : (
              <>
            <div class="space-y-1">
              <Label htmlFor="reclaim-talosconfig">{t('reclaim.talosconfig')}</Label>
              <Textarea
                id="reclaim-talosconfig"
                className="font-mono"
                value={talosconfig}
                onInput={(e) => setTalosconfig((e.target as HTMLTextAreaElement).value)}
              />
            </div>
            <div class="space-y-1">
              <Label htmlFor="reclaim-file">{t('reclaim.talosconfigFile')}</Label>
              <Input id="reclaim-file" type="file" onChange={readFile} />
            </div>
            <div class="space-y-1">
              <Label htmlFor="reclaim-context">{t('reclaim.context')}</Label>
              <Input id="reclaim-context" value={context} onInput={(e) => setContext((e.target as HTMLInputElement).value)} />
            </div>
            <Alert>
              <TriangleAlertIcon />
              <AlertDescription>{t('reclaim.storedWarning', { ttl: '15 minutes' })}</AlertDescription>
            </Alert>
              </>
            )}
            {error && <ErrorAlert {...error} />}
            </div>
            <DialogFooter>
              <Button type="submit" disabled={!canCreate}>
                {busy ? t('reclaim.creating') : t('reclaim.create')}
              </Button>
            </DialogFooter>
          </form>
        )}

        {reclaim && (
          <form
            class="grid gap-4 text-sm"
            onSubmit={(e) => {
              e.preventDefault()
              confirm()
            }}
          >
            <div className="-mx-4 max-h-[50dvh] space-y-3 overflow-y-auto px-4">
            <Fields>
              <Field label={t('reclaim.address')}>{reclaim.address}</Field>
              <Field label={t('reclaim.phase')}>
                <Badge variant="outline">{phase || '-'}</Badge>
              </Field>
              {insp && (
                <>
                  <Field label={t('reclaim.foreignCluster')}>{insp.clusterName || '-'}</Field>
                  <Field label={t('reclaim.uuid')}>
                    <span class="font-mono break-all">{insp.uuid}</span>
                  </Field>
                  <Field label={t('reclaim.hostname')}>{insp.hostname || '-'}</Field>
                  <Field label={t('reclaim.role')}>{insp.role || '-'}</Field>
                  <Field label={t('reclaim.etcd')}>{insp.etcdMembers.length ? insp.etcdMembers.join(', ') : '-'}</Field>
                </>
              )}
            </Fields>
            {reclaim.message && <p>{reclaim.message}</p>}
            {insp && insp.volumes.length > 0 && (
              <div class="space-y-1">
                <p>{t('reclaim.volumes')}</p>
                {insp.volumes.map((v) => (
                  <div key={v.name} class="flex flex-wrap items-center gap-2">
                    <span class="font-mono">{v.name}</span>
                    {v.encrypted && <Badge variant="destructive">{t('reclaim.encrypted')}</Badge>}
                  </div>
                ))}
              </div>
            )}
            {insp && insp.warnings.length > 0 && (
              <Alert>
                <TriangleAlertIcon />
                <AlertDescription>
                  {t('reclaim.warnings')}: {insp.warnings.join('; ')}
                </AlertDescription>
              </Alert>
            )}
            {insp && insp.refusals.length > 0 && (
              <Alert variant="destructive">
                <TriangleAlertIcon />
                <AlertDescription>
                  {t('reclaim.refusals')}: {insp.refusals.join('; ')}
                </AlertDescription>
              </Alert>
            )}
            {phase === 'AwaitingConfirmation' && !reclaim.confirmed && (
              <div class="space-y-1">
                <Label htmlFor="reclaim-uuid">{t('reclaim.typeUuid')}</Label>
                <Input
                  id="reclaim-uuid"
                  className="font-mono"
                  autoComplete="off"
                  value={typed}
                  onInput={(e) => setTyped((e.target as HTMLInputElement).value)}
                />
              </div>
            )}
            {phase === 'WaitingForMaintenance' && <p>{t('reclaim.waiting')}</p>}
            {reclaim.result && (
              <Field label={t('reclaim.result')}>
                <span class="font-mono">{reclaim.result}</span>
              </Field>
            )}
            {error && <ErrorAlert {...error} />}
            </div>
            <DialogFooter>
              {phase === 'AwaitingConfirmation' && !reclaim.confirmed && (
                <Button type="submit" variant="destructive" disabled={busy || !uuidMatches(typed, uuid)}>
                  {busy ? t('reclaim.confirming') : t('reclaim.confirm')}
                </Button>
              )}
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
