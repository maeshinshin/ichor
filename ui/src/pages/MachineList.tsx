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
import { ErrorAlert } from '@/components/ErrorAlert'
import type { NodeDetail } from '@/gen/ichor/v1alpha1/node_pb'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { clusterClient, machineClient, nodeClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { canApprove, isStrongIdentity } from '@/lib/identity'
import { formatBytes, formatTimestamp } from '@/lib/format'
import { objectRefPath, Link } from '@/router'

type ErrorShape = { title: string; detail: string }

// Machine list, pending approvals included unconditionally
// (architecture.md, "CLI and UI"; machine.proto's own doc comment), with
// the approve/reject actions.
export function MachineList() {
  const [machines, setMachines] = useState<Machine[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [clusterNames, setClusterNames] = useState<Record<string, string>>({})

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
  if (!machines) return <p class="mt-10 text-center text-sm text-muted-foreground">Loading machines…</p>

  // approval_state is a tri-state: a rejected
  // machine is its own bucket, not "pending" - that's the whole point of
  // the state existing, so it stops reappearing as a fresh approval
  // request every time this list loads.
  const pending = machines.filter((m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED)
  const rejected = machines.filter((m) => m.approvalState === MachineApprovalState.REJECTED)
  const approved = machines.filter((m) => m.approvalState === MachineApprovalState.APPROVED)

  return (
    <div class="mt-6 space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Pending approval ({pending.length})</CardTitle>
        </CardHeader>
        <CardContent>
          {pending.length === 0 ? (
            <p class="text-sm text-muted-foreground">Nothing is waiting on a decision.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Identity</TableHead>
                  <TableHead>Cluster</TableHead>
                  <TableHead>Endpoint</TableHead>
                  <TableHead>Stage</TableHead>
                  <TableHead>Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pending.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell>{identitySummary(m)}</TableCell>
                    <TableCell>{m.cluster ? clusterNames[`${m.cluster.namespace}/${m.cluster.name}`] ?? m.cluster.name : '-'}</TableCell>
                    <TableCell>{m.endpoint || '-'}</TableCell>
                    <TableCell>{m.stage || '-'}</TableCell>
                    <TableCell>
                      <div class="flex gap-2">
                        <ApproveDialog machine={m} onApproved={replaceMachine} />
                        <RejectDialog machine={m} onRejected={reload} />
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
          <CardTitle>Rejected ({rejected.length})</CardTitle>
        </CardHeader>
        <CardContent>
          {rejected.length === 0 ? (
            <p class="text-sm text-muted-foreground">Nothing has been turned away.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Identity</TableHead>
                  <TableHead>Cluster</TableHead>
                  <TableHead>Reason</TableHead>
                  <TableHead>When</TableHead>
                  <TableHead>Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rejected.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell>{identitySummary(m)}</TableCell>
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
                        <ApproveDialog machine={m} onApproved={replaceMachine} />
                        <RejectDialog machine={m} onRejected={reload} />
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
          <CardTitle>Approved ({approved.length})</CardTitle>
        </CardHeader>
        <CardContent>
          {approved.length === 0 ? (
            <p class="text-sm text-muted-foreground">No approved machines yet.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Hostname</TableHead>
                  <TableHead>Cluster</TableHead>
                  <TableHead>Role</TableHead>
                  <TableHead>Stage</TableHead>
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
                    <TableCell>{m.stage || '-'}</TableCell>
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

function sameRef(a: Machine, b: Machine): boolean {
  return a.ref?.namespace === b.ref?.namespace && a.ref?.name === b.ref?.name
}

function refKey(m: Machine): string {
  return `${m.ref?.namespace}/${m.ref?.name}`
}

function identitySummary(m: Machine): string {
  return m.identity?.uuid || m.ref?.name || 'unknown'
}

function ApproveDialog({ machine, onApproved }: { machine: Machine; onApproved: (m: Machine) => void }) {
  const [open, setOpen] = useState(false)
  const [node, setNode] = useState<NodeDetail | null>(null)
  const [nodeError, setNodeError] = useState<string | null>(null)
  const [role, setRole] = useState<MachineRole>(MachineRole.UNSPECIFIED)
  const [hostname, setHostname] = useState(machine.hostname)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)

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
  }, [open])

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
      if (res.machine) onApproved(res.machine)
      setOpen(false)
    } catch (err) {
      setSubmitError(describeError(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm">Approve</Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Approve {machine.ref?.name}?</DialogTitle>
          <DialogDescription>
            Approving hands this machine to the controller, which starts applying configuration to it right
            away. There is no "un-approve"; changing its configuration afterwards is a separate, later
            action, not something this dialog does.
          </DialogDescription>
        </DialogHeader>

        <div class="space-y-3 text-sm">
          <section>
            <h4 class="font-medium">Identity</h4>
            <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-muted-foreground">
              <dt>UUID</dt>
              <dd class="text-foreground">{machine.identity?.uuid || 'missing'}</dd>
              <dt>Serial</dt>
              <dd class="text-foreground">{machine.identity?.serial || 'missing'}</dd>
              <dt>MAC addresses</dt>
              <dd class="text-foreground">
                {machine.identity?.macAddresses.length ? machine.identity.macAddresses.join(', ') : 'none'}
              </dd>
            </dl>
            {!strong && (
              <p class="mt-2 rounded-md border border-destructive/30 bg-destructive/10 p-2 text-destructive">
                Weak identity: a UUID, a serial number and at least one permanent MAC address are all
                required. This machine cannot be reliably told apart from another next time it's
                seen, so approval is refused.
              </p>
            )}
          </section>

          <section>
            <h4 class="font-medium">Found address</h4>
            <p class="text-muted-foreground">{machine.endpoint || 'none reported'}</p>
          </section>

          <section>
            <h4 class="font-medium">Hardware</h4>
            {nodeError && <p class="text-muted-foreground">Could not load hardware details: {nodeError}</p>}
            {!nodeError && !node && <p class="text-muted-foreground">Loading…</p>}
            {node?.hardware && (
              <p class="text-muted-foreground">
                {node.hardware.manufacturer} {node.hardware.model} - {node.hardware.cpuCores} cores,{' '}
                {formatBytes(node.hardware.memoryTotalBytes)} RAM
              </p>
            )}
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-role">Role</Label>
            <Select value={role ? String(role) : ''} onValueChange={(v) => setRole(Number(v) as MachineRole)}>
              <SelectTrigger id="approve-role" className="w-full">
                <SelectValue placeholder="Select a role" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={String(MachineRole.CONTROL_PLANE)}>Control plane</SelectItem>
                <SelectItem value={String(MachineRole.WORKER)}>Worker</SelectItem>
              </SelectContent>
            </Select>
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-hostname">Hostname</Label>
            <Input
              id="approve-hostname"
              value={hostname}
              onInput={(e) => setHostname((e.target as HTMLInputElement).value)}
            />
          </section>

          <section class="space-y-1">
            <Label htmlFor="approve-reason">Reason (optional)</Label>
            {/* Symmetric with reject's reason: approving rarely needs a justification -
                identity, hardware and role above already say what's being approved -
                but one can still be recorded, e.g. "replacing node-3, same role". */}
            <Textarea
              id="approve-reason"
              value={reason}
              onInput={(e) => setReason((e.target as HTMLTextAreaElement).value)}
              placeholder="Why, if it isn't obvious from the above"
            />
          </section>

          {submitError && <ErrorAlert {...submitError} />}
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button disabled={!canApprove(machine.identity, role !== MachineRole.UNSPECIFIED) || submitting} onClick={submit}>
            {submitting ? 'Approving…' : 'Approve'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RejectDialog({ machine, onRejected }: { machine: Machine; onRejected: () => void }) {
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<ErrorShape | null>(null)

  async function submit() {
    if (!machine.ref) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      await machineClient.rejectMachine({ machine: machine.ref, reason })
      setOpen(false)
      onRejected()
    } catch (err) {
      setSubmitError(describeError(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          Reject
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Reject {machine.ref?.name}?</DialogTitle>
          <DialogDescription>
            Rejecting records who rejected it, when, and why, and marks it rejected; it is not deleted and
            its own discovery loop running again will not bring it back to "pending". This isn't permanent:
            approving a rejected machine later simply reverses it - there's no separate "un-reject" action.
          </DialogDescription>
        </DialogHeader>

        <div class="space-y-1">
          <Label htmlFor="reject-reason">Reason</Label>
          <Textarea
            id="reject-reason"
            value={reason}
            onInput={(e) => setReason((e.target as HTMLTextAreaElement).value)}
            placeholder="Why is this machine being turned away?"
          />
        </div>

        {submitError && <ErrorAlert {...submitError} />}

        <DialogFooter>
          <Button variant="ghost" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant="destructive" disabled={submitting} onClick={submit}>
            {submitting ? 'Rejecting…' : 'Reject'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
