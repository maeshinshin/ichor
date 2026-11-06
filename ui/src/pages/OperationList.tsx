import { useState } from 'preact/hooks'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { WorkflowIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { LinkRow, ListToolbar } from '@/components/ListToolbar'
import { PageTitle } from '@/components/PageTitle'
import { SectionCard } from '@/components/SectionCard'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { ToneBadge } from '@/components/ToneBadge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { matchesQuery } from '@/lib/filter'
import { operationState, progressCounts } from '@/lib/operations'
import { opStateTone } from '@/lib/status'
import { useList } from '@/lib/useList'
import { t } from '@/lib/i18n'
import { Link } from '@/router'

// Operation list: every TalosOperation the signed-in user may list, newest
// first (docs/adr/0068). The list is read as that user, so a cluster they
// cannot see does not appear. It is re-read every few seconds while open, since
// a rollout is the thing people watch.
export function OperationList() {
  const { items: ops, error, reload } = useList(async () => (await operationClient.listOperations({})).operations.slice().sort((a, b) => Number(b.createdAt?.seconds ?? 0n) - Number(a.createdAt?.seconds ?? 0n)), 5000)
  const [query, setQuery] = useState('')

  if (error) return <ErrorAlert {...error} />
  if (!ops) return <PageSkeleton label={t('ops.loading')} />

  const rows = ops.filter((o) =>
    matchesQuery(query, o.ref?.name, o.summary, enumLabel(OperationKind, o.kind), t(`ops.state.${operationState(o.phase, o.failureReason)}`)),
  )

  return (
    <div>
      <PageTitle title={t('ops.title')} />
      <ListToolbar query={query} onQuery={setQuery} shown={rows.length} total={ops.length} onRefresh={reload} stock />
      {ops.length === 0 ? (
        <EmptyNote icon={WorkflowIcon}>{t('ops.empty')}</EmptyNote>
      ) : rows.length === 0 ? (
        <EmptyNote>{t('list.noMatch')}</EmptyNote>
      ) : (
        <SectionCard>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('col.name')}</TableHead>
                <TableHead className="hidden sm:table-cell">{t('ops.colKind')}</TableHead>
                <TableHead className="hidden sm:table-cell">{t('ops.colState')}</TableHead>
                <TableHead className="hidden sm:table-cell">{t('ops.colProgress')}</TableHead>
                <TableHead>{t('col.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((op) => (
                <OperationRow key={`${op.ref?.namespace}/${op.ref?.name}`} op={op} onChanged={reload} />
              ))}
            </TableBody>
          </Table>
        </SectionCard>
      )}
    </div>
  )
}

function OperationRow({ op, onChanged }: { op: Operation; onChanged: () => void }) {
  const state = operationState(op.phase, op.failureReason)
  const { done, running } = progressCounts(op.machines)

  return (
    <LinkRow to={op.ref ? `/operations/${op.ref.name}` : '/operations'}>
      <TableCell className="max-sm:whitespace-normal max-sm:wrap-anywhere">
        {op.ref && (
          <Button asChild variant="link" size="sm" className="max-sm:h-auto max-sm:whitespace-normal">
            <Link to={`/operations/${op.ref.name}`}>{op.summary || op.ref.name}</Link>
          </Button>
        )}
        {op.ref && op.summary && <div class="px-3 font-mono text-xs text-muted-foreground">{op.ref.name}</div>}
        <div class="sm:hidden">
          <ToneBadge tone={opStateTone(state)}>{t(`ops.state.${state}`)}</ToneBadge>
        </div>
        <div class="text-muted-foreground sm:hidden">
          {enumLabel(OperationKind, op.kind)}
          {op.machines.length > 0 && ` · ${t('ops.progress', { done, running })}`}
        </div>
      </TableCell>
      <TableCell className="hidden sm:table-cell">{enumLabel(OperationKind, op.kind)}</TableCell>
      <TableCell className="hidden sm:table-cell">
        <ToneBadge tone={opStateTone(state)}>{t(`ops.state.${state}`)}</ToneBadge>
      </TableCell>
      <TableCell className="hidden sm:table-cell">{op.machines.length === 0 ? '–' : t('ops.progress', { done, running })}</TableCell>
      <TableCell>
        <OperationControls op={op} onChanged={onChanged} onDeleted={onChanged} />
      </TableCell>
    </LinkRow>
  )
}
