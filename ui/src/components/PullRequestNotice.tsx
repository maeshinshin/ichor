import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { pullRequestHref } from '@/lib/pullRequest'
import { useT } from '@/lib/useT'

// Shown after a change was proposed as a pull request instead of written. Nothing follows the pull request from here; this is only
// where to find it.
export function PullRequestNotice({ url }: { url: string }) {
  const t = useT()
  const href = pullRequestHref(url)
  return (
    <Alert className="mt-6">
      <AlertTitle>{t('pr.title')}</AlertTitle>
      <AlertDescription>
        {t('pr.body')}{' '}
        {href ? (
          <a class="underline" href={href} target="_blank" rel="noreferrer">
            {url}
          </a>
        ) : (
          url
        )}
      </AlertDescription>
    </Alert>
  )
}
