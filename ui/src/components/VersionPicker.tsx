import { useEffect, useState } from 'preact/hooks'
import { Combobox, ComboboxCollection, ComboboxContent, ComboboxEmpty, ComboboxGroup, ComboboxInput, ComboboxItem, ComboboxLabel, ComboboxList, ComboboxSeparator } from '@/components/ui/combobox'
import type { Key } from '@/lib/i18n'
import { useT } from '@/lib/useT'
import { pickerOptions, type PickerGroup, type TargetList } from '@/lib/upgradeTargets'

const groupLabel: Record<Exclude<PickerGroup['key'], 'typed'>, Key> = {
  recommended: 'ops.picker.recommended',
  patch: 'ops.picker.patch',
  nextMinor: 'ops.picker.nextMinor',
}

// VersionPicker chooses the target of a Talos or Kubernetes upgrade: a stock combobox over the candidates
// that ListUpgradeTargets gave, grouped and filtered in the browser. A typed final version
// the list does not hold is offered as "Use vX.Y.Z"; the server plan still validates it. A list that is
// still loading, or unavailable, leaves the typed entry working and says why in a muted note.
export function VersionPicker({
  id,
  value,
  onValueChange,
  list,
}: {
  id: string
  value: string
  onValueChange: (version: string) => void
  // list is null while the candidates are read.
  list: TargetList | null
}) {
  const t = useT()
  const [typed, setTyped] = useState(value)
  // A version chosen or preselected from outside is what the input shows, and it does not filter the list.
  useEffect(() => setTyped(value), [value])

  const groups = pickerOptions(list, typed, value)

  return (
    <div class="space-y-2">
      <Combobox
        items={groups}
        filter={null}
        value={value || null}
        onValueChange={(v: string | null) => onValueChange(v ?? '')}
        inputValue={typed}
        onInputValueChange={(v: string) => setTyped(v)}
      >
        <ComboboxInput id={id} placeholder={t('ops.picker.placeholder')} className="w-full" />
        <ComboboxContent>
          <ComboboxEmpty>{t('ops.picker.empty')}</ComboboxEmpty>
          <ComboboxList>
            {(group: PickerGroup) => (
              <ComboboxGroup key={group.key} items={group.items}>
                {group.key !== 'typed' && <ComboboxLabel>{t(groupLabel[group.key])}</ComboboxLabel>}
                <ComboboxCollection>
                  {(v: string) => (
                    <ComboboxItem key={v} value={v}>
                      {group.key === 'typed' ? t('ops.picker.use', { version: v }) : v}
                    </ComboboxItem>
                  )}
                </ComboboxCollection>
                <ComboboxSeparator />
              </ComboboxGroup>
            )}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>
      {list === null ? (
        <p class="text-xs text-muted-foreground">{t('ops.picker.loading')}</p>
      ) : (
        !list.available && list.unavailableReason !== '' && <p class="text-xs text-muted-foreground">{list.unavailableReason}</p>
      )}
    </div>
  )
}
