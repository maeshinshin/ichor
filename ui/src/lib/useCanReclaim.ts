import { useEffect, useState } from 'preact/hooks'
import { reclaimClient } from '@/lib/connect'
import { canOfferReclaim } from '@/lib/reclaim'

// useCanReclaim asks once whether reclaim is enabled and the caller holds the verb.
// False until answered, and when the probe fails: offer nothing.
export function useCanReclaim(): boolean {
  const [can, setCan] = useState(false)

  useEffect(() => {
    let cancelled = false
    reclaimClient.canReclaim({}).then(
      (res) => {
        if (!cancelled) setCan(canOfferReclaim(res))
      },
      () => {},
    )
    return () => {
      cancelled = true
    }
  }, [])

  return can
}
