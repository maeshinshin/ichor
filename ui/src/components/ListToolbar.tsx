import type { ComponentChildren } from 'preact'
import { RefreshCwIcon, SearchIcon } from 'lucide-preact'
import { Button } from '@/components/ui/button'
import { InputGroup, InputGroupAddon, InputGroupInput } from '@/components/ui/input-group'
import { TableRow } from '@/components/ui/table'
import { useT } from '@/lib/useT'
import { navigate } from '@/router'

// ListToolbar sits above every list table: a search box, the count of rows
// shown, and on the right the page's primary action and a refresh button.
export function ListToolbar({
  query,
  onQuery,
  shown,
  total,
  onRefresh,
  children,
}: {
  query: string
  onQuery: (q: string) => void
  shown: number
  total: number
  onRefresh?: () => void
  children?: ComponentChildren
}) {
  const t = useT()
  return (
    <div class="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2">
      <InputGroup className="w-full sm:w-72">
        <InputGroupAddon>
          <SearchIcon />
        </InputGroupAddon>
        <InputGroupInput
          type="search"
          value={query}
          placeholder={t('list.search')}
          aria-label={t('list.search')}
          onInput={(e) => onQuery((e.currentTarget as HTMLInputElement).value)}
        />
      </InputGroup>
      <span class="text-sm text-muted-foreground" aria-live="polite">
        {shown === total ? t('list.count', { n: total }) : t('list.countOf', { shown, total })}
      </span>
      <div class="ml-auto flex items-center gap-2">
        {children}
        {onRefresh && (
          <Button variant="outline" size="icon" aria-label={t('list.refresh')} onClick={onRefresh}>
            <RefreshCwIcon />
          </Button>
        )}
      </div>
    </div>
  )
}

// DataTable frames a table as one bordered block (no card around a card); the
// table scrolls sideways inside it on a phone.
export function DataTable({ children }: { children: ComponentChildren }) {
  return <div class="overflow-hidden rounded-md border bg-card [&_td]:px-3 [&_td]:py-2.5 [&_th]:h-9 [&_th]:bg-muted/40 [&_th]:px-3 [&_th]:text-xs [&_th]:font-medium [&_th]:text-muted-foreground [&_tr:last-child]:border-b-0">{children}</div>
}

// LinkRow is a table row that opens `to` when clicked anywhere; links and
// buttons inside it keep their own behaviour, and the name cell's link stays
// the keyboard route in.
export function LinkRow({ to, children }: { to: string; children: ComponentChildren }) {
  return (
    <TableRow
      className="cursor-pointer"
      onClick={(e) => {
        if (!(e.target as HTMLElement).closest('a,button,input,[role=dialog]')) navigate(to)
      }}
    >
      {children}
    </TableRow>
  )
}
