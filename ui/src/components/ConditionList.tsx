import { timestampDate } from '@bufbuild/protobuf/wkt'
import { EmptyNote } from '@/components/PageStates'
import { ToneBadge } from '@/components/ToneBadge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { Condition } from '@/gen/ichor/v1alpha1/common_pb'
import { formatDate } from '@/lib/format'
import { conditionTone } from '@/lib/conditions'
import { t } from '@/lib/i18n'

// ConditionBadge shows a condition's True/False/Unknown as a stock Badge, coloured by whether that status is healthy for its type.
export function ConditionBadge({ type, status }: { type: string; status: string }) {
  return <ToneBadge tone={conditionTone(type, status)}>{status || 'Unknown'}</ToneBadge>
}

// ConditionList is the one place conditions are rendered: type, status badge,
// reason, plain-text message and the last transition time.
export function ConditionList({ conditions }: { conditions: Condition[] }) {
  if (conditions.length === 0) return <EmptyNote>{t('cluster.noConditions')}</EmptyNote>
  return (
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('condition.type')}</TableHead>
            <TableHead>{t('condition.status')}</TableHead>
            <TableHead className="max-md:hidden">{t('condition.reason')}</TableHead>
            <TableHead className="max-md:hidden">{t('condition.message')}</TableHead>
            <TableHead className="max-md:hidden">{t('condition.since')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {conditions.map((c) => (
            <TableRow key={c.type}>
              <TableCell className="font-medium max-md:whitespace-normal">
                {c.type}
                <div class="font-normal text-muted-foreground md:hidden">{c.reason || '-'}</div>
                <div class="font-normal text-muted-foreground md:hidden">{c.message || '-'}</div>
                <div class="font-normal text-muted-foreground md:hidden">{c.lastTransitionTime ? formatDate(timestampDate(c.lastTransitionTime)) : '-'}</div>
              </TableCell>
              <TableCell>
                <ConditionBadge type={c.type} status={c.status} />
              </TableCell>
              <TableCell className="text-muted-foreground max-md:hidden">{c.reason || '-'}</TableCell>
              <TableCell className="min-w-64 whitespace-normal max-md:hidden">{c.message || '-'}</TableCell>
              <TableCell className="text-muted-foreground max-md:hidden">
                {c.lastTransitionTime ? formatDate(timestampDate(c.lastTransitionTime)) : '-'}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
  )
}
