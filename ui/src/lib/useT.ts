import { t } from './i18n.ts'

// useT returns t. Kept so components read their wording the same way they
// always have; there is one language, so there is nothing to subscribe to.
export function useT(): typeof t {
  return t
}
