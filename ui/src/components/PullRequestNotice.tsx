import { BreakGlassDialog, type BreakGlassRun } from '@/components/BreakGlassDialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { pullRequestHref } from '@/lib/pullRequest'
import { useT } from '@/lib/useT'

// Shown after a change was proposed as a pull request instead of written. Nothing follows the pull request from here; this is only
// where to find it.
export function PullRequestNotice({
  url,
  direct,
  breakGlass,
  onBreakGlassDone,
}: {
  url: string
  // direct: the change was already written (break-glass); the pull request
  // only lets the repository catch up.
  direct?: boolean
  // breakGlass offers the emergency direct write.
  breakGlass?: BreakGlassRun
  onBreakGlassDone?: () => void
}) {
  const t = useT()
  const href = pullRequestHref(url)
  return (
    <Alert className="mt-6">
      <AlertTitle>{t('pr.title')}</AlertTitle>
      <AlertDescription>
        {direct ? t('breakGlass.prBody') : t('pr.body')}{' '}
        {href ? (
          <a class="underline" href={href} target="_blank" rel="noreferrer">
            {url}
          </a>
        ) : (
          url
        )}
      </AlertDescription>
      {breakGlass && (
        <div class="mt-3">
          <BreakGlassDialog run={breakGlass} onDone={onBreakGlassDone} />
        </div>
      )}
    </Alert>
  )
}
