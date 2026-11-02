import { reclaimClient } from '@/lib/connect'
import { useLoad } from '@/lib/useLoad'

// useCanReclaim asks once whether the caller holds the reclaim verb (RBAC alone).
// False until answered, and when the probe fails: offer nothing.
export function useCanReclaim(): boolean {
  return useLoad(() => reclaimClient.canReclaim({}), []).data?.allowed ?? false
}
