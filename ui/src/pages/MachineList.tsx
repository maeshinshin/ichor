import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
import { StatusBadge } from '@/components/ConditionList'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import type { NodeDetail } from '@/gen/ichor/v1alpha1/node_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient, nodeClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { offersBreakGlass } from '@/lib/breakGlass'
import { describeError } from '@/lib/errors'
import { canApprove, isStrongIdentity } from '@/lib/identity'
import { formatBytes, formatTimestamp } from '@/lib/format'
import { useT } from '@/lib/useT'
import { objectRefPath, Link } from '@/router'

type ErrorShape = { title: string; detail: string }

// Machine list, pending approvals included unconditionally
// (architecture.md, "CLI and UI"; machine.proto's own doc comment), with
// the approve/reject actions.
export function MachineList() {
  const t = useT()
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [clusterNames, setClusterNames] = useState<Record<string, string>>({})
  const [pullRequest, setPullRequest] = useState<{ url: string; run?: BreakGlassRun } | null>(null)

  async function reload() {
    try {
      const [machinesRes, clustersRes] = await Promise.all([
        machineClient.listMachines({}),
        clusterClient.listClusters({}),
      ])
      setMachines(machinesRes.machines)
      setClusterNames(
        Object.fromEntries(
          clustersRes.clusters
            .filter((c) => c.ref)
            .map((c) => [`${c.ref!.namespace}/${c.ref!.name}`, c.ref!.name]),
        ),
      )
      setError(null)
    } catch (err) {
      setError(describeError(err))
    }
  }

  useEffect(() => {
    reload()
  }, [])

  function replaceMachine(updated: Machine) {
    setMachines((prev) => prev?.map((m) => (sameRef(m, updated) ? updated : m)) ?? prev)
  }

  if (error) return <ErrorAlert {...error} />
  if (!machines) return <p class="mt-10 text-center text-sm text-muted-foreground">{t('machines.loading')}</p>

  // approval_state is a tri-state: a rejected
  // machine is its own bucket, not "pending" - that's the whole point of
  // the state existing, so it stops reappearing as a fresh approval
  // request every time this list loads.
  const pending = machines.filter((m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED)
  const rejected = machines.filter((m) => m.approvalState === MachineApprovalState.REJECTED)
  const approved = machines.filter((m) => m.approvalState === MachineApprovalState.APPROVED)

  return (
    <div class="mt-6 space-y-4">
      {pullRequest && <PullRequestNotice url={pullRequest.url} breakGlass={pullRequest.run} onBreakGlassDone={reload} />}
      <Card>
        <CardHeader>
          <CardTitle>{t('machines.pending', { n: pending.length })}</CardTitle>
        </CardHeader>
        <CardContent>
          {pending.length === 0 ? (
            <p class="text-sm text-muted-foreground">{t('machines.pendingEmpty')}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.identity')}</TableHead>
                  <TableHead>{t('col.cluster')}</TableHead>
                  <TableHead>{t('col.endpoint')}</TableHead>
                  <TableHead>{t('col.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pending.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell>{identitySummary(m, t('machines.unknown'))}</TableCell>
                    <TableCell>{m.cluster ? clusterNames[`${m.cluster.namespace}/${m.cluster.name}`] ?? m.cluster.name : '-'}</TableCell>
                    <TableCell>{m.endpoint || '-'}</TableCell>
                    <TableCell>
                      <div class="flex gap-2">
                        <ApproveDialog machine={m} onApproved={replaceMachine} onPullRequest={(url, run) => setPullRequest({ url, run })} />
                        <RejectDialog machine={m} onRejected={reload} onPullRequest={(url, run) => setPullRequest({ url, run })} />
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('machines.rejected', { n: rejected.length })}</CardTitle>
        </CardHeader>
        <CardContent>
          {rejected.length === 0 ? (
            <p class="text-sm text-muted-foreground">{t('machines.rejectedEmpty')}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.identity')}</TableHead>
                  <TableHead>{t('col.cluster')}</TableHead>
                  <TableHead>{t('machines.colReason')}</TableHead>
                  <TableHead>{t('machines.colWhen')}</TableHead>
                  <TableHead>{t('col.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rejected.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell>{identitySummary(m, t('machines.unknown'))}</TableCell>
                    <TableCell>{m.cluster ? clusterNames[`${m.cluster.namespace}/${m.cluster.name}`] ?? m.cluster.name : '-'}</TableCell>
                    {/* The decision record: recorded on the CR
                        itself, not just a one-shot RPC response, so it's still here the
                        next time anyone loads this list. */}
                    <TableCell>{m.approvalDecision?.reason || '-'}</TableCell>
                    <TableCell>{formatTimestamp(m.approvalDecision?.at)}</TableCell>
                    <TableCell>
                      <div class="flex gap-2">
                        {/* Rejection is reversible: approving a
                            rejected machine simply moves it to approved, no separate
                            "un-reject" action exists. */}
                        <ApproveDialog machine={m} onApproved={replaceMachine} onPullRequest={(url, run) => setPullRequest({ url, run })} />
                        <RejectDialog machine={m} onRejected={reload} onPullRequest={(url, run) => setPullRequest({ url, run })} />
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>{t('machines.approved', { n: approved.length })}</CardTitle>
        </CardHeader>
        <CardContent>
          {approved.length === 0 ? (
            <p class="text-sm text-muted-foreground">{t('machines.approvedEmpty')}</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('machines.colHostname')}</TableHead>
                  <TableHead>{t('col.cluster')}</TableHead>
                  <TableHead>{t('col.role')}</TableHead>
                  <TableHead>{t('col.ready')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {approved.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell>
                      {m.ref ? (
                        <Link to={objectRefPath('/nodes', m.ref)} class="font-medium hover:underline">
                          {m.hostname || m.ref.name}
                        </Link>
                      ) : (
                        m.hostname || '-'
                      )}
                    </TableCell>
                    <TableCell>{m.cluster ? clusterNames[`${m.cluster.namespace}/${m.cluster.name}`] ?? m.cluster.name : '-'}</TableCell>
                    <TableCell>{enumLabel(MachineRole, m.role)}</TableCell>
                    <TableCell>
                      <ReadyCell machine={m} />
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

// ReadyCell shows the machine's Ready condition (status and reason), which the
// controller does record; Talos's own stage is not read for the list.
function ReadyCell({ machine }: { machine: Machine }) {
  const ready = machine.conditions.find((c) => c.type === 'Ready')
  if (!ready) return <>-</>
  return (
    <span class="inline-flex items-center gap-2">
      <StatusBadge type={ready.type} status={ready.status} />
      {ready.reason && <span class="text-xs text-muted-foreground">{ready.reason}</span>}
    </span>
  )
}

function sameRef(a: Machine, b: Machine): boolean {
  return a.ref?.namespace === b.ref?.namespace && a.ref?.name === b.ref?.name
}

function refKey(m: Machine): string {
  return `${m.ref?.namespace}/${m.ref?.name}`
}

function identitySummary(m: Machine, unknown: string): string {
  return m.identity?.uuid || m.ref?.name || unknown
}

function ApproveDialog({
  machine,
  onApproved,
  onPullRequest,
}: {
  machine: Machine
  onApproved: (m: Machine) => void
  onPullRequest: (url: string, run: BreakGlassRun) => void
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [node, setNode] = useState<NodeDetail | null>(null)
  const [nodeError, setNodeError] = useState<string | null>(null)
  const [role, setRole] = useState<MachineRole>(MachineRole.UNSPECIFIED)
  const [hostname, setHostname] = useState(machine.hostname)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)
  const [refused, setRefused] = useState(false)

  const strong = isStrongIdentity(machine.identity)

  useEffect(() => {
    if (!open || !machine.ref) return
    setNode(null)
    setNodeError(null)
    nodeClient.getNode({ machine: machine.ref }).then(
      (res) => setNode(res.node ?? null),
      (err) => setNodeError(describeError(err).detail),
    )
    // Reset the form each time the dialog is reopened.
    setRole(MachineRole.UNSPECIFIED)
    setHostname(machine.hostname)
    setReason('')
    setSubmitError(null)
    setRefused(false)
  }, [open])

  // The same approval again with break_glass set.
  const breakGlassApprove: BreakGlassRun = async (bg) => {
    const res = await machineClient.approveMachine({
      machine: machine.ref,
      role,
      hostname,
      nodeLabels: {},
      reason,
      breakGlass: bg,
    })
    if (res.machine) onApproved(res.machine)
    return { pullRequestUrl: res.pullRequestUrl, notice: res.notice }
  }

  async function submit() {
    if (!machine.ref || !canApprove(machine.identity, role !== MachineRole.UNSPECIFIED)) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      const res = await machineClient.approveMachine({
        machine: machine.ref,
        role,
        hostname,
        nodeLabels: {},
        reason,
      })
      if (res.pullRequestUrl) onPullRequest(res.pullRequestUrl, breakGlassApprove)
      else if (res.machine) onApproved(res.machine)
      setOpen(false)
    } catch (err) {
      setSubmitError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">{t('approve.button')}</Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('approve.title', { name: machine.ref?.name ?? '' })}</DialogTitle>
          <DialogDescription>{t('approve.description')}</DialogDescription>
        </DialogHeader>

        <div class="space-y-3 text-sm">
          <section>
            <h4 class="font-medium">{t('approve.identity')}</h4>
            <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-muted-foreground">
              <dt>{t('approve.uuid')}</dt>
              <dd class="text-foreground">{machine.identity?.uuid || t('approve.missing')}</dd>
              <dt>{t('approve.serial')}</dt>
              <dd class="text-foreground">{machine.identity?.serial || t('approve.missing')}</dd>
              <dt>{t('approve.mac')}</dt>
              <dd class="text-foreground">
                {machine.identity?.macAddresses.length ? machine.identity.macAddresses.join(', ') : t('common.none')}
              </dd>
            </dl>
            {!strong && (
              <p class="mt-2 rounded-md border border-destructive/30 bg-destructive/10 p-2 text-destructive">
                {t('approve.weakIdentity')}
              </p>
            )}
          </section>

          <section>
            <h4 class="font-medium">{t('approve.foundAddress')}</h4>
            <p class="text-muted-foreground">{machine.endpoint || t('approve.noAddress')}</p>
          </section>

          <section>
            <h4 class="font-medium">{t('approve.hardware')}</h4>
            {nodeError && <p class="text-muted-foreground">{t('approve.hardwareFailed', { detail: nodeError })}</p>}
            {!nodeError && !node && <p class="text-muted-foreground">{t('common.loading')}</p>}
            {node?.hardware && (
              <p class="text-muted-foreground">
                {t('approve.hardwareSummary', {
                  manufacturer: node.hardware.manufacturer,
                  model: node.hardware.model,
                  cores: node.hardware.cpuCores,
                  ram: formatBytes(node.hardware.memoryTotalBytes),
                })}
              </p>
            )}
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-role">{t('approve.role')}</Label>
            <Select value={role ? String(role) : ''} onValueChange={(v) => setRole(Number(v) as MachineRole)}>
              <SelectTrigger id="approve-role" className="w-full">
                <SelectValue placeholder={t('approve.selectRole')} />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={String(MachineRole.CONTROL_PLANE)}>{t('role.controlPlane')}</SelectItem>
                <SelectItem value={String(MachineRole.WORKER)}>{t('role.worker')}</SelectItem>
              </SelectContent>
            </Select>
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-hostname">{t('approve.hostname')}</Label>
            <Input
              id="approve-hostname"
              value={hostname}
              onInput={(e) => setHostname((e.target as HTMLInputElement).value)}
            />
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-reason">{t('approve.reason')}</Label>
            {/* Symmetric with reject's reason: approving rarely needs a justification -
                identity, hardware and role above already say what's being approved -
                but one can still be recorded, e.g. "replacing node-3, same role". */}
            <Textarea
              id="approve-reason"
              value={reason}
              onInput={(e) => setReason((e.target as HTMLTextAreaElement).value)}
              placeholder={t('approve.reasonPlaceholder')}
            />
          </section>

          {submitError && <ErrorAlert {...submitError} />}
          {refused && <BreakGlassDialog run={breakGlassApprove} onDone={() => setOpen(false)} />}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button disabled={!canApprove(machine.identity, role !== MachineRole.UNSPECIFIED) || submitting} onClick={submit}>
            {submitting ? t('approve.submitting') : t('approve.button')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RejectDialog({
  machine,
  onRejected,
  onPullRequest,
}: {
  machine: Machine
  onRejected: () => void
  onPullRequest: (url: string, run: BreakGlassRun) => void
}) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)
  const [refused, setRefused] = useState(false)

  // The same rejection again with break_glass set.
  const breakGlassReject: BreakGlassRun = async (bg) => {
    const res = await machineClient.rejectMachine({ machine: machine.ref, reason, breakGlass: bg })
    return { pullRequestUrl: res.pullRequestUrl, notice: res.notice }
  }

  async function submit() {
    if (!machine.ref) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      const res = await machineClient.rejectMachine({ machine: machine.ref, reason })
      if (res.pullRequestUrl) onPullRequest(res.pullRequestUrl, breakGlassReject)
      setOpen(false)
      onRejected()
    } catch (err) {
      setSubmitError(describeError(err))
      setRefused(offersBreakGlass({ error: err }))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          {t('reject.button')}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('reject.title', { name: machine.ref?.name ?? '' })}</DialogTitle>
          <DialogDescription>{t('reject.description')}</DialogDescription>
        </DialogHeader>

        <div class="space-y-1">
          <Label htmlFor="reject-reason">{t('reject.reason')}</Label>
          <Textarea
            id="reject-reason"
            value={reason}
            onInput={(e) => setReason((e.target as HTMLTextAreaElement).value)}
            placeholder={t('reject.reasonPlaceholder')}
          />
        </div>

        {submitError && <ErrorAlert {...submitError} />}
        {refused && <BreakGlassDialog run={breakGlassReject} onDone={() => { setOpen(false); onRejected() }} />}

        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant="destructive" disabled={submitting} onClick={submit}>
            {submitting ? t('reject.submitting') : t('reject.button')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
