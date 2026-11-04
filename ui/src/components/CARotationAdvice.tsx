import { useState } from 'preact/hooks'
import { InfoIcon } from 'lucide-preact'
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { clusterClient } from '@/lib/connect'
import { describeError, type ErrorShape } from '@/lib/errors'
import { t } from '@/lib/i18n'

// CARotationAdvice is the notice an adopted cluster carries: the
// previous administrators' credentials stay valid until the CAs are rotated. It is advice only;
// Dismiss writes an annotation on the TalosCluster and the controller clears the condition.
export function CARotationAdvice({ cluster }: { cluster: ObjectRef }) {
  const [dismissed, setDismissed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)

  async function dismiss() {
    setBusy(true)
    setError(null)
    try {
      await clusterClient.dismissCARotationAdvice({ cluster })
      setDismissed(true)
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  if (dismissed) return null
  return (
    <>
      <Alert>
        <InfoIcon />
        <AlertTitle>{t('caAdvice.title')}</AlertTitle>
        <AlertDescription>{t('caAdvice.text')}</AlertDescription>
        <AlertAction>
          <Button size="xs" variant="outline" disabled={busy} onClick={dismiss}>
            {t('caAdvice.dismiss')}
          </Button>
        </AlertAction>
      </Alert>
      {error && <ErrorAlert {...error} />}
    </>
  )
}
