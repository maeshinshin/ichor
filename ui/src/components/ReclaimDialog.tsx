import { useEffect, useState } from 'preact/hooks'
import { TriangleAlertIcon } from 'lucide-preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field as InfoField } from '@/components/PageHeader'
import { Field, FieldContent, FieldDescription, FieldLabel } from '@/components/ui/field'
import { CopyableName } from '@/components/CopyableName'
import type { Reclaim } from '@/gen/ichor/v1alpha1/reclaim_pb'
import { useAction } from '@/hooks/use-action'
import { reclaimClient } from '@/lib/connect'
import { reclaimPhaseLabel, reclaimSettled, usesClusterCredentials, uuidMatches } from '@/lib/reclaim'
import { useLoad } from '@/lib/useLoad'
import { t } from '@/lib/i18n'

const POLL_MS = 2000

// "Reset and add": reset a machine that runs another cluster's Talos configuration, then add it here. The
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
  // ownCredentials presets "It was removed from this cluster" (an orphan of the cluster).
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
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] grid-cols-1 flex-col sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(own ? 'reclaim.ownTitle' : 'reclaim.title')}</DialogTitle>
          <DialogDescription>{t(own ? 'reclaim.ownDescription' : 'reclaim.description')}</DialogDescription>
        </DialogHeader>
        <Alert variant="destructive">
          <TriangleAlertIcon />
          <AlertDescription>{t(own ? 'reclaim.ownBanner' : 'reclaim.banner')}</AlertDescription>
        </Alert>

        {!reclaim && (
          <form
            class="flex min-h-0 flex-1 flex-col gap-4 text-sm"
            onSubmit={(e) => {
              e.preventDefault()
              if (canCreate) create()
            }}
          >
            <div className="-mx-4 min-h-0 flex-1 space-y-3 overflow-y-auto px-4">
            <div class="space-y-1">
              <Label htmlFor="reclaim-address">{t('reclaim.address')}</Label>
              <Input id="reclaim-address" value={address} onInput={(e) => setAddress((e.target as HTMLInputElement).value)} />
            </div>
            {clusterAllowed && (
              <Field orientation="horizontal">
                <Checkbox id="reclaim-own" checked={own} onCheckedChange={(on: boolean | 'indeterminate') => setOwn(on === true)} />
                <FieldContent>
                  <FieldLabel htmlFor="reclaim-own">{t('reclaim.ownCredentials')}</FieldLabel>
                  <FieldDescription>{t('reclaim.ownCredentialsHint')}</FieldDescription>
                </FieldContent>
              </Field>
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
            class="flex min-h-0 flex-1 flex-col gap-4 text-sm"
            onSubmit={(e) => {
              e.preventDefault()
              confirm()
            }}
          >
            <div className="-mx-4 min-h-0 flex-1 space-y-3 overflow-y-auto px-4">
            <dl class="grid grid-cols-2 gap-x-6 gap-y-4">
              <InfoField label={t('reclaim.address')}>{reclaim.address}</InfoField>
              <InfoField label={t('reclaim.phase')}>
                <Badge variant="outline" className="h-auto max-w-full whitespace-normal">
                  {phase ? reclaimPhaseLabel(phase) : '-'}
                </Badge>
              </InfoField>
              {insp && (
                <>
                  <InfoField label={t('reclaim.foreignCluster')}>{insp.clusterName || '-'}</InfoField>
                  <InfoField label={t('reclaim.hostname')}>{insp.hostname || '-'}</InfoField>
                  <InfoField label={t('reclaim.role')}>{insp.role || '-'}</InfoField>
                  <InfoField label={t('reclaim.etcd')}>{insp.etcdMembers.length ? insp.etcdMembers.join(', ') : '-'}</InfoField>
                  <div class="col-span-2">
                    <InfoField label={t('reclaim.uuid')}>
                      <CopyableName value={insp.uuid} ariaLabel={t('reclaim.copyUuid')} />
                    </InfoField>
                  </div>
                </>
              )}
            </dl>
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
              <InfoField label={t('reclaim.result')}>
                <span class="font-mono">{reclaim.result}</span>
              </InfoField>
            )}
            {error && <ErrorAlert {...error} />}
            </div>
            <DialogFooter>
              {!(phase === 'AwaitingConfirmation' && !reclaim.confirmed) && (
                <DialogClose asChild>
                  <Button type="button" variant="outline">
                    {t('common.close')}
                  </Button>
                </DialogClose>
              )}
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
