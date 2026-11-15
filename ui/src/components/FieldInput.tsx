import { useState } from 'preact/hooks'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Trash2Icon } from 'lucide-preact'
import { complete, getField, inputKind, isEntry, setField, steps, type Chosen } from '@/lib/fieldSearch'
import { t } from '@/lib/i18n'

// One chosen setting of the config, edited with the control its type calls for
// (lib/fieldSearch inputKind). It reads and writes the config's JSON text, so the
// JSON box beside it always shows the same config. A setting in a map is under a
// key, which is named here before there is anything to write.
export function FieldInput({
  chosen,
  config,
  onChange,
  onKey,
  onRemove,
}: {
  chosen: Chosen
  config: string
  onChange: (config: string) => void
  onKey: (i: number, key: string) => void
  onRemove: () => void
}) {
  const { row } = chosen
  const path = steps(row.path, chosen.keys)
  const id = `field-${chosen.id}`
  const ready = complete(chosen)
  const current = ready ? getField(config, path) : undefined
  const write = (value: unknown) => {
    const next = ready ? setField(config, path, value) : null
    if (next !== null) onChange(next)
  }
  // The names of the maps the setting is in, one for each key to name.
  const maps = row.path.split('.').flatMap((seg, i, all) => (seg === '<key>' ? [all.slice(0, i).join('.')] : []))

  let control
  switch (inputKind(row)) {
    case 'key':
      control = null
      break
    case 'select':
      control = (
        <Select value={typeof current === 'string' ? current : ''} onValueChange={write} disabled={!ready}>
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
      control = <Switch id={id} checked={current === true} disabled={!ready} onCheckedChange={write} />
      break
    case 'number':
      control = (
        <Input
          id={id}
          type="number"
          disabled={!ready}
          value={current === undefined ? '' : String(current)}
          onInput={(e) => {
            const v = (e.target as HTMLInputElement).value
            write(v === '' ? undefined : Number(v))
          }}
        />
      )
      break
    case 'text':
      control = <Input id={id} disabled={!ready} value={current === undefined ? '' : String(current)} onInput={(e) => write((e.target as HTMLInputElement).value || undefined)} />
      break
    default:
      control = <JsonControl id={id} value={current} disabled={!ready} onValue={write} />
  }

  return (
    <Field>
      <div className="flex items-center justify-between gap-2">
        <FieldLabel htmlFor={isEntry(row) ? `${id}-key-${maps.length - 1}` : id}>
          {row.path}
          {row.required && <span className="text-xs font-normal text-muted-foreground">{t('patch.fieldRequired')}</span>}
        </FieldLabel>
        <Button size="sm" variant="ghost" onClick={onRemove} aria-label={t('patch.fieldRemove')}>
          <Trash2Icon aria-hidden="true" />
        </Button>
      </div>
      {maps.map((name, i) => (
        <KeyInput key={`${i}:${chosen.keys[i]}`} id={`${id}-key-${i}`} label={t('patch.fieldKey', { name })} value={chosen.keys[i]} onCommit={(key) => onKey(i, key)} />
      ))}
      {control}
      {!ready && !isEntry(row) && <FieldDescription>{t('patch.fieldNeedsKey')}</FieldDescription>}
      {row.description && <FieldDescription className="line-clamp-3">{row.description}</FieldDescription>}
    </Field>
  )
}

// The key a setting is under, taken when it is left or entered: while it is typed it is
// not yet a name, and the entry is not moved on each letter.
function KeyInput({ id, label, value, onCommit }: { id: string; label: string; value: string; onCommit: (key: string) => void }) {
  const [draft, setDraft] = useState(value)
  const commit = () => {
    const key = draft.trim()
    if (key === '') setDraft(value)
    else onCommit(key)
  }

  return (
    <Input
      id={id}
      aria-label={label}
      placeholder={label}
      value={draft}
      onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === 'Enter' && commit()}
    />
  )
}

// An object, an array or a map is edited as JSON, the same Textarea the whole
// document has. The text is kept here: while it does not parse, the document
// keeps the last value that did.
function JsonControl({ id, value, disabled, onValue }: { id: string; value: unknown; disabled: boolean; onValue: (value: unknown) => void }) {
  const [text, setText] = useState(value === undefined ? '' : JSON.stringify(value))
  const [invalid, setInvalid] = useState(false)

  return (
    <>
      <Textarea
        id={id}
        rows={3}
        disabled={disabled}
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
