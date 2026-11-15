import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Trash2Icon } from 'lucide-preact'
import { editablePath, getField, inputKind, setField, type FieldRow } from '@/lib/fieldSearch'
import { t } from '@/lib/i18n'

// One chosen field of a document, edited with the control its type calls for
// (lib/fieldSearch inputKind). It reads and writes the document's JSON text, so
// the JSON box beside it always shows the same document.
export function FieldInput({ row, document, onChange, onRemove }: { row: FieldRow; document: string; onChange: (document: string) => void; onRemove: () => void }) {
  const path = editablePath(row)
  const id = `field-${row.path}`
  const current = getField(document, path)
  const write = (value: unknown) => {
    const next = setField(document, path, value)
    if (next !== null) onChange(next)
  }

  let control
  switch (inputKind(row)) {
    case 'select':
      control = (
        <Select value={typeof current === 'string' ? current : ''} onValueChange={write}>
          <SelectTrigger id={id} className="w-full min-w-0 *:data-[slot=select-value]:block *:data-[slot=select-value]:min-w-0 *:data-[slot=select-value]:truncate">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {row.values!.map((v) => (
              <SelectItem key={v} value={v}>
                {v}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )
      break
    case 'switch':
      control = <Switch id={id} checked={current === true} onCheckedChange={write} />
      break
    case 'number':
      control = (
        <Input
          id={id}
          type="number"
          value={current === undefined ? '' : String(current)}
          onInput={(e) => {
            const v = (e.target as HTMLInputElement).value
            write(v === '' ? undefined : Number(v))
          }}
        />
      )
      break
    case 'text':
      control = <Input id={id} value={current === undefined ? '' : String(current)} onInput={(e) => write((e.target as HTMLInputElement).value || undefined)} />
      break
    default:
      control = <JsonControl id={id} value={current} onValue={write} />
  }

  return (
    <Field>
      <div className="flex items-center justify-between gap-2">
        <FieldLabel htmlFor={id}>
          {row.path}
          {row.required && <span className="text-xs font-normal text-muted-foreground">{t('patch.fieldRequired')}</span>}
        </FieldLabel>
        <Button size="sm" variant="ghost" onClick={() => (write(undefined), onRemove())} aria-label={t('patch.fieldRemove')}>
          <Trash2Icon aria-hidden="true" />
        </Button>
      </div>
      {control}
      {row.description && <FieldDescription className="line-clamp-3">{row.description}</FieldDescription>}
    </Field>
  )
}

// An object, an array or a map is edited as JSON, the same Textarea the whole
// document has. The text is kept here: while it does not parse, the document
// keeps the last value that did.
function JsonControl({ id, value, onValue }: { id: string; value: unknown; onValue: (value: unknown) => void }) {
  const [text, setText] = useState(value === undefined ? '' : JSON.stringify(value))
  const [invalid, setInvalid] = useState(false)

  return (
    <>
      <Textarea
        id={id}
        rows={3}
        value={text}
        aria-invalid={invalid}
        onInput={(e) => {
          const v = (e.target as HTMLTextAreaElement).value
          setText(v)
          if (v.trim() === '') {
            setInvalid(false)
            onValue(undefined)
            return
          }
          try {
            onValue(JSON.parse(v))
            setInvalid(false)
          } catch {
            setInvalid(true)
          }
        }}
      />
      {invalid && <FieldDescription>{t('patch.fieldJsonInvalid')}</FieldDescription>}
    </>
  )
}
