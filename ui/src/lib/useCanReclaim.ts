import { reclaimClient } from '@/lib/connect'
import { useLoad } from '@/lib/useLoad'

// useCanReclaim asks once whether the caller holds the reclaim verb (ADR 0103, 2.2; ADR 0107: RBAC alone).
// False until answered, and when the probe fails: offer nothing.
export function useCanReclaim(): boolean {
  return useLoad(() => reclaimClient.canReclaim({}), []).data?.allowed ?? false
}
