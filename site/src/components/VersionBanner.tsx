import { Info } from 'lucide-preact'
import { useEffect, useState } from 'preact/hooks'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { loadVersions, pageIn, type Version } from '@/lib/versions'

/** A notice on the development docs, and on a release that is not the newest any more. */
export function VersionBanner({ version, root }: { version: string; root: string }) {
  const [latest, setLatest] = useState<Version>()
  useEffect(() => void loadVersions(root).then((vs) => setLatest(vs.find((v) => v.isLatest))), [root])
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
  if (!latest || latest.name === version) return null
  return (
    <Alert>
      <Info />
      <AlertTitle>You are viewing the docs for {version}</AlertTitle>
      <AlertDescription>The latest is {link}.</AlertDescription>
    </Alert>
  )
}
