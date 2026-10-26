import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { isLang, langNames, langs } from '@/lib/i18n'
import { getLang, setLang } from '@/lib/lang'
import { useT } from '@/lib/useT'

// The language switch. Each language is named in itself.
export function LanguageSwitch() {
  const t = useT()
  return (
    <Select value={getLang()} onValueChange={(v) => isLang(v) && setLang(v)}>
      <SelectTrigger size="sm" className="w-32" aria-label={t('common.language')}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {langs.map((l) => (
          <SelectItem key={l} value={l}>
            {langNames[l]}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
