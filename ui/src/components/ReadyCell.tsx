import { StatusText } from '@/components/Status'
import type { Machine } from '@/gen/ichor/v1alpha1/machine_pb'
import { conditionStatusTone } from '@/lib/status'

// ReadyCell shows the machine's Ready condition (status and reason), which the
// controller does record; Talos's own stage is not read for the list (ADR 0071).
export function ReadyCell({ machine }: { machine: Machine }) {
  const ready = machine.conditions.find((c) => c.type === 'Ready')
  if (!ready) return <>-</>
  return (
    <span class="inline-flex items-center gap-2">
      <StatusText tone={conditionStatusTone(ready.type, ready.status)}>{ready.reason || ready.status}</StatusText>
    </span>
  )
}
