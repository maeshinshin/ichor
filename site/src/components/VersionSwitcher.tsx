import { ChevronsUpDown } from 'lucide-preact'
import { Fragment } from 'preact'
import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { label, loadVersions, pageIn, type Version } from '@/lib/versions'

/** Which version of the docs this is, and a menu of the others (from versions.json). */
export function VersionSwitcher({ version, root }: { version: string; root: string }) {
  const [versions, setVersions] = useState<Version[]>([])
  useEffect(() => void loadVersions(root).then(setVersions), [root])
  const here = versions.find((v) => v.name === version) ?? { name: version, path: `${version}/`, isNext: version === 'next', isLatest: false }
  const minor = version.replace(/^(v\d+\.\d+)\..*/, '$1') // the line of the copy being read
  const to = (name: string) => versions.find((v) => v.name === name) ?? versions.flatMap((v) => v.patches ?? []).find((p) => p.name === name)
  const item = (v: Pick<Version, 'name' | 'isNext' | 'isLatest'>, className?: string) => (
    <DropdownMenuRadioItem key={v.name} value={v.name} className={className}>
      {label(v)}
    </DropdownMenuRadioItem>
  )
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" aria-label="Documentation version">
          {label(here)}
          <ChevronsUpDown />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-44">
        <DropdownMenuRadioGroup
          value={version}
          onValueChange={async (name) => {
            const dest = to(name)
            if (dest) location.href = await pageIn(root, version, dest)
          }}
        >
          {(versions.length ? versions : [here]).map((v) => (
            <Fragment key={v.name}>
              {item(v)}
              {v.name === minor && v.patches?.map((p) => item({ ...p, isNext: false, isLatest: false }, 'pl-11'))}
            </Fragment>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
