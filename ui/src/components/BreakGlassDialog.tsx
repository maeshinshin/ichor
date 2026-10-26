import { useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import { breakGlassRequest, reasonIsValid } from '@/lib/breakGlass'
import { notifyDone } from '@/lib/notify'
import { describeError } from '@/lib/errors'
import { useT } from '@/lib/useT'

// What a break-glass call returns: the response's pull_request_url and notice.
export type BreakGlassOutcome = { pullRequestUrl: string; notice: string }

// Repeats the caller's own request with break_glass set.
export type BreakGlassRun = (breakGlass: { reason: string }) => Promise<BreakGlassOutcome>

// The emergency path (docs/adr/0065), offered where a write became a pull
// request or was refused because Argo CD tracks the resource. It is not
// checked beforehand whether the user may: the button is always offered and
// Kubernetes' refusal, with its reason, is shown here if they may not.
export function BreakGlassDialog({ run, onDone }: { run: BreakGlassRun; onDone?: () => void }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)
  const [outcome, setOutcome] = useState<BreakGlassOutcome | null>(null)

  async function submit() {
    if (!reasonIsValid(reason)) return
    setSubmitting(true)
    setError(null)
    try {
      const out = await run(breakGlassRequest(reason))
      setOutcome(out)
      notifyDone(out.pullRequestUrl ? t('toast.breakGlassPr') : t('toast.breakGlass'))
    } catch (err) {
      setError(describeError(err))
    } finally {
      setSubmitting(false)
    }
  }

  function onOpenChange(next: boolean) {
    setOpen(next)
    if (!next && outcome) onDone?.()
  }

  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      trigger={
        <Button size="sm" variant="destructive">
          {t('breakGlass.button')}
        </Button>
      }
      title={outcome ? t('breakGlass.done') : t('breakGlass.title')}
      description={outcome ? undefined : t('breakGlass.description')}
      closeLabel={outcome ? t('common.close') : undefined}
      confirm={
        outcome
          ? undefined
          : { label: submitting ? t('breakGlass.submitting') : t('breakGlass.submit'), disabled: submitting || !reasonIsValid(reason), onClick: submit }
      }
    >
      {outcome ? (
        <>
          {outcome.notice && (
            <Alert variant="destructive">
              <AlertTitle>{t('breakGlass.noticeTitle')}</AlertTitle>
              <AlertDescription>{outcome.notice}</AlertDescription>
            </Alert>
          )}
          {outcome.pullRequestUrl && <PullRequestNotice url={outcome.pullRequestUrl} direct />}
        </>
      ) : (
        <>
          <p class="text-sm text-muted-foreground">{t('breakGlass.selfHeal')}</p>
          <div class="space-y-1">
            <Label htmlFor="break-glass-reason">{t('breakGlass.reason')}</Label>
            <Textarea
              id="break-glass-reason"
              value={reason}
              required
              onInput={(e) => setReason((e.target as HTMLTextAreaElement).value)}
              placeholder={t('breakGlass.reasonPlaceholder')}
            />
          </div>
          {error && <ErrorAlert {...error} />}
        </>
      )}
    </ConfirmDialog>
  )
}
