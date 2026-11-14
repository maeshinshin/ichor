import { ToneBadge } from '@/components/ToneBadge'
import type { Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { conditionTone, readyLabel } from '@/lib/conditions'

// ReadyCell shows the machine's Ready condition (status and reason), which the
// controller does record; Talos's own stage is not read for the list.
export function ReadyCell({ machine }: { machine: Machine }) {
  const ready = machine.conditions.find((c) => c.type === 'Ready')
  if (!ready) return <>-</>
  return <ToneBadge tone={conditionTone(ready.type, ready.status)}>{readyLabel(ready)}</ToneBadge>
}
