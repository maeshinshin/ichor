import { useEffect, useState } from 'preact/hooks'
import { ArrowLeftIcon, CircleCheckIcon, CircleXIcon, InboxIcon, PowerIcon, RadarIcon, TriangleAlertIcon, XIcon } from 'lucide-preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CardDescription } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Item, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemSeparator } from '@/components/ui/item'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ApproveDialog } from '@/components/ApproveDialog'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ReclaimDialog } from '@/components/ReclaimDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useAction } from '@/hooks/use-action'
import { Field, Fields } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { SectionCard } from '@/components/SectionCard'
import { PageTitle } from '@/components/PageTitle'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient } from '@/lib/connect'
import { checkDiscoveryTarget, discoveryOn, maskCidr } from '@/lib/discovery'
import { describeError } from '@/lib/errors'
import { notifyWrite } from '@/lib/notify'
import { mayApprove } from '@/lib/actions'
import { useCluster } from '@/lib/useCluster'
import { useMyPermissions } from '@/lib/useMyPermissions'
import { useCanReclaim } from '@/lib/useCanReclaim'
import { t } from '@/lib/i18n'
import { Link } from '@/router'

const POLL_MS = 5000

// AddNode is where a person brings a new node into a cluster. Ichor never scans
// a network: it only looks at the addresses the cluster lists. So the
// page shows those, lets the person add one, and lists the nodes that were found
// and now wait for a decision. Approving uses the same dialog as the node list.
export function AddNode() {
  const { cluster: current, error: clusterError, reload: reloadCurrent } = useCluster()
  const [cluster, setCluster] = useState<Cluster | null>(null)
  const namespace = current?.ref?.namespace
  const name = current?.ref?.name
  const perms = useMyPermissions(undefined, namespace && name ? { namespace, name } : undefined)
  const [machines, setMachines] = useState<Machine[]>([])
  // Addresses discovery reached that already run a Talos configuration.
  const [configured, setConfigured] = useState<string[]>([])

  async function reloadMachines() {
    if (!namespace || !name) return
    try {
      setMachines((await machineClient.listMachines({ cluster: { namespace, name } })).machines)
      const res = await clusterClient.getCluster({ cluster: { namespace, name } })
      setConfigured(res.cluster?.alreadyConfiguredAddresses ?? [])
    } catch {
      // The next poll tries again; the cluster itself loaded or the page shows its error.
    }
  }

  useEffect(() => {
    setCluster(current)
    reloadMachines()
    const id = setInterval(reloadMachines, POLL_MS)
    return () => clearInterval(id)
  }, [current])

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (!cluster) return <PageSkeleton label={t('cluster.loading')} />

  const pending = machines.filter(
    (m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED,
  )

  return (
    <>
      <PageTitle
        title={t('addNode.pageTitle')}
        description={t('kind.cluster')}
        actions={
          <Button asChild size="sm" variant="outline">
            <Link to="/nodes">
              <ArrowLeftIcon data-icon="inline-start" aria-hidden="true" />
              {t('addNode.backToNodes')}
            </Link>
          </Button>
        }
      />
      <div class="max-w-4xl space-y-4">
        <SectionCard id="steps" title={t('addNode.stepsTitle')}>
          <ItemGroup>
            {([PowerIcon, RadarIcon, CircleCheckIcon] as const).map((Icon, i) => (
              <div key={i}>
                {i > 0 && <ItemSeparator />}
                <Item>
                  <ItemMedia variant="icon">
                    <Icon aria-hidden="true" />
                  </ItemMedia>
                  <ItemContent>
                    <ItemDescription className="line-clamp-none">{t(`addNode.step${i + 1}` as 'addNode.step1')}</ItemDescription>
                  </ItemContent>
                </Item>
              </div>
            ))}
          </ItemGroup>
        </SectionCard>

        <SectionCard id="discovery" title={t('addNode.discoveryTitle')}>
          <Discovery
            cluster={cluster}
            configured={configured}
            canEdit={perms?.clusterUpdate === true}
            onAdded={(c) => (c ? setCluster(c) : reloadCurrent())}
          />
        </SectionCard>

        <SectionCard id="pending" title={t('addNode.pendingTitle', { n: pending.length })}>
          {pending.length === 0 ? (
            <EmptyNote icon={InboxIcon}>{t('addNode.pendingEmpty')}</EmptyNote>
          ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('col.identity')}</TableHead>
                    <TableHead className="hidden sm:table-cell">{t('col.endpoint')}</TableHead>
                    {mayApprove(perms) && <TableHead>{t('col.actions')}</TableHead>}
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pending.map((m) => (
                    <TableRow key={m.ref?.name}>
                      <TableCell className="font-mono max-sm:whitespace-normal max-sm:wrap-anywhere">
                        {m.identity?.uuid || m.ref?.name}
                        <div class="font-sans whitespace-nowrap text-muted-foreground sm:hidden">{m.endpoint || '-'}</div>
                      </TableCell>
                      <TableCell className="hidden sm:table-cell">{m.endpoint || '-'}</TableCell>
                      {mayApprove(perms) && (
                        <TableCell>
                          <ApproveDialog machine={m} onApproved={reloadMachines} />
                        </TableCell>
                      )}
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
          )}
        </SectionCard>
      </div>
    </>
  )
}

// RemoveCidr confirms first: a range can stand for 256 machines. onRemove throws
// when the server refuses; the page shows the error, the dialog just stays open.
function RemoveCidr({ cidr, onRemove }: { cidr: string; onRemove: () => Promise<void> }) {
  const [open, setOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  async function submit() {
    setSubmitting(true)
    try {
      await onRemove()
      setOpen(false)
    } catch {
      // The page shows the error.
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      trigger={
        <Button type="button" variant="ghost" size="icon-xs" aria-label={t('addNode.remove', { target: cidr })}>
          <XIcon aria-hidden="true" />
        </Button>
      }
      title={t('addNode.removeCidrTitle', { cidr })}
      description={t('addNode.removeCidrDescription')}
      confirm={{
        label: submitting ? t('addNode.removing') : t('addNode.removeConfirm'),
        disabled: submitting,
        onClick: submit,
      }}
    />
  )
}

function Discovery({
  cluster,
  configured,
  canEdit,
  onAdded,
}: {
  cluster: Cluster
  configured: readonly string[]
  canEdit: boolean
  onAdded: (c: Cluster | undefined) => void
}) {
  const [address, setAddress] = useState('')
  const { busy: submitting, error, setError, run } = useAction()
  const canReclaim = useCanReclaim()

  // One input takes an address or a range; a slash means a range.
  const { kind, check } = checkDiscoveryTarget(address, cluster.discoveryAddresses, cluster.discoveryCidrs)
  const on = discoveryOn(cluster.discoveryAddresses, cluster.discoveryCidrs)
  const problem = check === 'ok' || check === 'empty' ? null : check

  async function submit() {
    if (check !== 'ok') return
    const value = address.trim()
    await run(async () => {
      if (kind === 'cidr') {
        const res = await clusterClient.addDiscoveryCidr({ cluster: cluster.ref, cidr: value })
        onAdded(res.cluster)
        notifyWrite('toast.discoveryCidrAdded', { cidr: maskCidr(value) ?? value })
      } else {
        const res = await clusterClient.addDiscoveryAddress({ cluster: cluster.ref, address: value })
        onAdded(res.cluster)
        notifyWrite('toast.discoveryAdded', { address: value })
      }
      setAddress('')
    })
  }

  async function remove(target: string, isCidr: boolean) {
    setError(null)
    try {
      const res = isCidr
        ? await clusterClient.removeDiscoveryCidr({ cluster: cluster.ref, cidr: target })
        : await clusterClient.removeDiscoveryAddress({ cluster: cluster.ref, address: target })
      onAdded(res.cluster)
      notifyWrite('toast.discoveryRemoved', { target })
    } catch (err) {
      setError(describeError(err))
      throw err
    }
  }

  return (
    <>
      <CardDescription>{on ? t('addNode.discoveryOn') : t('addNode.discoveryOff')}</CardDescription>
      <Fields>
        <Field label={t('addNode.addresses')}>
          {cluster.discoveryAddresses.length === 0 ? (
            '-'
          ) : (
            <div class="flex flex-wrap gap-1">
              {cluster.discoveryAddresses.map((a) => (
                <span key={a} class="flex items-center gap-1">
                  <Badge variant="outline">{a}</Badge>
                  {canEdit && (
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      aria-label={t('addNode.remove', { target: a })}
                      onClick={() => remove(a, false).catch(() => {})}
                    >
                      <XIcon aria-hidden="true" />
                    </Button>
                  )}
                </span>
              ))}
            </div>
          )}
        </Field>
        <Field label={t('addNode.cidrs')}>
          {cluster.discoveryCidrs.length === 0 ? (
            '-'
          ) : (
            <div class="flex flex-wrap gap-1">
              {cluster.discoveryCidrs.map((c) => (
                <span key={c} class="flex items-center gap-1">
                  <Badge variant="outline">{c}</Badge>
                  {canEdit && <RemoveCidr cidr={c} onRemove={() => remove(c, true)} />}
                </span>
              ))}
            </div>
          )}
        </Field>
      </Fields>
      {canEdit && (
      <form
        class="space-y-2"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Label htmlFor="discovery-address">{t('addNode.addLabel')}</Label>
        <div class="flex gap-2">
          <Input
            id="discovery-address"
            className="max-w-xs"
            placeholder={t('addNode.placeholder')}
            value={address}
            onInput={(e) => setAddress((e.target as HTMLInputElement).value)}
            aria-invalid={problem !== null}
          />
          <Button type="submit" disabled={check !== 'ok' || submitting}>
            {submitting ? t('addNode.adding') : t('addNode.add')}
          </Button>
        </div>
        {problem && (
          <Alert variant="destructive">
            <CircleXIcon />
            <AlertDescription>
              {problem === 'duplicate' ? t(kind === 'cidr' ? 'addNode.duplicateCidr' : 'addNode.duplicate') : t(`addNode.${problem}`)}
            </AlertDescription>
          </Alert>
        )}
        <CardDescription>{t('addNode.cidrHint')}</CardDescription>
      </form>
      )}
      {error && <ErrorAlert {...error} />}
      {configured.map((a) => (
        <Alert key={a}>
          <TriangleAlertIcon />
          <AlertDescription>
            {t(canReclaim ? 'addNode.alreadyConfiguredCan' : 'addNode.alreadyConfigured', { address: a })}
            {canReclaim && (
              <div class="mt-2">
                <ReclaimDialog address={a} cluster={cluster.ref} label={t('reclaim.link')} />
              </div>
            )}
          </AlertDescription>
        </Alert>
      ))}
    </>
  )
}
