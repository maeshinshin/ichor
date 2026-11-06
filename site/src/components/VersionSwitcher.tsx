import { ChevronsUpDown } from 'lucide-preact'
import { useEffect, useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { label, loadVersions, pageIn, type Version } from '@/lib/versions'

/** Which version of the docs this is, and a menu of the others (from versions.json). */
export function VersionSwitcher({ version, root }: { version: string; root: string }) {
  const [versions, setVersions] = useState<Version[]>([])
  useEffect(() => void loadVersions(root).then(setVersions), [root])
  const here = versions.find((v) => v.name === version) ?? { name: version, path: `${version}/`, isNext: version === 'next', isLatest: false }
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
            const to = versions.find((v) => v.name === name)
            if (to) location.href = await pageIn(root, version, to)
          }}
        >
          {(versions.length ? versions : [here]).map((v) => (
            <DropdownMenuRadioItem key={v.name} value={v.name}>
              {label(v)}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
