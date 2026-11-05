import { useState } from 'preact/hooks'
import { InboxIcon, PlusIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { ApproveDialog } from '@/components/ApproveDialog'
import { ReclaimDialog } from '@/components/ReclaimDialog'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { LinkRow, ListToolbar } from '@/components/ListToolbar'
import { PageTitle } from '@/components/PageTitle'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ReadyCell } from '@/components/ReadyCell'
import { ErrorAlert } from '@/components/ErrorAlert'
import { MachineApprovalState, MachineRole, type Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { machineClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { machineDisplayName } from '@/lib/machineName'
import { notifyWrite } from '@/lib/notify'
import { matchesQuery } from '@/lib/filter'
import { formatTimestamp } from '@/lib/format'
import { useCanReclaim } from '@/lib/useCanReclaim'
import { describeError, type ErrorShape } from '@/lib/errors'
import { useList } from '@/lib/useList'
import { t } from '@/lib/i18n'
import { Link } from '@/router'

// Machine list, pending approvals included unconditionally
// (architecture.md, "CLI and UI"; machine.proto's own doc comment), with
// the approve/reject actions.
export function MachineList() {
  const { items: machines, setItems: setMachines, error, reload } = useList(async () => (await machineClient.listMachines({})).machines)
  const [query, setQuery] = useState('')
  const canReclaim = useCanReclaim()

  function replaceMachine(updated: Machine) {
    setMachines((prev) => prev?.map((m) => (sameRef(m, updated) ? updated : m)) ?? prev)
  }

  if (error) return <ErrorAlert {...error} />
  if (!machines) return <PageSkeleton label={t('machines.loading')} />

  // approval_state is a tri-state: a rejected
  // machine is its own bucket, not "pending" - that's the whole point of
  // the state existing, so it stops reappearing as a fresh approval
  // request every time this list loads.
  const match = (m: Machine) =>
    matchesQuery(query, m.hostname, m.nodeName, m.ref?.name, m.identity?.uuid, m.endpoint, enumLabel(MachineRole, m.role))
  const shown = machines.filter(match)
  const pending = shown.filter((m) => m.approvalState !== MachineApprovalState.APPROVED && m.approvalState !== MachineApprovalState.REJECTED)
  const rejected = shown.filter((m) => m.approvalState === MachineApprovalState.REJECTED)
  const approved = shown.filter((m) => m.approvalState === MachineApprovalState.APPROVED)

  return (
    <div class="space-y-6">
      <PageTitle
        title={t('nodes.title')}
        actions={
          <div class="flex gap-2">
            {canReclaim && <ReclaimDialog />}
            <Button asChild size="sm">
              <Link to="/nodes/add">
                <PlusIcon data-icon="inline-start" aria-hidden="true" />
                {t('nodes.add')}
              </Link>
            </Button>
          </div>
        }
      />
      <ListToolbar query={query} onQuery={setQuery} shown={shown.length} total={machines.length} onRefresh={reload} />
      {pending.length > 0 && (
<Card>
<CardHeader><CardTitle>{t('machines.pending', { n: pending.length })}</CardTitle></CardHeader>
<CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.identity')}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t('col.endpoint')}</TableHead>
                  <TableHead>{t('col.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {pending.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell className="max-sm:whitespace-normal max-sm:wrap-anywhere">
                      {identitySummary(m, t('machines.unknown'))}
                      <div class="whitespace-nowrap text-muted-foreground sm:hidden">{m.endpoint || '-'}</div>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{m.endpoint || '-'}</TableCell>
                    <TableCell>
                      <div class="flex gap-2 max-sm:flex-wrap">
                        <ApproveDialog machine={m} onApproved={replaceMachine} />
                        <RejectDialog machine={m} onRejected={reload} />
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
        </CardContent></Card>
)}

      {rejected.length > 0 && (
<Card>
<CardHeader><CardTitle>{t('machines.rejected', { n: rejected.length })}</CardTitle></CardHeader>
<CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('col.identity')}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t('machines.colReason')}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t('machines.colWhen')}</TableHead>
                  <TableHead>{t('col.actions')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rejected.map((m) => (
                  <TableRow key={refKey(m)}>
                    <TableCell className="max-sm:whitespace-normal max-sm:wrap-anywhere">
                      {identitySummary(m, t('machines.unknown'))}
                      <div class="text-muted-foreground sm:hidden">{m.approvalDecision?.reason || '-'}</div>
                    </TableCell>
                    {/* The decision record: recorded on the CR
                        itself, not just a one-shot RPC response, so it's still here the
                        next time anyone loads this list. */}
                    <TableCell className="hidden sm:table-cell">{m.approvalDecision?.reason || '-'}</TableCell>
                    <TableCell className="hidden sm:table-cell">{formatTimestamp(m.approvalDecision?.at)}</TableCell>
                    <TableCell>
                      <div class="flex gap-2 max-sm:flex-wrap">
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
        </CardContent></Card>
)}

      <Card>
<CardHeader><CardTitle>{t('machines.approved', { n: approved.length })}</CardTitle></CardHeader>
<CardContent>
          {approved.length === 0 ? (
            <EmptyNote icon={InboxIcon}>{t('machines.approvedEmpty')}</EmptyNote>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('machines.colHostname')}</TableHead>
                  <TableHead className="hidden sm:table-cell">{t('col.role')}</TableHead>
                  <TableHead>{t('col.ready')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {approved.map((m) => (
                  <LinkRow key={refKey(m)} to={m.ref ? `/nodes/${m.ref.name}` : '/nodes'}>
                    <TableCell className="max-sm:whitespace-normal max-sm:wrap-anywhere">
                      {m.ref ? (
                        <Link to={`/nodes/${m.ref.name}`} className="font-medium hover:underline">
                          {machineDisplayName(m)}
                        </Link>
                      ) : (
                        m.hostname || '-'
                      )}
                      <div class="text-muted-foreground sm:hidden">{enumLabel(MachineRole, m.role)}</div>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">{enumLabel(MachineRole, m.role)}</TableCell>
                    <TableCell>
                      <ReadyCell machine={m} />
                    </TableCell>
                  </LinkRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent></Card>
    </div>
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

function RejectDialog({
  machine,
  onRejected,
}: {
  machine: Machine
  onRejected: () => void
}) {
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
      notifyWrite('toast.rejected', { name: machine.ref.name })
      setOpen(false)
      onRejected()
    } catch (err) {
      setSubmitError(describeError(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={setOpen}
      trigger={
        <Button size="sm" variant="outline">
          {t('reject.button')}
        </Button>
      }
      title={t('reject.title', { name: machine.ref?.name ?? '' })}
      description={t('reject.description')}
      confirm={{ label: submitting ? t('reject.submitting') : t('reject.button'), disabled: submitting, onClick: submit }}
    >
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
    </ConfirmDialog>
  )
}
