import { Fragment } from 'preact'
import { useState } from 'preact/hooks'
import { ChevronRightIcon } from 'lucide-preact'
import { Badge } from '@/components/ui/badge'
import { Breadcrumb, BreadcrumbItem, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from '@/components/ui/breadcrumb'
import { Button } from '@/components/ui/button'
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '@/components/ui/collapsible'
import { FieldDescription } from '@/components/ui/field'
import { childrenOf, type FieldRow, type TreeChild } from '@/lib/fieldSearch'
import { t } from '@/lib/i18n'

// The settings of spec.config as a tree from the root, one level at a time. Each level is rendered only when the
// node above it is open (the stock Collapsible unmounts closed content), so the machine branch is never 893 nodes up
// front. A node that is a setting is chosen the way a search result is. The search stays beside it.
export function ConfigTree({ rows, onSelect }: { rows: FieldRow[]; onSelect: (row: FieldRow) => void }) {
  // The path of the node last touched: the breadcrumb shows where the user is.
  const [focus, setFocus] = useState('')
  const segments = ['spec.config', ...(focus ? focus.split('.') : [])]
  return (
    <div className="space-y-2">
      <Breadcrumb>
        <BreadcrumbList>
          {segments.map((s, i) => (
            <Fragment key={i}>
              {i > 0 && <BreadcrumbSeparator />}
              <BreadcrumbItem>{i === segments.length - 1 ? <BreadcrumbPage>{s}</BreadcrumbPage> : s}</BreadcrumbItem>
            </Fragment>
          ))}
        </BreadcrumbList>
      </Breadcrumb>
      <Level rows={rows} under="" onSelect={onSelect} onFocus={setFocus} />
    </div>
  )
}

function Level({ rows, under, onSelect, onFocus }: { rows: FieldRow[]; under: string; onSelect: (row: FieldRow) => void; onFocus: (path: string) => void }) {
  return (
    <>
      {childrenOf(rows, under).map((c) => (
        <Node key={c.path} rows={rows} child={c} onSelect={onSelect} onFocus={onFocus} />
      ))}
    </>
  )
}

function Node({ rows, child, onSelect, onFocus }: { rows: FieldRow[]; child: TreeChild; onSelect: (row: FieldRow) => void; onFocus: (path: string) => void }) {
  const { seg, path, row } = child
  const entry = seg === '<key>'
  // A map's entry is named by its collection, as the key input says.
  const label = entry ? t('patch.fieldKey', { name: path.split('.').slice(-2, -1)[0] ?? '' }) : seg
  const expandable = childrenOf(rows, path).length > 0
  const required = row?.required && !entry ? <Badge variant="outline">{t('patch.fieldRequired')}</Badge> : null
  const choose = () => {
    onFocus(path)
    if (row) onSelect(row)
  }
  // A list takes one item from this tree: the first. The note says so where the user would add a second.
  const listNote = seg.endsWith('[]') ? <FieldDescription>{t('patch.treeArrayNote')}</FieldDescription> : null

  if (!expandable) {
    return (
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <Button size="sm" variant="ghost" onClick={choose}>
            {label}
          </Button>
          {required}
        </div>
        {listNote}
      </div>
    )
  }

  return (
    <Collapsible>
      <div className="flex items-center gap-2">
        <CollapsibleTrigger asChild>
          <Button size="sm" variant="ghost" onClick={() => onFocus(path)}>
            <ChevronRightIcon data-icon="inline-start" aria-hidden="true" />
            {label}
          </Button>
        </CollapsibleTrigger>
        {required}
        {row && (
          <Button size="sm" variant="outline" onClick={choose}>
            {t('patch.treeChoose')}
          </Button>
        )}
      </div>
      {listNote}
      <CollapsibleContent className="mt-1 ml-5">
        <Level rows={rows} under={path} onSelect={onSelect} onFocus={onFocus} />
      </CollapsibleContent>
    </Collapsible>
  )
}
