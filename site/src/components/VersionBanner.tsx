import { Info } from 'lucide-preact'
import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { loadVersions, pageIn, type Version } from '@/lib/versions'

/**
 * A notice on the development docs, and on a release that is not the newest any more. `line` and `patch` are the newest
 * release as the build knew it: the notice is complete in the HTML from the start, and the versions.json the browser fetches
 * only replaces them when a release has been made since (a banner that grows or appears late moves the page under the reader).
 */
export function VersionBanner({ version, root, line, patch }: { version: string; root: string; line?: string; patch?: string }) {
  const [latest, setLatest] = useState<Version | undefined>(
    line && patch ? { name: line, path: `${line}/`, patch, patches: [{ name: patch, path: `${patch}/` }], isLatest: true, isNext: false } : undefined,
  )
  useEffect(() => void loadVersions(root).then((vs) => setLatest((was) => vs.find((v) => v.isLatest) ?? was)), [root])
  const link = latest && (
    <a
      href={`${root}${latest.path}`}
      className="underline underline-offset-4"
      onClick={async (e) => {
        e.preventDefault()
        location.href = await pageIn(root, version, latest)
      }}
    >
      {latest.name}
    </a>
  )
  if (version === 'next') {
    return (
      <Alert>
        <Info />
        <AlertTitle>Unreleased (main)</AlertTitle>
        <AlertDescription>These are the development docs; they can describe changes no release has yet.{link && <> The latest release is {link}.</>}</AlertDescription>
      </Alert>
    )
  }
  if (!latest || latest.name === version || latest.patches?.[0]?.name === version) return null // the newest patch of the latest line is current
  return (
    <Alert>
      <Info />
      <AlertTitle>You are viewing the docs for {version}</AlertTitle>
      <AlertDescription>The latest is {link}.</AlertDescription>
    </Alert>
  )
}
