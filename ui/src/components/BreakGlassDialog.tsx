import { useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PullRequestNotice } from '@/components/PullRequestNotice'
import { breakGlassRequest, reasonIsValid } from '@/lib/breakGlass'
import { describeError } from '@/lib/errors'
import { useT } from '@/lib/useT'

// What a break-glass call returns: the response's pull_request_url and notice.
export type BreakGlassOutcome = { pullRequestUrl: string; notice: string }

// Repeats the caller's own request with break_glass set.
export type BreakGlassRun = (breakGlass: { reason: string }) => Promise<BreakGlassOutcome>

// The emergency path, offered where a write became a pull
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
      setOutcome(await run(breakGlassRequest(reason)))
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
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm" variant="destructive">
          {t('breakGlass.button')}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-lg">
        {outcome ? (
          <>
            <DialogHeader>
              <DialogTitle>{t('breakGlass.done')}</DialogTitle>
            </DialogHeader>
            {outcome.notice && (
              <Alert variant="destructive">
                <AlertTitle>{t('breakGlass.noticeTitle')}</AlertTitle>
                <AlertDescription>{outcome.notice}</AlertDescription>
              </Alert>
            )}
            {outcome.pullRequestUrl && <PullRequestNotice url={outcome.pullRequestUrl} direct />}
            <DialogFooter>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                {t('common.close')}
              </Button>
            </DialogFooter>
          </>
        ) : (
          <>
            <DialogHeader>
              <DialogTitle>{t('breakGlass.title')}</DialogTitle>
              <DialogDescription>{t('breakGlass.description')}</DialogDescription>
            </DialogHeader>
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
            <DialogFooter>
              <Button variant="ghost" onClick={() => setOpen(false)}>
                {t('common.cancel')}
              </Button>
              <Button variant="destructive" disabled={submitting || !reasonIsValid(reason)} onClick={submit}>
                {submitting ? t('breakGlass.submitting') : t('breakGlass.submit')}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
