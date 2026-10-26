import { useEffect, useState } from 'preact/hooks'
import { getPageTitles, subscribePageTitles } from './pageTitle.ts'

// usePageTitles re-renders the caller when a page publishes or clears a title.
export function usePageTitles(): Readonly<Record<string, string>> {
  const [titles, setTitles] = useState(getPageTitles())
  useEffect(() => subscribePageTitles(() => setTitles(getPageTitles())), [])
  return titles
}
