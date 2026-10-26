import { useEffect, useState } from 'preact/hooks'
import type { Key, Params } from './i18n.ts'
import { getLang, subscribeLang, t } from './lang.ts'

// useT returns t, and redraws the calling component when the language changes.
export function useT(): typeof t {
  const [, setLang] = useState(getLang())
  useEffect(() => subscribeLang(() => setLang(getLang())), [])
  return (key: Key, params?: Params) => t(key, params)
}
