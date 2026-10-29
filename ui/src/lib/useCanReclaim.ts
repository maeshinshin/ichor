import { useEffect, useState } from 'preact/hooks'
import { reclaimClient } from '@/lib/connect'

// useCanReclaim asks once whether the caller holds the reclaim verb (RBAC alone).
// False until answered, and when the probe fails: offer nothing.
export function useCanReclaim(): boolean {
  const [can, setCan] = useState(false)

  useEffect(() => {
    let cancelled = false
    reclaimClient.canReclaim({}).then(
      (res) => {
        if (!cancelled) setCan(res.allowed)
      },
      () => {},
    )
    return () => {
      cancelled = true
    }
  }, [])

  return can
}
