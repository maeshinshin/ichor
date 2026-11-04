import { useEffect, useRef, useState } from 'preact/hooks'
import { CircleCheckIcon, DownloadIcon } from 'lucide-preact'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Operation } from '@/gen/ichor/v1alpha1/operation_pb'
import { operationClient } from '@/lib/connect'
import { canContinue, canDownload, credentialLifetimes, lifetimeSeconds, type CredentialsPanel } from '@/lib/credentials'
import { download } from '@/lib/download'
import { describeError, type ErrorShape } from '@/lib/errors'
import { notifyDone, notifyFailed } from '@/lib/notify'
import { t } from '@/lib/i18n'

// The buttons of the CA rotation credential hand-off: download the new kubeconfig and
// talosconfig, and continue the rotation. The download needs the Kubernetes RBAC verb "download" on
// talosclusters/credentials, so it is rendered only when the server says the caller holds it; the server
// asks again on the call. Continue needs only what pausing an operation needs.
export function CARotationCredentials({
  op,
  panel,
  permitted,
  onChanged,
}: {
  op: Operation
  panel: Exclude<CredentialsPanel, 'none'>
  // Whether the caller holds the download verb, fetched once by the page; undefined offers nothing.
  permitted: boolean | undefined
  onChanged: (next: Operation) => void
}) {
  const ref = op.ref
  const [confirmDownload, setConfirmDownload] = useState(false)
  const [confirmContinue, setConfirmContinue] = useState(false)
  const [busy, setBusy] = useState(false)
  const [lifetime, setLifetime] = useState<string>(credentialLifetimes[0].key)
  const [readOnly, setReadOnly] = useState(false)
  const [acknowledged, setAcknowledged] = useState(false)
  const [error, setError] = useState<ErrorShape | null>(null)
  const abort = useRef<AbortController | null>(null)

  useEffect(() => () => abort.current?.abort(), [])

  async function runDownload() {
    if (!ref) return
    setConfirmDownload(false)
    const controller = new AbortController()
    abort.current = controller
    try {
      const file = await download({ kind: 'ca-credentials', operation: ref, lifetimeSeconds: lifetimeSeconds(lifetime), readOnly }, controller.signal)
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
                <DownloadIcon data-icon="inline-start" aria-hidden="true" />
                {t('creds.download')}
              </Button>
            }
            title={t('creds.downloadTitle')}
            description={t('creds.downloadDescription')}
            confirm={{ label: t('creds.downloadConfirm'), onClick: runDownload, destructive: false }}
          >
            <Field>
              <FieldLabel htmlFor="creds-lifetime">{t('creds.lifetime')}</FieldLabel>
              <Select value={lifetime} onValueChange={setLifetime}>
                <SelectTrigger id="creds-lifetime" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {credentialLifetimes.map((l) => (
                    <SelectItem key={l.key} value={l.key}>
                      {t(`creds.lifetime.${l.key}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t('creds.lifetimeHint')}</FieldDescription>
            </Field>
            <Field orientation="horizontal">
              <Checkbox id="creds-readonly" checked={readOnly} onCheckedChange={(on: boolean | 'indeterminate') => setReadOnly(on === true)} />
              <FieldLabel htmlFor="creds-readonly">{t('creds.readOnly')}</FieldLabel>
            </Field>
          </ConfirmDialog>
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
            <Alert>
              <AlertDescription>{t('creds.continueNoDownload')}</AlertDescription>
            </Alert>
            {error && <ErrorAlert {...error} />}
          </ConfirmDialog>
        )}
      </div>
      {panel === 'gate' && acknowledged && (
        <Badge variant="secondary" className="w-fit">
          <CircleCheckIcon data-icon="inline-start" aria-hidden="true" />
          {t('creds.acknowledged')}
        </Badge>
      )}
    </div>
  )
}
