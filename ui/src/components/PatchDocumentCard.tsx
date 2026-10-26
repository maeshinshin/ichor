import { useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { buildSysctlDocument, defaultDocument, DOCUMENT_KINDS, kindInfo, parseSysctlParams, summarizeDocument, type SysctlRow } from '@/lib/patchDocs'
import { useT } from '@/lib/useT'

export interface DocItem {
  id: number
  kind: string
  document: string
  // fresh marks a document added in this session: its editor opens at once.
  fresh?: boolean
}

// One document of a patch: a summary line (kind and key fields) with Edit and
// Remove, and an editor that opens for a new document or on Edit. The editor
// says what the chosen kind configures and names its fields.
export function PatchDocumentCard({
  doc,
  startEditing,
  onChange,
  onRemove,
}: {
  doc: DocItem
  startEditing: boolean
  onChange: (next: Partial<DocItem>) => void
  onRemove: () => void
}) {
  const t = useT()
  const [editing, setEditing] = useState(startEditing)
  const info = kindInfo(doc.kind)
  const summary = summarizeDocument(doc.kind, doc.document)

  return (
    <div class="space-y-2 rounded-md border p-3">
      <div class="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{doc.kind}</Badge>
        <span class="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground">{summary || t('patch.docEmptySummary')}</span>
        <Button size="sm" variant="outline" onClick={() => setEditing(!editing)}>
          {editing ? t('patch.docDone') : t('patch.docEdit')}
        </Button>
        <Button size="sm" variant="ghost" onClick={onRemove}>
          {t('patch.removeDocument')}
        </Button>
      </div>

      {editing && (
        <div class="space-y-3 border-t pt-3">
          <div class="space-y-1">
            <Label>{t('patch.kindLabel')}</Label>
            <Select value={doc.kind} onValueChange={(v) => onChange({ kind: v, document: defaultDocument(v) })}>
              <SelectTrigger className="w-full sm:w-64">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DOCUMENT_KINDS.map((k) => (
                  <SelectItem key={k.kind} value={k.kind}>
                    {k.kind}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {info && <p class="text-xs text-muted-foreground">{t(info.descKey)}</p>}
          </div>

          {info && (
            <div class="space-y-1">
              <p class="text-xs font-medium">{t('patch.fieldsTitle')}</p>
              <ul class="space-y-0.5 text-xs text-muted-foreground">
                {info.fields.map((f) => (
                  <li key={f.name}>
                    <span class="font-mono text-foreground">{f.name}</span>: {t(f.helpKey)}
                  </li>
                ))}
              </ul>
            </div>
          )}

          {doc.kind === 'SysctlConfig' && parseSysctlParams(doc.document) !== null ? (
            <SysctlRows key={doc.kind} initial={parseSysctlParams(doc.document) ?? []} onChange={(rows) => onChange({ document: buildSysctlDocument(rows) })} />
          ) : (
            <div class="space-y-1">
              <Label>{t('patch.jsonLabel')}</Label>
              <Textarea
                className="font-mono text-xs"
                rows={6}
                value={doc.document}
                onInput={(e) => onChange({ document: (e.target as HTMLTextAreaElement).value })}
              />
              <p class="text-xs text-muted-foreground">{t('patch.jsonHelp')}</p>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// Key/value rows for sysctl params. The rows live here, not in the document:
// a row with an empty key is not part of the JSON yet and would vanish.
function SysctlRows({ initial, onChange }: { initial: SysctlRow[]; onChange: (rows: SysctlRow[]) => void }) {
  const t = useT()
  const [rows, setRows] = useState<SysctlRow[]>(initial)

  function update(next: SysctlRow[]) {
    setRows(next)
    onChange(next)
  }

  return (
    <div class="space-y-2">
      {rows.map((r, i) => (
        <div key={i} class="flex flex-wrap items-center gap-2">
          <Input
            className="min-w-0 flex-1 font-mono text-xs"
            aria-label={t('patch.sysctlKey')}
            placeholder="net.core.somaxconn"
            value={r.key}
            onInput={(e) => update(rows.map((x, j) => (j === i ? { ...x, key: (e.target as HTMLInputElement).value } : x)))}
          />
          <Input
            className="w-32 font-mono text-xs"
            aria-label={t('patch.sysctlValue')}
            placeholder="1024"
            value={r.value}
            onInput={(e) => update(rows.map((x, j) => (j === i ? { ...x, value: (e.target as HTMLInputElement).value } : x)))}
          />
          <Button size="sm" variant="ghost" aria-label={t('patch.sysctlRemoveRow')} onClick={() => update(rows.filter((_, j) => j !== i))}>
            {t('patch.removeDocument')}
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" onClick={() => update([...rows, { key: '', value: '' }])}>
        {t('patch.sysctlAddRow')}
      </Button>
    </div>
  )
}
