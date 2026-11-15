import { useEffect, useMemo, useState } from 'preact/hooks'
import { Command, CommandDialog, CommandEmpty, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { loadFieldIndex } from '@/lib/fieldIndex'
import { searchFields, type FieldRow } from '@/lib/fieldSearch'
import { t } from '@/lib/i18n'

// A search over every setting of a patch's config, by its path there. The query is
// matched here, over the generated index, with no round trip: shouldFilter is
// off because searchFields has already chosen and ordered the rows.
export function FieldSearchDialog({ open, onOpenChange, onSelect }: { open: boolean; onOpenChange: (open: boolean) => void; onSelect: (row: FieldRow) => void }) {
  const [query, setQuery] = useState('')
  const [rows, setRows] = useState<FieldRow[]>([])
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (open) loadFieldIndex().then(setRows, () => setFailed(true))
  }, [open])

  // A search that has been used starts the next one empty.
  useEffect(() => {
    if (!open) setQuery('')
  }, [open])

  const hits = useMemo(() => searchFields(rows, query), [rows, query])

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title={t('patch.searchTitle')} description={t('patch.searchDescription')} className="sm:max-w-xl">
      <Command shouldFilter={false}>
        <CommandInput placeholder={t('patch.searchPlaceholder')} value={query} onValueChange={setQuery} />
        <CommandList>
          {query.trim() && <CommandEmpty>{failed ? t('patch.searchLoadFailed') : t('patch.searchEmpty')}</CommandEmpty>}
          {hits.map((h) => {
            const key = h.path
            return (
              <CommandItem
                key={key}
                value={key}
                onSelect={() => {
                  onSelect(h)
                  onOpenChange(false)
                }}
              >
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="flex items-center gap-2">
                    <span className="truncate font-medium">{h.path}</span>
                    {h.type && <span className="text-xs text-muted-foreground">{h.type}</span>}
                  </span>
                  {h.description && <span className="line-clamp-2 text-xs text-muted-foreground">{h.description}</span>}
                </div>
              </CommandItem>
            )
          })}
        </CommandList>
      </Command>
    </CommandDialog>
  )
}
