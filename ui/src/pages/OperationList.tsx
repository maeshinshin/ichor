import { useEffect, useState } from 'preact/hooks'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { DataTable, LinkRow, ListToolbar } from '@/components/ListToolbar'
import { PageHeader } from '@/components/PageHeader'
import { EmptyNote, PageSkeleton } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { ErrorAlert } from '@/components/ErrorAlert'
import { OperationControls } from '@/components/OperationControls'
import { OperationKind, type Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { enumLabel } from '@/lib/enum'
import { describeError } from '@/lib/errors'
import { matchesQuery } from '@/lib/filter'
import { operationState, progressCounts } from '@/lib/operations'
import { opStateTone } from '@/lib/status'
import { useT } from '@/lib/useT'
import { Link, objectRefPath } from '@/router'

type ErrorShape = { title: string; detail: string }

// Operation list: every TalosOperation the signed-in user may list, newest
// first (docs/adr/0068). The list is read as that user, so a cluster they
// cannot see does not appear. It is re-read every few seconds while open, since
// a rollout is the thing people watch.
export function OperationList() {
  const t = useT()
  const [ops, setOps] = useState<Operation[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  const [query, setQuery] = useState('')

  async function reload() {
    try {
      const res = await operationClient.listOperations({})
      setOps(res.operations.slice().sort((a, b) => Number(b.createdAt?.seconds ?? 0n) - Number(a.createdAt?.seconds ?? 0n)))
      setError(null)
    } catch (err) {
      setError(describeError(err))
    }
  }

  useEffect(() => {
    reload()
    const id = setInterval(reload, 5000)
    return () => clearInterval(id)
  }, [])

  if (error) return <ErrorAlert {...error} />
  if (!ops) return <PageSkeleton label={t('ops.loading')} />

  const rows = ops.filter((o) =>
    matchesQuery(query, o.ref?.name, o.cluster?.name, enumLabel(OperationKind, o.kind), t(`ops.state.${operationState(o.phase, o.failureReason)}`)),
  )

  return (
    <div>
      <PageHeader title={t('ops.title')} />
      <ListToolbar query={query} onQuery={setQuery} shown={rows.length} total={ops.length} onRefresh={reload} />
      {ops.length === 0 ? (
        <EmptyNote>{t('ops.empty')}</EmptyNote>
      ) : rows.length === 0 ? (
        <EmptyNote>{t('list.noMatch')}</EmptyNote>
      ) : (
        <DataTable>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('col.name')}</TableHead>
                <TableHead>{t('ops.colKind')}</TableHead>
                <TableHead>{t('col.cluster')}</TableHead>
                <TableHead>{t('ops.colState')}</TableHead>
                <TableHead>{t('ops.colProgress')}</TableHead>
                <TableHead>{t('col.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((op) => (
                <OperationRow key={`${op.ref?.namespace}/${op.ref?.name}`} op={op} onChanged={reload} />
              ))}
            </TableBody>
          </Table>
        </DataTable>
      )}
    </div>
  )
}

function OperationRow({ op, onChanged }: { op: Operation; onChanged: () => void }) {
  const t = useT()
  const state = operationState(op.phase, op.failureReason)
  const { done, running } = progressCounts(op.machines)

  return (
    <LinkRow to={op.ref ? objectRefPath('/operations', op.ref) : '/operations'}>
      <TableCell>
        {op.ref && (
          <Link to={objectRefPath('/operations', op.ref)} class="font-medium hover:underline">
            {op.ref.name}
          </Link>
        )}
      </TableCell>
      <TableCell>{enumLabel(OperationKind, op.kind)}</TableCell>
      <TableCell>{op.cluster?.name}</TableCell>
      <TableCell>
        <StatusText tone={opStateTone(state)}>{t(`ops.state.${state}`)}</StatusText>
      </TableCell>
      <TableCell>{t('ops.progress', { done, running })}</TableCell>
      <TableCell>
        <OperationControls op={op} onChanged={onChanged} onDeleted={onChanged} />
      </TableCell>
    </LinkRow>
  )
}
