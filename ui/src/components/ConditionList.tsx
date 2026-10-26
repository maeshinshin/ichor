import { timestampDate } from '@bufbuild/protobuf/wkt'
import { Badge } from '@/components/ui/badge'
import type { Condition } from '@/gen/ichor/v1alpha1/common_pb'
import { conditionTone } from '@/lib/conditions'
import { formatDate } from '@/lib/format'
import { useT } from '@/lib/useT'

// StatusBadge shows a condition's True/False/Unknown, coloured by whether that
// status is healthy for the condition's type.
export function StatusBadge({ type, status }: { type: string; status: string }) {
  const tone = conditionTone(type, status)
  const variant = tone === 'good' ? 'secondary' : tone === 'bad' ? 'destructive' : 'outline'
  return <Badge variant={variant}>{status || 'Unknown'}</Badge>
}

// ConditionList is the one place conditions are rendered: type, status badge,
// reason, plain-text message and the last transition time.
export function ConditionList({ conditions }: { conditions: Condition[] }) {
  const t = useT()
  if (conditions.length === 0) return <p class="text-muted-foreground">{t('cluster.noConditions')}</p>
  return (
    <ul class="space-y-3">
      {conditions.map((c) => (
        <li key={c.type} class="space-y-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="font-medium">{c.type}</span>
            <StatusBadge type={c.type} status={c.status} />
            {c.reason && <span class="text-xs text-muted-foreground">{c.reason}</span>}
          </div>
          {c.message && <p>{c.message}</p>}
          {c.lastTransitionTime && (
            <p class="text-xs text-muted-foreground">
              {t('condition.since')} {formatDate(timestampDate(c.lastTransitionTime))}
            </p>
          )}
        </li>
      ))}
    </ul>
  )
}
