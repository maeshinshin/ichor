import { useState } from 'preact/hooks'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { CheckIcon, PencilIcon, PlusIcon, Trash2Icon } from 'lucide-preact'
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldTitle } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Item, ItemContent, ItemDescription, ItemGroup, ItemTitle } from '@/components/ui/item'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { buildSysctlDocument, defaultDocument, DOCUMENT_KINDS, kindInfo, parseSysctlParams, summarizeDocument, type SysctlRow } from '@/lib/patchDocs'
import { t } from '@/lib/i18n'

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
  const [editing, setEditing] = useState(startEditing)
  const info = kindInfo(doc.kind)
  const summary = summarizeDocument(doc.kind, doc.document)

  return (
    <Card size="sm">
      <CardHeader>
        <CardTitle>
          <Badge variant="secondary">{doc.kind}</Badge>
        </CardTitle>
        <CardDescription className="truncate">{summary || t('patch.docEmptySummary')}</CardDescription>
        <CardAction className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => setEditing(!editing)}>
            {editing ? <CheckIcon data-icon="inline-start" aria-hidden="true" /> : <PencilIcon data-icon="inline-start" aria-hidden="true" />}
            {editing ? t('patch.docDone') : t('patch.docEdit')}
          </Button>
          <Button size="sm" variant="ghost" onClick={onRemove}>
            <Trash2Icon data-icon="inline-start" aria-hidden="true" />
            {t('patch.removeDocument')}
          </Button>
        </CardAction>
      </CardHeader>

      {editing && (
        <CardContent>
          <FieldGroup>
            <Field>
              <FieldLabel>{t('patch.kindLabel')}</FieldLabel>
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
              {info && <FieldDescription>{t(info.descKey)}</FieldDescription>}
            </Field>

            {info && (
              <Field>
                <FieldTitle>{t('patch.fieldsTitle')}</FieldTitle>
                <ItemGroup>
                  {info.fields.map((f) => (
                    <Item key={f.name} size="xs">
                      <ItemContent>
                        <ItemTitle>{f.name}</ItemTitle>
                        <ItemDescription>{t(f.helpKey)}</ItemDescription>
                      </ItemContent>
                    </Item>
                  ))}
                </ItemGroup>
              </Field>
            )}

            {doc.kind === 'SysctlConfig' && parseSysctlParams(doc.document) !== null ? (
              <SysctlRows key={doc.kind} initial={parseSysctlParams(doc.document) ?? []} onChange={(rows) => onChange({ document: buildSysctlDocument(rows) })} />
            ) : (
              <Field>
                <FieldLabel>{t('patch.jsonLabel')}</FieldLabel>
                <Textarea rows={6} value={doc.document} onInput={(e) => onChange({ document: (e.target as HTMLTextAreaElement).value })} />
                <FieldDescription>{t('patch.jsonHelp')}</FieldDescription>
              </Field>
            )}
          </FieldGroup>
        </CardContent>
      )}
    </Card>
  )
}

// Key/value rows for sysctl params. The rows live here, not in the document:
// a row with an empty key is not part of the JSON yet and would vanish.
function SysctlRows({ initial, onChange }: { initial: SysctlRow[]; onChange: (rows: SysctlRow[]) => void }) {
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
            className="min-w-0 flex-1"
            aria-label={t('patch.sysctlKey')}
            placeholder="net.core.somaxconn"
            value={r.key}
            onInput={(e) => update(rows.map((x, j) => (j === i ? { ...x, key: (e.target as HTMLInputElement).value } : x)))}
          />
          <Input
            className="w-32"
            aria-label={t('patch.sysctlValue')}
            placeholder="1024"
            value={r.value}
            onInput={(e) => update(rows.map((x, j) => (j === i ? { ...x, value: (e.target as HTMLInputElement).value } : x)))}
          />
          <Button size="sm" variant="ghost" aria-label={t('patch.sysctlRemoveRow')} onClick={() => update(rows.filter((_, j) => j !== i))}>
            <Trash2Icon data-icon="inline-start" aria-hidden="true" />
            {t('patch.removeDocument')}
          </Button>
        </div>
      ))}
      <Button size="sm" variant="outline" onClick={() => update([...rows, { key: '', value: '' }])}>
        <PlusIcon data-icon="inline-start" aria-hidden="true" />
        {t('patch.sysctlAddRow')}
      </Button>
    </div>
  )
}
