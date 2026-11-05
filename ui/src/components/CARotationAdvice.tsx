import { useState } from 'preact/hooks'
import { InfoIcon } from 'lucide-preact'
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useAction } from '@/hooks/use-action'
import type { ObjectRef } from '@/gen/ichor/v1alpha1/common_pb'
import { clusterClient } from '@/lib/connect'
import { t } from '@/lib/i18n'

// CARotationAdvice is the notice an adopted cluster carries (docs/adr/0103, section 1.4): the
// previous administrators' credentials stay valid until the CAs are rotated. It is advice only;
// Dismiss writes an annotation on the TalosCluster and the controller clears the condition.
export function CARotationAdvice({ cluster }: { cluster: ObjectRef }) {
  const [dismissed, setDismissed] = useState(false)
  const { busy, error, run } = useAction()

  const dismiss = () =>
    run(async () => {
      await clusterClient.dismissCARotationAdvice({ cluster })
      setDismissed(true)
    })

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
