import { timestampDate } from '@bufbuild/protobuf/wkt'
import { EmptyNote } from '@/components/PageStates'
import { StatusText } from '@/components/Status'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { Condition } from '@/gen/ichor/v1alpha1/common_pb'
import { formatDate } from '@/lib/format'
import { conditionStatusTone } from '@/lib/status'
import { useT } from '@/lib/useT'

// StatusBadge shows a condition's True/False/Unknown as an icon and word,
// coloured by whether that status is healthy for the condition's type.
export function StatusBadge({ type, status }: { type: string; status: string }) {
  return <StatusText tone={conditionStatusTone(type, status)}>{status || 'Unknown'}</StatusText>
}

// ConditionList is the one place conditions are rendered: type, status badge,
// reason, plain-text message and the last transition time.
export function ConditionList({ conditions }: { conditions: Condition[] }) {
  const t = useT()
  if (conditions.length === 0) return <EmptyNote>{t('cluster.noConditions')}</EmptyNote>
  return (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('condition.type')}</TableHead>
            <TableHead>{t('condition.status')}</TableHead>
            <TableHead>{t('condition.reason')}</TableHead>
            <TableHead>{t('condition.message')}</TableHead>
            <TableHead>{t('condition.since')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {conditions.map((c) => (
            <TableRow key={c.type}>
              <TableCell className="font-medium">{c.type}</TableCell>
              <TableCell>
                <StatusBadge type={c.type} status={c.status} />
              </TableCell>
              <TableCell className="text-muted-foreground">{c.reason || '-'}</TableCell>
              <TableCell className="min-w-64 whitespace-normal">{c.message || '-'}</TableCell>
              <TableCell className="text-muted-foreground">
                {c.lastTransitionTime ? formatDate(timestampDate(c.lastTransitionTime)) : '-'}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
  )
}
