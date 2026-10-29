import { useEffect, useState } from 'preact/hooks'
import { ArrowLeftIcon, CircleCheckIcon, CircleXIcon, InboxIcon, PowerIcon, RadarIcon, TriangleAlertIcon } from 'lucide-preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CardDescription } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Item, ItemContent, ItemDescription, ItemGroup, ItemMedia, ItemSeparator } from '@/components/ui/item'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ApproveDialog } from '@/components/ApproveDialog'
import { ReclaimDialog } from '@/components/ReclaimDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Field, Fields } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { SectionCard } from '@/components/SectionCard'
import { PageTitle } from '@/components/PageTitle'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient } from '@/lib/connect'
import { checkDiscoveryAddress, discoveryOn } from '@/lib/discovery'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyWrite } from '@/lib/notify'
import { useCluster } from '@/lib/useCluster'
import { useCanReclaim } from '@/lib/useCanReclaim'
import { useT } from '@/lib/useT'
import { Link } from '@/router'

const POLL_MS = 5000

// AddNode is where a person brings a new node into a cluster. Ichor never scans
// a network (ADR 0011): it only looks at the addresses the cluster lists. So the
// page shows those, lets the person add one, and lists the nodes that were found
// and now wait for a decision. Approving uses the same dialog as the node list.
export function AddNode() {
  const t = useT()
  const { cluster: current, error: clusterError, reload: reloadCurrent } = useCluster()
  const [cluster, setCluster] = useState<Cluster | null>(null)
  const namespace = current?.ref?.namespace
  const name = current?.ref?.name
  const [machines, setMachines] = useState<Machine[]>([])
  // Addresses discovery reached that already run a Talos configuration (ADR 0103).
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
                    <TableHead>{t('col.endpoint')}</TableHead>
                    <TableHead>{t('col.actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pending.map((m) => (
                    <TableRow key={m.ref?.name}>
                      <TableCell className="font-mono">{m.identity?.uuid || m.ref?.name}</TableCell>
                      <TableCell>{m.endpoint || '-'}</TableCell>
                      <TableCell>
                        <ApproveDialog
                          machine={m}
                          onApproved={() => reloadMachines()}
                        />
                      </TableCell>
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

function Discovery({
  cluster,
  configured,
  onAdded,
}: {
  cluster: Cluster
  configured: readonly string[]
  onAdded: (c: Cluster | undefined) => void
}) {
  const t = useT()
  const [address, setAddress] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const canReclaim = useCanReclaim()

  const check = checkDiscoveryAddress(address, cluster.discoveryAddresses)
  const on = discoveryOn(cluster.discoveryAddresses, cluster.discoveryCidrs)


  async function submit() {
    if (check !== 'ok') return
    setSubmitting(true)
    setError(null)
    try {
      const res = await clusterClient.addDiscoveryAddress({ cluster: cluster.ref, address: address.trim() })
      onAdded(res.cluster)
      notifyWrite('toast.discoveryAdded', { address: address.trim() })
      setAddress('')
    } catch (err) {
      setError(describeError(err))
    } finally {
      setSubmitting(false)
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
                <Badge key={a} variant="outline">
                  {a}
                </Badge>
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
                <Badge key={c} variant="outline">
                  {c}
                </Badge>
              ))}
            </div>
          )}
        </Field>
      </Fields>
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
            placeholder="192.168.0.50"
            value={address}
            onInput={(e) => setAddress((e.target as HTMLInputElement).value)}
            aria-invalid={check === 'invalid' || check === 'duplicate'}
          />
          <Button type="submit" disabled={check !== 'ok' || submitting}>
            {submitting ? t('addNode.adding') : t('addNode.add')}
          </Button>
        </div>
        {(check === 'invalid' || check === 'duplicate') && (
          <Alert variant="destructive">
            <CircleXIcon />
            <AlertDescription>{check === 'invalid' ? t('addNode.invalid') : t('addNode.duplicate')}</AlertDescription>
          </Alert>
        )}
        <CardDescription>{t('addNode.cidrHint')}</CardDescription>
      </form>
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
