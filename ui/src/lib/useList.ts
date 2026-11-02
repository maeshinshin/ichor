import { useEffect, useState } from 'preact/hooks'
import { describeError, type ErrorShape } from '@/lib/errors'

// useList loads a list on mount (and every pollMs when given). A failure is shown in place of the
// list until the next load succeeds; reload is for the refresh button and for after a change.
export function useList<T>(load: () => Promise<T[]>, pollMs?: number, toError: (err: unknown) => ErrorShape = describeError) {
  const [items, setItems] = useState<T[] | null>(null)
  const [error, setError] = useState<ErrorShape | null>(null)
  async function reload() {
    try {
      setItems(await load())
      setError(null)
    } catch (err) {
      setError(toError(err))
    }
  }
  useEffect(() => {
    reload()
    if (!pollMs) return
    const id = setInterval(reload, pollMs)
    return () => clearInterval(id)
  }, [])
  return { items, setItems, error, reload }
}
