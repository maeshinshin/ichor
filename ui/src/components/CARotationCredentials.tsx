import { useEffect, useRef, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { canContinue, canDownload, type CredentialsPanel } from '@/lib/credentials'
import { download } from '@/lib/download'
import { describeError } from '@/lib/errors'
import { notifyDone, notifyFailed } from '@/lib/notify'
import { useT } from '@/lib/useT'

type ErrorShape = { title: string; detail: string }

// The buttons of the CA rotation credential hand-off: download the new kubeconfig and
// talosconfig, and continue the rotation. The download needs the Kubernetes RBAC verb "download" on
// talosclusters/credentials, so it is rendered only when the server says the caller holds it; the server
// asks again on the call. Continue needs only what pausing an operation needs.
export function CARotationCredentials({
  op,
  panel,
  onChanged,
}: {
  op: Operation
  panel: Exclude<CredentialsPanel, 'none'>
  onChanged: (next: Operation) => void
}) {
  const t = useT()
  const ref = op.ref
  const [permitted, setPermitted] = useState<boolean | undefined>(undefined)
  const [confirmDownload, setConfirmDownload] = useState(false)
  const [confirmContinue, setConfirmContinue] = useState(false)
  const [busy, setBusy] = useState(false)
  const [acknowledged, setAcknowledged] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => {
    let cancelled = false
    setPermitted(undefined)
    if (ref) {
      operationClient.getCARotationCredentialsPermission({ operation: ref }).then(
        (res) => {
          if (!cancelled) setPermitted(res.download)
        },
        () => {},
      )
    }
    return () => {
      cancelled = true
      abort.current?.abort()
    }
  }, [ref?.namespace, ref?.name])

  async function runDownload() {
    if (!ref) return
    setConfirmDownload(false)
    const controller = new AbortController()
    abort.current = controller
    try {
      const file = await download({ kind: 'ca-credentials', operation: ref }, controller.signal)
      notifyDone(t('creds.downloaded', { file }))
    } catch (err) {
      if (controller.signal.aborted) return
      const e = describeError(err)
      notifyFailed(t('creds.downloadFailed'), e.detail || e.title)
    }
  }

  async function runContinue() {
    if (!ref) return
    setBusy(true)
    setError(null)
    try {
      const res = await operationClient.acknowledgeCARotationCredentials({ operation: ref })
      if (res.operation) onChanged(res.operation)
      setAcknowledged(true)
      setConfirmContinue(false)
      notifyDone(t('creds.continued'))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setBusy(false)
    }
  }

  const showDownload = canDownload(panel, permitted)
  const showContinue = canContinue(panel, acknowledged)
  if (!showDownload && !showContinue && !(panel === 'gate' && acknowledged)) return null

  return (
    <div class="mt-3 flex flex-col gap-2">
      <div class="flex flex-wrap gap-2">
        {showDownload && (
          <ConfirmDialog
            open={confirmDownload}
            onOpenChange={setConfirmDownload}
            trigger={
              <Button size="sm" variant="outline">
                {t('creds.download')}
              </Button>
            }
            title={t('creds.downloadTitle')}
            description={t('creds.downloadDescription')}
            confirm={{ label: t('creds.downloadConfirm'), onClick: runDownload, destructive: false }}
          />
        )}
        {showContinue && (
          <ConfirmDialog
            open={confirmContinue}
            onOpenChange={(open) => {
              setConfirmContinue(open)
              if (!open) setError(null)
            }}
            trigger={
              <Button size="sm" variant="destructive">
                {t('creds.continue')}
              </Button>
            }
            title={t('creds.continueTitle')}
            description={t('creds.continueDescription')}
            confirm={{ label: busy ? t('creds.continuing') : t('creds.continueConfirm'), onClick: runContinue, disabled: busy }}
          >
            <p class="text-sm text-muted-foreground">{t('creds.continueNoDownload')}</p>
            {error && <ErrorAlert {...error} />}
          </ConfirmDialog>
        )}
      </div>
      {panel === 'gate' && acknowledged && <p class="text-sm">{t('creds.acknowledged')}</p>}
    </div>
  )
}
