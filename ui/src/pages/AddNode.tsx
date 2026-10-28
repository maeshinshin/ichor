import { useEffect, useState } from 'preact/hooks'
import { ArrowLeftIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ApproveDialog } from '@/components/ApproveDialog'
import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import { Section } from '@/components/Section'
import { DataTable } from '@/components/ListToolbar'
import type { Cluster } from '@/gen/ichor/v1alpha1/cluster_pb'
import { MachineApprovalState, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { offersBreakGlass } from '@/lib/breakGlass'
import { clusterClient, machineClient } from '@/lib/connect'
import { checkDiscoveryAddress, discoveryOn } from '@/lib/discovery'
import { describeError } from '@/lib/errors'
import { notifyWrite } from '@/lib/notify'
import { useCluster } from '@/lib/useCluster'
import { useT } from '@/lib/useT'
import { Link } from '@/router'

type ErrorShape = { title: string; detail: string }

const POLL_MS = 5000

// AddNode is where a person brings a new node into a cluster. Ichor never scans
// a network: it only looks at the addresses the cluster lists. So the
// page shows those, lets the person add one, and lists the nodes that were found
// and now wait for a decision. Approving uses the same dialog as the node list.
export function AddNode() {
  const t = useT()
  const { cluster: current, error: clusterError, reload: reloadCurrent } = useCluster()
  const [cluster, setCluster] = useState<Cluster | null>(null)
  const namespace = current?.ref?.namespace
  const name = current?.ref?.name
  const [machines, setMachines] = useState<Machine[]>([])
  const [error, setError] = useState<ErrorShape | null>(null)
  const [pullRequest, setPullRequest] = useState<{ url: string; run?: BreakGlassRun } | null>(null)

  async function reloadMachines() {
    if (!namespace || !name) return
    try {
      setMachines((await machineClient.listMachines({ cluster: { namespace, name } })).machines)
    } catch {
      // The next poll tries again; the cluster itself loaded or the page shows its error.
    }
  }

  function reloadCluster() {
    reloadCurrent()
  }

  useEffect(() => {
    setCluster(current)
    setError(null)
    reloadMachines()
    const id = setInterval(reloadMachines, POLL_MS)
    return () => clearInterval(id)
  }, [current])

  if (clusterError) return <ErrorAlert {...clusterError} />
  if (error) return <ErrorAlert {...error} />
  if (!cluster) return <PageSkeleton label={t('cluster.loading')} />

  const pending = machines.filter(
    (m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED,
  )
  const onPullRequest = (url: string, run: BreakGlassRun) => setPullRequest({ url, run })

  return (
    <>
      <PageHeader
        title={t('addNode.pageTitle')}
        kind={t('kind.cluster')}
        actions={
          <Button asChild size="sm" variant="outline">
            <Link to="/nodes">
              <ArrowLeftIcon />
              {t('addNode.backToNodes')}
            </Link>
          </Button>
        }
      />
      <div class="max-w-4xl space-y-8">
        <Section id="steps" title={t('addNode.stepsTitle')}>
          <ol class="list-inside list-decimal space-y-2">
            <li>{t('addNode.step1')}</li>
            <li>{t('addNode.step2')}</li>
            <li>{t('addNode.step3')}</li>
          </ol>
        </Section>

        <Section id="discovery" title={t('addNode.discoveryTitle')}>
          <Discovery
            cluster={cluster}
            onAdded={(c) => (c ? setCluster(c) : reloadCluster())}
            onPullRequest={(url, run) => setPullRequest({ url, run })}
          />
          {pullRequest && <PullRequestNotice url={pullRequest.url} breakGlass={pullRequest.run} onBreakGlassDone={reloadCluster} />}
        </Section>

        <Section id="pending" title={t('addNode.pendingTitle', { n: pending.length })}>
          {pending.length === 0 ? (
            <EmptyNote>{t('addNode.pendingEmpty')}</EmptyNote>
          ) : (
            <DataTable>
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
                      <TableCell className="font-mono text-xs">{m.identity?.uuid || m.ref?.name}</TableCell>
                      <TableCell>{m.endpoint || '-'}</TableCell>
                      <TableCell>
                        <ApproveDialog
                          machine={m}
                          onApproved={() => reloadMachines()}
                          onPullRequest={onPullRequest}
                        />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </DataTable>
          )}
        </Section>
      </div>
    </>
  )
}

function Discovery({
  cluster,
  onAdded,
  onPullRequest,
}: {
  cluster: Cluster
  onAdded: (c: Cluster | undefined) => void
  onPullRequest: (url: string, run: BreakGlassRun) => void
}) {
  const t = useT()
  const [address, setAddress] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [refused, setRefused] = useState(false)

  const check = checkDiscoveryAddress(address, cluster.discoveryAddresses)
  const on = discoveryOn(cluster.discoveryAddresses, cluster.discoveryCidrs)

  // The same write again with break_glass set.
  const breakGlassAdd: BreakGlassRun = async (bg) => {
    const res = await clusterClient.addDiscoveryAddress({ cluster: cluster.ref, address: address.trim(), breakGlass: bg })
    if (res.cluster) onAdded(res.cluster)
    return { pullRequestUrl: res.pullRequestUrl, notice: res.notice }
  }

  async function submit() {
    if (check !== 'ok') return
    setSubmitting(true)
    setError(null)
    setRefused(false)
    try {
      const res = await clusterClient.addDiscoveryAddress({ cluster: cluster.ref, address: address.trim() })
      if (res.pullRequestUrl) onPullRequest(res.pullRequestUrl, breakGlassAdd)
      else onAdded(res.cluster)
      notifyWrite(res, 'toast.discoveryAdded', { address: address.trim() })
      setAddress('')
    } catch (err) {
      setError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div class="space-y-4">
      <p class="text-muted-foreground">{on ? t('addNode.discoveryOn') : t('addNode.discoveryOff')}</p>
      <dl class="grid gap-4 sm:grid-cols-2">
        <div>
          <dt class="mb-1 text-xs text-muted-foreground">{t('addNode.addresses')}</dt>
          <dd class="flex flex-wrap gap-1">
            {cluster.discoveryAddresses.length === 0 ? (
              <span class="text-muted-foreground">-</span>
            ) : (
              cluster.discoveryAddresses.map((a) => (
                <Badge key={a} variant="outline" className="font-mono">
                  {a}
                </Badge>
              ))
            )}
          </dd>
        </div>
        <div>
          <dt class="mb-1 text-xs text-muted-foreground">{t('addNode.cidrs')}</dt>
          <dd class="flex flex-wrap gap-1">
            {cluster.discoveryCidrs.length === 0 ? (
              <span class="text-muted-foreground">-</span>
            ) : (
              cluster.discoveryCidrs.map((c) => (
                <Badge key={c} variant="outline" className="font-mono">
                  {c}
                </Badge>
              ))
            )}
          </dd>
        </div>
      </dl>
      <form
        class="space-y-1"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Label htmlFor="discovery-address">{t('addNode.addLabel')}</Label>
        <div class="flex gap-2">
          <Input
            id="discovery-address"
            className="max-w-xs font-mono"
            placeholder="192.168.0.50"
            value={address}
            onInput={(e) => setAddress((e.target as HTMLInputElement).value)}
            aria-invalid={check === 'invalid' || check === 'duplicate'}
          />
          <Button type="submit" disabled={check !== 'ok' || submitting}>
            {submitting ? t('addNode.adding') : t('addNode.add')}
          </Button>
        </div>
        {check === 'invalid' && <p class="text-xs text-destructive">{t('addNode.invalid')}</p>}
        {check === 'duplicate' && <p class="text-xs text-destructive">{t('addNode.duplicate')}</p>}
        <p class="text-xs text-muted-foreground">{t('addNode.cidrHint')}</p>
      </form>
      {error && <ErrorAlert {...error} />}
      {refused && <BreakGlassDialog run={breakGlassAdd} onDone={() => onAdded(undefined)} />}
    </div>
  )
}
