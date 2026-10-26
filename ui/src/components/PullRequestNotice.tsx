import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { pullRequestHref } from '@/lib/pullRequest'

// Shown after a change was proposed as a pull request instead of written. Nothing follows the pull request from here; this is only
// where to find it.
export function PullRequestNotice({ url }: { url: string }) {
  const href = pullRequestHref(url)
  return (
    <Alert className="mt-6">
      <AlertTitle>Pull request opened</AlertTitle>
      <AlertDescription>
        Nothing was changed in the cluster. The change applies once this pull request is merged and synced:{' '}
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
