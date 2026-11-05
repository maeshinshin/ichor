import { useState } from 'preact/hooks'
import { describeError, type ErrorShape } from '@/lib/errors'

// useAction runs one async step of a dialog: busy while it runs, a failure kept as the error to show.
// onError sees the raw error of a failed step; describe turns it into the shown text.
export function useAction(describe: (err: unknown) => ErrorShape = describeError) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  async function run(step: () => Promise<unknown>, onError?: (err: unknown) => void) {
    setBusy(true)
    setError(null)
    try {
      await step()
    } catch (err) {
      setError(describe(err))
      onError?.(err)
    } finally {
      setBusy(false)
    }
  }
  return { busy, error, setError, run }
}
