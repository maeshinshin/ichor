import { useEffect, useState } from 'preact/hooks'
import { describeError } from '@/lib/errors'

// useLoad runs one read call when deps change (none when load is undefined) and returns its
// result or a person-readable error; a stale answer after deps changed or unmount is dropped.
export function useLoad<T>(load: (() => Promise<T>) | undefined, deps: unknown[]) {
  const [state, setState] = useState<{ data: T | null; error: ReturnType<typeof describeError> | null }>({ data: null, error: null })
  useEffect(() => {
    let cancelled = false
    setState({ data: null, error: null })
    load?.().then(
      (data) => !cancelled && setState({ data, error: null }),
      (err) => !cancelled && setState({ data: null, error: describeError(err) }),
    )
    return () => {
      cancelled = true
    }
  }, deps)
  return state
}
